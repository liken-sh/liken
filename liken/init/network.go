package main

// At boot, init raises each interface that the manifest lists and gives
// it an address from DHCP or from the manifest.
//
// Links, addresses, and routes are configured over netlink, the kernel
// interface that the `ip` command uses.
//
// The kernel has no DHCP client, so init runs one itself with
// insomniacslk/dhcp. The library sends over an AF_PACKET socket, which
// can send UDP before the interface has an IP address.
//
// A machine needs a static address when its cluster peers must know the
// address before it boots, or when its segment has no DHCP server, as
// on the lab fleet's QEMU network.

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

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// connection is what init learned while it brought up one interface.
// The boot prints it to the console and publishes it to the Machine's
// status.
type connection struct {
	ifname string
	mac    net.HardwareAddr

	// addr is nil for a wireless interface whose radio did not join or
	// did not get an address. The status still reports the interface,
	// so every reader checks addr before it uses it.
	addr        *net.IPNet
	method      machine.AddressMethod // DHCP or Static
	gateway     net.IP
	nameservers []net.IP
	leaseTime   time.Duration
	server      net.IP

	// leaseExpires is set once, when the DHCP ACK arrives. If it were
	// computed each time the facts are written, the expiry would move
	// forward on every write although the lease did not renew.
	leaseExpires time.Time

	// radio is the 802.11 session of an interface whose spec has a
	// wireless entry. It is nil for a wired interface.
	radio *radio
}

// bringUpNetwork configures every interface in the spec. When the spec
// lists none, it runs DHCP on the first interface with a hardware
// address. A failure on one interface does not stop the others, so a
// machine with its uplink down and its cluster segment up still boots,
// and the console shows which interface failed. A failed interface is
// not an error. bringUpNetwork fails when no interface has an address,
// and when it cannot validate the spec, list the links, choose a
// default interface, or write resolv.conf.
//
// radios.go runs the interfaces in two passes: the wired interfaces
// first, then the radios. The radios run in the background when
// backgroundable decides from clusterDoc that the wired interfaces are
// enough. The returned *radioPass is that background pass, or nil. The
// caller hands it to watchRadios, which writes each radio's result to
// the facts after the boot has published its own.
func bringUpNetwork(spec machine.NetworkSpec, clusterDoc *cluster.Cluster) ([]*connection, *radioPass, error) {
	// The kernel creates `lo` down. Much software expects 127.0.0.1 to
	// work, so init raises it before anything else.
	if lo, err := linkByName("lo"); err == nil {
		if err := linkSetUp(lo); err != nil {
			return nil, nil, fmt.Errorf("raising lo: %w", err)
		}
	}

	// Validate before changing any link. The operator refuses to stage
	// an invalid spec, but init also boots from manifests written by
	// hand on an install stick, which the operator never checked.
	if err := spec.Validate(); err != nil {
		return nil, nil, err
	}

	// Read the link list once, so every error message lists the same
	// ports.
	links, err := listLinks()
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

// resolvConfPath is a variable so a test can write to its own file.
var resolvConfPath = "/etc/resolv.conf"

// writeResolvConf writes one /etc/resolv.conf for the whole machine
// from the nameservers of every interface. bringUpNetwork writes it
// during the boot, and radiowatch.go writes it again when a radio joins
// later, so a late radio's nameservers go through the same order and
// limit as the others. It writes nothing when no interface has a
// nameserver.
func writeResolvConf(conns []*connection) error {
	content := resolvConf(conns)
	if content == "" {
		return nil
	}
	return writeResolvConfAtomic(resolvConfPath, []byte(content))
}

// writeResolvConfAtomic writes a temporary file in the same directory
// and renames it over the target. A radio's rewrite can happen while
// the kubelet creates a pod sandbox, which copies resolv.conf as it is
// at that moment. A plain WriteFile truncates the file first, so a pod
// created in that window would keep an empty resolv.conf. A rename
// replaces the file in one step, so a reader sees the old content or
// the new.
func writeResolvConfAtomic(path string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".liken-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file beside %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("writing the new content of %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing the new content of %s: %w", path, err)
	}
	// CreateTemp creates the file with mode 0600, but every process on
	// the machine must be able to read resolv.conf.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("setting the mode of the new content of %s: %w", path, err)
	}
	return os.Rename(tmp.Name(), path)
}

// resolvConf renders resolv.conf from the nameservers of the
// connections, in interface order, with duplicates removed, and keeps
// the first three.
//
// glibc reads at most three nameservers (MAXNS=3), and musl has the
// same limit. The kubelet also cuts each pod's list to three, and it
// logs a warning on every sync when the node's file has more. Some DHCP
// servers offer many more. Writing them all would not change which
// resolvers answer, and it would repeat that warning.
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

// The netlink calls of the bring-up are variables so a test can run it
// without a kernel, including a link raise that never returns.
var (
	linkByName = netlink.LinkByName
	linkSetUp  = netlink.LinkSetUp
	addrAdd    = netlink.AddrAdd
	routeAdd   = netlink.RouteAdd
)

// requestLease is a variable so a test can supply a lease without a
// network or a DHCP server.
var requestLease = acquireLease

