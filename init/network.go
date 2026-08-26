package main

// Network setup: from no configuration to a routed interface, in
// userspace Go.
//
// This file uses two different kernel interfaces. Neither is a
// classic syscall. Interface configuration (links up, addresses,
// routes) happens over netlink, a socket-based protocol that the
// kernel implements. The `ip` command uses netlink too. The
// vishvananda/netlink library builds the netlink messages.
//
// Getting an address by DHCP works differently. DHCP is a network
// protocol, not a kernel feature, so liken must implement the client
// side itself: broadcast DISCOVER, receive OFFER, send REQUEST,
// receive ACK. The insomniacslk/dhcp library does this work (the
// same library Talos uses to boot). It sends over a raw AF_PACKET
// socket. This socket type lets a machine send UDP before it has an
// IP address to send from.
//
// A static address is the simplest case: it uses no protocol, only
// the netlink calls that apply an address someone already chose.
// Static addressing exists because clustering needs it. A machine's
// peers must have its address before it boots, so the manifest must
// declare the address instead of negotiating it on the wire. The lab
// also needs static addressing, for a different reason: the network
// segment that joins the QEMU guests has no DHCP server on it.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// connection holds the facts that the code gathers while it brings up
// one interface. These facts are enough to print a report of how the
// machine connects to the network, and to publish the same facts to
// the Machine's status.
type connection struct {
	ifname string
	mac    net.HardwareAddr

	// addr may be nil. A radio that did not join leaves an interface
	// that exists and has no address, and that state is exactly what
	// the status must report, so the boot keeps the connection and
	// every reader checks this field before it reads one.
	addr        *net.IPNet
	method      machine.AddressMethod // how the code obtained the address: DHCP or Static
	gateway     net.IP
	nameservers []net.IP
	leaseTime   time.Duration
	server      net.IP

	// leaseExpires is fixed at the moment the DHCP ACK lands. The
	// facts render from this absolute time, so a rewrite hours later
	// reports the same expiry; deriving it from the clock at render
	// time would move every lease forward with no DHCP exchange.
	leaseExpires time.Time

	// radio is the 802.11 session behind this interface, for an
	// interface the spec gave a wireless entry. Nil means the
	// interface is wired.
	radio *radio
}

// bringUpNetwork configures every interface that the spec names.
// When the spec names no interface, it uses the zero-configuration
// default: DHCP on the first interface that looks like real
// hardware. If one interface fails, the function still configures
// the others. A machine with its uplink down but its cluster segment
// up is degraded, not absent, and the console report shows which
// interface is which. The function returns an error only when no
// interface comes up.
//
// The bring-up runs in two passes (radios.go): wired interfaces
// settle here, and radios settle behind the boot when the machine
// already has a route toward its cluster. The second return value is
// that background pass, nil when nothing runs behind; the caller
// hands it to the component that lands each radio's verdict in the
// facts tree after the boot's own facts publish.
//
// The whole cluster document comes in rather than the endpoint
// alone, because the pass split reads two of its facts: the endpoint
// for the route question, and the nodeCIDR for the node-address
// question (radios.go, backgroundable).
func bringUpNetwork(spec machine.NetworkSpec, clusterDoc *cluster.Cluster) ([]*connection, *radioPass, error) {
	// The code brings up loopback first. Nearly all networked
	// software assumes that 127.0.0.1 exists. The kernel creates the
	// loopback interface; the code only needs to raise it.
	if lo, err := linkByName("lo"); err == nil {
		if err := linkSetUp(lo); err != nil {
			return nil, nil, fmt.Errorf("raising lo: %w", err)
		}
	}

	// A spec that cannot be right is refused before any link changes.
	// The same check runs in the cluster, where the operator refuses
	// to stage such a spec, but the boot cannot rely on that: init
	// also reads manifests that were written by hand and carried in
	// on a stick, which no API server ever saw.
	if err := spec.Validate(); err != nil {
		return nil, nil, err
	}

	// The kernel's link list is read once and used for every
	// interface below. Reading it once also means that every error
	// message can list the same set of ports, which is the list a
	// person needs to correct the manifest.
	links, err := netlink.LinkList()
	if err != nil {
		return nil, nil, fmt.Errorf("listing interfaces: %w", err)
	}
	present := presentInterfaces(links)

	interfaces := spec.Interfaces
	if len(interfaces) == 0 {
		name, err := pickInterface(present)
		if err != nil {
			return nil, nil, err
		}
		interfaces = []machine.InterfaceSpec{{Name: name}}
	}

	conns, pass := bootPasses(present).run(interfaces, clusterDoc)

	if !anyAddressed(conns) {
		return conns, pass, fmt.Errorf("no interface came up")
	}
	if err := writeResolvConf(conns); err != nil {
		return conns, pass, err
	}
	return conns, pass, nil
}

