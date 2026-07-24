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
// peers must know its address before it boots, so the manifest must
// declare the address instead of negotiating it on the wire. The lab
// also needs static addressing, for a different reason: the network
// segment that joins the QEMU guests has no DHCP server on it.

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/machine"
)

// connection holds the facts that the code learns while it brings up
// one interface. These facts are enough to print a report of how the
// machine connects to the network, and to publish the same facts to
// the Machine's status.
type connection struct {
	ifname      string
	mac         net.HardwareAddr
	addr        *net.IPNet
	method      machine.AddressMethod // how the code obtained the address: DHCP or Static
	gateway     net.IP
	nameservers []net.IP
	leaseTime   time.Duration
	server      net.IP
}

// bringUpNetwork configures every interface that the spec names.
// When the spec names no interface, it uses the zero-configuration
// default: DHCP on the first interface that looks like real
// hardware. If one interface fails, the function still configures
// the others. A machine with its uplink down but its cluster segment
// up is degraded, not absent, and the console report shows which
// interface is which. The function returns an error only when no
// interface comes up.
func bringUpNetwork(spec machine.NetworkSpec) ([]*connection, error) {
	// The code brings up loopback first. Nearly all networked
	// software assumes that 127.0.0.1 exists. The kernel creates the
	// loopback interface; the code only needs to raise it.
	if lo, err := netlink.LinkByName("lo"); err == nil {
		if err := netlink.LinkSetUp(lo); err != nil {
			return nil, fmt.Errorf("raising lo: %w", err)
		}
	}

	// A spec that cannot be right is refused before any link changes.
	// The same check runs in the cluster, where the operator refuses
	// to stage such a spec, but the boot cannot rely on that: init
	// also reads manifests that were written by hand and carried in
	// on a stick, which no API server ever saw.
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	// The kernel's link list is read once and used for every
	// resolution below. Reading it once also means that every error
	// message can list the same set of ports, which is the list a
	// person needs to correct the manifest.
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}
	present := presentInterfaces(links)

	interfaces := spec.Interfaces
	if len(interfaces) == 0 {
		name, err := pickInterface(present)
		if err != nil {
			return nil, err
		}
		interfaces = []machine.InterfaceSpec{{Name: name}}
	}

	var conns []*connection
	for _, ifc := range interfaces {
		conn, err := bringUpInterface(ifc, present)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", ifc.Identity(), err)
			continue
		}
		conns = append(conns, conn)
	}
	if len(conns) == 0 {
		return nil, fmt.Errorf("no interface came up")
	}

	// The code builds one resolv.conf file for the whole machine,
	// gathered from every interface. The resolvConf function below
	// explains which nameservers it keeps. The file is an ordinary
	// file. Resolvers, including Go's own resolver, read it by
	// convention.
	if content := resolvConf(conns); content != "" {
		if err := os.WriteFile("/etc/resolv.conf", []byte(content), 0o644); err != nil {
			return conns, err
		}
	}
	return conns, nil
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

// bringUpInterface raises one link and gives it an address, using
// the method that the interface spec chose.
func bringUpInterface(ifc machine.InterfaceSpec, present []interfaceIdentity) (*connection, error) {
	ifname, err := resolveInterface(ifc, present)
	if err != nil {
		return nil, err
	}
	// The rest of the boot log, and the status the operator
	// publishes, speak in kernel names. A manifest that asked for a
	// MAC address gets the translation printed once, so a reader can
	// follow the rest.
	if ifc.MAC != "" {
		fmt.Printf("liken: MAC %s is %s on this machine\n", ifc.MAC, ifname)
	}
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		return nil, fmt.Errorf("opening interface %q: %w", ifname, err)
	}
	fmt.Printf("liken: bringing up %s\n", ifname)
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, fmt.Errorf("raising %s: %w", ifname, err)
	}

	if ifc.Address != "" {
		return applyStatic(link, ifc)
	}

	fmt.Printf("liken: negotiating DHCP on %s\n", ifname)
	lease, err := acquireLease(ifname)
	if err != nil {
		return nil, err
	}
	return applyLease(link, lease, ifc)
}

// interfaceIdentity is one link reduced to the two facts that decide
// which link a spec means: the name the kernel gave it, and the
// address burned into the card. Resolution takes these instead of
// netlink links, so the rule that chooses a port is a pure function
// that a test can drive on any machine.
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

// resolveInterface decides which port one interface spec means, and
// returns the kernel name of that port.
//
// A spec identifies its port by name, by MAC address, or by both. A
// name is what the kernel called the port on this boot. A MAC address
// is what the card carries, and it is compared through net.ParseMAC
// on both sides, so a manifest may spell an address in any of the
// forms a person copies: colons, hyphens, or the dotted quads that
// switch consoles print.
//
// When a spec carries both, the two must agree. Guessing which one
// was meant would put the machine's whole network on a coin toss, and
// the person who would have to correct it cannot reach the machine
// except by walking to it.
func resolveInterface(ifc machine.InterfaceSpec, present []interfaceIdentity) (string, error) {
	if ifc.MAC == "" {
		for _, port := range present {
			if port.name == ifc.Name {
				return port.name, nil
			}
		}
		return "", fmt.Errorf("no interface is named %s; this machine has %s", ifc.Name, describePorts(present))
	}

	want, err := net.ParseMAC(ifc.MAC)
	if err != nil {
		return "", fmt.Errorf("mac %q is not a MAC address", ifc.MAC)
	}
	for _, port := range present {
		if !bytes.Equal(port.mac, want) {
			continue
		}
		if ifc.Name != "" && ifc.Name != port.name {
			return "", fmt.Errorf("this interface declares both name %s and MAC %s, and they are different ports: that MAC is %s; remove whichever one is wrong",
				ifc.Name, ifc.MAC, port.name)
		}
		return port.name, nil
	}
	return "", fmt.Errorf("no interface has MAC %s; this machine has %s", ifc.MAC, describePorts(present))
}

// describePorts lists every port with its name and its address, for
// the errors that report a spec no port satisfies. The whole value of
// this listing is that a person cannot see the machine: nothing here
// has a shell or an SSH daemon, so the console message is all they
// get. A message that names the addresses the machine really carries
// turns a drive to the site into an edit of the manifest.
func describePorts(present []interfaceIdentity) string {
	if len(present) == 0 {
		return "no network interface at all"
	}
	described := make([]string, len(present))
	for i, port := range present {
		if len(port.mac) == 0 {
			described[i] = port.name
			continue
		}
		described[i] = fmt.Sprintf("%s %s", port.name, port.mac)
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

	conn := &connection{
		ifname:      link.Attrs().Name,
		mac:         link.Attrs().HardwareAddr,
		addr:        addr,
		method:      machine.MethodDHCP,
		nameservers: ack.DNS(),
		leaseTime:   ack.IPAddressLeaseTime(0),
		server:      ack.ServerIdentifier(),
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