// bringUpInterface raises one wired link and gives it an address. It
// puts no deadline on the raise. bringUpRadio in radios.go raises a
// radio under one.
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

// addressInterface gives a raised link the static address of the spec,
// or a DHCP lease when the spec has none. A radio must be associated
// first, because it sends no frames before that.
func addressInterface(link netlink.Link, ifc machine.InterfaceSpec) (*connection, error) {
	if ifc.Address != "" {
		return applyStatic(link, ifc)
	}
	fmt.Printf("liken: negotiating DHCP on %s\n", ifc.Name)
	lease, err := requestLease(ifc.Name)
	if err != nil {
		return nil, err
	}
	return applyLease(link, lease, ifc)
}

// anyAddressed reports whether any interface has an address.
func anyAddressed(conns []*connection) bool {
	for _, conn := range conns {
		if conn.addr != nil {
			return true
		}
	}
	return false
}

// addressByName gives an address to a link that is already up. Its
// callers use it for a radio that joined after bringUpRadio raised it.
func addressByName(ifc machine.InterfaceSpec) (*connection, error) {
	link, err := linkByName(ifc.Name)
	if err != nil {
		return nil, err
	}
	return addressInterface(link, ifc)
}

// readdressRadio addresses a radio whose first join failed and that
// joined while waitForRadio waited. It replaces the radio's connection
// with no address in conns, and leaves conns unchanged when the radio
// is still not connected or addressing fails.
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
	conn, err := addressByName(spec)
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

// interfaceIdentity is the name and hardware address of a netlink
// link. The port checks take it in place of netlink.Link, so tests can
// run them on any machine.
type interfaceIdentity struct {
	name string
	mac  net.HardwareAddr
}

// presentInterfaces returns the links that a manifest can configure.
// It leaves out loopback, because the boot raises it on its own and the
// error messages that list a machine's ports should not show it.
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

// requirePort returns an error that lists the machine's ports when it
// has none named name. netlink refuses an unknown name too, but only
// says that the link does not exist. The machine has no shell or SSH,
// so the console message is all a person has to fix the manifest with.
func requirePort(name string, present []interfaceIdentity) error {
	for _, port := range present {
		if port.name == name {
			return nil
		}
	}
	return fmt.Errorf("no interface is named %s, and this machine has %s", name, describePorts(present))
}

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

// pickInterface chooses the interface to configure when the manifest
// lists none: the first one with a hardware address, because a link
// without one is a virtual device. This default suits a machine with
// one port. A machine with several needs a manifest that lists them.
func pickInterface(present []interfaceIdentity) (string, error) {
	for _, port := range present {
		if len(port.mac) > 0 {
			return port.name, nil
		}
	}
	return "", fmt.Errorf("no network interface found: none of the %d links other than loopback has a hardware address", len(present))
}

// applyStatic adds the manifest's address and default route to the
// kernel and records its nameservers in the connection, as applyLease
// does from a DHCP ACK. The prefix length of the address tells the
// kernel which destinations are on this link and which go through the
// gateway.
func applyStatic(link netlink.Link, ifc machine.InterfaceSpec) (*connection, error) {
	addr, err := netlink.ParseAddr(ifc.Address)
	if err != nil {
		return nil, fmt.Errorf("parsing address %q: %w", ifc.Address, err)
	}
	if err := addrAdd(link, addr); err != nil {
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
		if err := routeAdd(route); err != nil {
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

// acquireLease runs the DHCP exchange under a 30-second deadline, so a
// dead network fails the interface instead of stopping the boot.
// WithSummaryLogger prints each packet to the console, which is the
// only record of the exchange on a machine with no shell.
//
// The client picks its transaction ID with getrandom(2), which blocks
// until the kernel's random number generator is initialized, and the
// deadline cannot interrupt it. QEMU's default CPU model has no RDRAND,
// so a guest needs RDRAND or virtio-rng from the host, or it waits for
// the kernel's jitter entropy collector.
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
		return nil, fmt.Errorf("requesting a DHCP lease on %s: %w", ifname, err)
	}
	return lease, nil
}

// applyLease adds the leased address to the link and the lease's first
// router as the default route. The connection's nameservers are those
// of the lease followed by those of the manifest.
func applyLease(link netlink.Link, lease *nclient4.Lease, ifc machine.InterfaceSpec) (*connection, error) {
	ack := lease.ACK

	addr := &net.IPNet{IP: ack.YourIPAddr, Mask: ack.SubnetMask()}
	if err := addrAdd(link, &netlink.Addr{IPNet: addr}); err != nil {
		return nil, fmt.Errorf("assigning %s: %w", addr, err)
	}

	leaseTime := ack.IPAddressLeaseTime(0)
	conn := &connection{
		ifname:       link.Attrs().Name,
		mac:          link.Attrs().HardwareAddr,
		addr:         addr,
		method:       machine.MethodDHCP,
		nameservers:  ack.DNS(),
		leaseTime:    leaseTime,
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
		if err := routeAdd(route); err != nil {
			return nil, fmt.Errorf("default route via %s: %w", conn.gateway, err)
		}
	}

	return conn, nil
}

// report prints the connection to the console.
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