// resolvConfPath is where the resolver file goes. It is a variable
// rather than a constant so a test can write into a file of its own,
// the way wirelessRunDir and parkConsole let tests stand in for the
// real machine.
var resolvConfPath = "/etc/resolv.conf"

// The code builds one resolv.conf file for the whole machine,
// gathered from every interface. The resolvConf function below
// explains which nameservers it keeps. The file is an ordinary
// file. Resolvers, including Go's own resolver, read it by
// convention.
//
// The boot writes the file once, from the pass one connections. A
// radio that settles late renders it again through this same
// function, so its nameservers join the file under the same order
// and the same cap as every other interface's. That late rewrite
// happens while k3s and the kubelet run, and a pod sandbox created
// mid-write copies whatever the file holds, which is why the write
// below is atomic.
func writeResolvConf(conns []*connection) error {
	content := resolvConf(conns)
	if content == "" {
		return nil
	}
	return writeResolvConfAtomic(resolvConfPath, []byte(content))
}

// writeResolvConfAtomic writes one file through a temp file in the
// same directory and a rename, the pattern machine/staging.go's
// writeAtomic uses for the facts tree.
//
// A rename inside one filesystem replaces the file in a single step,
// so a reader opens either the old content or the new, never an
// empty or partial file. A plain WriteFile truncates first, and a
// reader in that window gets an empty resolv.conf for the life of
// its copy.
func writeResolvConfAtomic(path string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".liken-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	// CreateTemp makes the file owner-only, and every resolver on
	// the machine reads this file, so the mode is set before the
	// rename publishes it.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Rename(tmp.Name(), path)
}

// resolvConf renders the machine's resolv.conf file from its
// connections' nameservers. It includes nameservers from DHCP leases
// and from manifest declarations, in interface order. It removes
// duplicates and keeps at most three nameservers.
//
// Three is the oldest hard limit in the resolver world. Since the
// 1980s, glibc has read at most MAXNS=3 nameservers, and other libc
// stacks follow the same limit. Kubernetes also truncates every
// pod's nameserver list to three, and it logs a warning at each sync
// when a node's file offers more.
//
// Some networks do offer more than three nameservers. For example,
// Linode's DHCP service hands out its whole regional fleet: eighteen
// resolvers in one lease. Writing all eighteen would not change how
// names resolve, and it would cost a warning every minute, forever.
// Interface order is priority order, so the cap of three keeps the
// same resolvers that the machine would consult anyway.
func resolvConf(conns []*connection) string {
	const maxNameservers = 3
	var b strings.Builder
	seen := map[string]bool{}
	for _, conn := range conns {
		for _, ns := range conn.nameservers {
			if len(seen) == maxNameservers || seen[ns.String()] {
				continue
			}
			seen[ns.String()] = true
			fmt.Fprintf(&b, "nameserver %s\n", ns)
		}
	}
	return b.String()
}

// The two netlink calls the passes make, as variables so a test can
// run the whole bring-up with no kernel, including a raise that
// never returns.
var (
	linkByName = netlink.LinkByName
	linkSetUp  = netlink.LinkSetUp
)

// bringUpInterface raises one link and gives it an address, using
// the method that the interface spec chose. Only pass one calls it:
// a wired raise returns in milliseconds, so it runs with no deadline
// machinery, and the wireless half of what this function once did
// lives in bringUpRadio (radios.go).
func bringUpInterface(ifc machine.InterfaceSpec, present []interfaceIdentity) (*connection, error) {
	if err := requirePort(ifc.Name, present); err != nil {
		return nil, err
	}
	link, err := linkByName(ifc.Name)
	if err != nil {
		return nil, fmt.Errorf("opening interface %q: %w", ifc.Name, err)
	}
	fmt.Printf("liken: bringing up %s\n", ifc.Name)
	if err := linkSetUp(link); err != nil {
		return nil, fmt.Errorf("raising %s: %w", ifc.Name, err)
	}
	return addressInterface(link, ifc)
}

// addressInterface gives one raised link its address, by the method the
// interface spec chose. It is the half of the bring-up that a wireless
// interface reaches only after its radio associates.
func addressInterface(link netlink.Link, ifc machine.InterfaceSpec) (*connection, error) {
	if ifc.Address != "" {
		return applyStatic(link, ifc)
	}
	fmt.Printf("liken: negotiating DHCP on %s\n", ifc.Name)
	lease, err := acquireLease(ifc.Name)
	if err != nil {
		return nil, err
	}
	return applyLease(link, lease, ifc)
}

// anyAddressed reports whether any interface came up with an address.
// An interface that exists with no address is a report, not a path.
func anyAddressed(conns []*connection) bool {
	for _, conn := range conns {
		if conn.addr != nil {
			return true
		}
	}
	return false
}

// readdressRadio addresses the interface behind a radio that joined
// after the park released the boot. It replaces the addressless
// connection that the failed join left behind.
func readdressRadio(conns []*connection, interfaces []machine.InterfaceSpec, r *radio) []*connection {
	if r.state != machine.WirelessConnected {
		return conns
	}
	var spec machine.InterfaceSpec
	for _, ifc := range interfaces {
		if ifc.Name == r.ifname {
			spec = ifc
		}
	}
	link, err := linkByName(r.ifname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", r.ifname, err)
		return conns
	}
	conn, err := addressInterface(link, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", r.ifname, err)
		return conns
	}
	conn.radio = r
	for i, existing := range conns {
		if existing.ifname == r.ifname {
			conns[i] = conn
			return conns
		}
	}
	return append(conns, conn)
}

// interfaceIdentity is one link reduced to the two facts the boot
// needs about it: the name the kernel gave it, and whether the link
// carries a hardware address, which is how a real card is told from a
// virtual device. The functions below take these instead of netlink
// links, so the rules a manifest meets are pure functions that a test
// can drive on any machine.
type interfaceIdentity struct {
	name string
	mac  net.HardwareAddr
}

// presentInterfaces reduces the kernel's link list to the ports a
// manifest can configure. Loopback is dropped: it is not hardware,
// the boot has already raised it, and leaving it out keeps it out of
// the error messages that list what a machine has.
func presentInterfaces(links []netlink.Link) []interfaceIdentity {
	var present []interfaceIdentity
	for _, link := range links {
		attrs := link.Attrs()
		if attrs.Flags&net.FlagLoopback != 0 {
			continue
		}
		present = append(present, interfaceIdentity{name: attrs.Name, mac: attrs.HardwareAddr})
	}
	return present
}

// requirePort checks that the machine has the port a manifest names,
// and reports what the machine does have when it does not.
//
// netlink would refuse the name on its own, with the kernel's own
// words. Those words say that a link is missing and stop there, and
// the whole value of this check is the sentence after that: nobody
// can see this machine, which has no shell and no SSH daemon, so the
// console message is the entire diagnosis. A message that names the
// ports the machine really has turns a drive to the site into an edit
// of the manifest.
func requirePort(name string, present []interfaceIdentity) error {
	for _, port := range present {
		if port.name == name {
			return nil
		}
	}
	return fmt.Errorf("no interface is named %s; this machine has %s", name, describePorts(present))
}

// describePorts lists the ports a person could have named, for the
// errors that report a name no port answers to.
func describePorts(present []interfaceIdentity) string {
	if len(present) == 0 {
		return "no network interface at all"
	}
	described := make([]string, len(present))
	for i, port := range present {
		described[i] = port.name
	}
	return strings.Join(described, ", ")
}

// pickInterface finds the hardware to configure when the manifest
// names no interface. The rule is simple: the code picks the first
// port that has a MAC address. Such a link looks like a real network
// card. On the hardware that this default serves, one machine with
// one port, there is nothing to choose between. When there is more
// than one interface, the manifest must say which ports to configure.
func pickInterface(present []interfaceIdentity) (string, error) {
	for _, port := range present {
		if len(port.mac) > 0 {
			return port.name, nil
		}
	}
	return "", fmt.Errorf("no network interface found: none of the %d links outside loopback carries a hardware address", len(present))
}

// applyStatic sets a declared address in the kernel. This produces
// the same kernel state that a DHCP ACK produces, without the
// negotiation. The prefix length inside the CIDR tells the kernel
// which destinations are neighbors on this link, and which
// destinations are beyond the gateway.
func applyStatic(link netlink.Link, ifc machine.InterfaceSpec) (*connection, error) {
	addr, err := netlink.ParseAddr(ifc.Address)
	if err != nil {
		return nil, fmt.Errorf("address %q: %w", ifc.Address, err)
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return nil, fmt.Errorf("assigning %s: %w", ifc.Address, err)
	}

	conn := &connection{
		ifname: link.Attrs().Name,
		mac:    link.Attrs().HardwareAddr,
		addr:   addr.IPNet,
		method: machine.MethodStatic,
	}

	if ifc.Gateway != "" {
		gw := net.ParseIP(ifc.Gateway)
		if gw == nil {
			return nil, fmt.Errorf("gateway %q is not an IP address", ifc.Gateway)
		}
		conn.gateway = gw
		route := &netlink.Route{LinkIndex: link.Attrs().Index, Gw: gw}
		if err := netlink.RouteAdd(route); err != nil {
			return nil, fmt.Errorf("default route via %s: %w", gw, err)
		}
	}

	for _, raw := range ifc.Nameservers {
		ns := net.ParseIP(raw)
		if ns == nil {
			return nil, fmt.Errorf("nameserver %q is not an IP address", raw)
		}
		conn.nameservers = append(conn.nameservers, ns)
	}
	return conn, nil
}

// acquireLease runs the DHCP exchange. The library handles
// DISCOVER, OFFER, REQUEST, and ACK, and it handles their retries.
// The code bounds the whole exchange with a deadline. A boot that
// hangs forever on a dead network is worse than a boot that reports
// failure and continues to the console.
//
// The summary logger prints each packet of the exchange to the
// console. On a machine with no shell, the boot log is the only
// record of these packets.
//
// A note on entropy: the DHCP client draws a random transaction ID
// with getrandom(2). This call blocks until the kernel's random
// number generator has initialized, and the block is uninterruptible:
// the context deadline cannot stop it. On hardware with no entropy
// source, such as QEMU's default CPU model, which lacks RDRAND, the
// call blocks forever. The host must supply entropy: RDRAND,
// virtio-rng, or enough time for the kernel's jitter collector to
// gather entropy on its own.
func acquireLease(ifname string) (*nclient4.Lease, error) {
	client, err := nclient4.New(ifname, nclient4.WithSummaryLogger())
	if err != nil {
		return nil, fmt.Errorf("opening DHCP socket on %s: %w", ifname, err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lease, err := client.Request(ctx)
	if err != nil {
		return nil, fmt.Errorf("DHCP on %s: %w", ifname, err)
	}
	return lease, nil
}

// applyLease turns the DHCP ACK into kernel state. The code adds the
// address to the link and sets the router as the default route.
// Nameservers come from the lease, plus any nameservers that the
// manifest adds. They reach /etc/resolv.conf together with every
// other interface's nameservers, in bringUpNetwork.
func applyLease(link netlink.Link, lease *nclient4.Lease, ifc machine.InterfaceSpec) (*connection, error) {
	ack := lease.ACK

	addr := &net.IPNet{IP: ack.YourIPAddr, Mask: ack.SubnetMask()}
	if err := netlink.AddrAdd(link, &netlink.Addr{IPNet: addr}); err != nil {
		return nil, fmt.Errorf("assigning %s: %w", addr, err)
	}

	leaseTime := ack.IPAddressLeaseTime(0)
	conn := &connection{
		ifname:      link.Attrs().Name,
		mac:         link.Attrs().HardwareAddr,
		addr:        addr,
		method:      machine.MethodDHCP,
		nameservers: ack.DNS(),
		leaseTime:   leaseTime,
		// The clock is read once, here at the ACK; see the field's
		// comment for why the expiry must not be derived later.
		leaseExpires: time.Now().Add(leaseTime),
		server:       ack.ServerIdentifier(),
	}
	for _, raw := range ifc.Nameservers {
		if ns := net.ParseIP(raw); ns != nil {
			conn.nameservers = append(conn.nameservers, ns)
		}
	}

	if routers := ack.Router(); len(routers) > 0 {
		conn.gateway = routers[0]
		route := &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Gw:        conn.gateway,
		}
		if err := netlink.RouteAdd(route); err != nil {
			return nil, fmt.Errorf("default route via %s: %w", conn.gateway, err)
		}
	}

	return conn, nil
}

func (c *connection) report() {
	if c.radio != nil {
		fmt.Printf("liken: %s (%s) is on %s (%s)\n", c.ifname, c.mac, c.radio.ssid, c.radio.state)
		if c.radio.message != "" {
			fmt.Printf("liken:   %s\n", c.radio.message)
		}
	}
	if c.addr == nil {
		fmt.Printf("liken: %s (%s) has no address\n", c.ifname, c.mac)
		return
	}
	fmt.Printf("liken: %s (%s) is %s (%s)\n", c.ifname, c.mac, c.addr, strings.ToLower(string(c.method)))
	if c.method == machine.MethodDHCP {
		fmt.Printf("liken:   gateway %s, dhcp server %s, lease %s\n",
			c.gateway, c.server, c.leaseTime)
	} else if c.gateway != nil {
		fmt.Printf("liken:   gateway %s\n", c.gateway)
	}
	if len(c.nameservers) > 0 {
		fmt.Printf("liken:   nameservers %s\n", joinIPs(c.nameservers))
	}
}

func joinIPs(ips []net.IP) string {
	strs := make([]string, len(ips))
	for i, ip := range ips {
		strs[i] = ip.String()
	}
	return strings.Join(strs, ", ")
}
