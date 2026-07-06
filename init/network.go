package main

// Network bring-up, from nothing to routed, in userspace Go.
//
// Two different kernel interfaces cooperate here, and neither is a
// classic syscall. Interface configuration (links up, addresses,
// routes) happens over netlink, a socket-based protocol the kernel
// speaks (it's what `ip` does under the hood); the vishvananda/netlink
// library composes the messages. Getting an address by DHCP is a
// different story entirely: DHCP is a network protocol, not a kernel
// feature, so liken must *speak* it: broadcast DISCOVER, receive
// OFFER, REQUEST, receive ACK. The insomniacslk/dhcp library (the same
// one Talos boots with) does this over a raw AF_PACKET socket, which
// is how a machine can send UDP before it has an IP address to send
// from.
//
// A static address is the degenerate case: no protocol at all, just
// the netlink calls, applying an address someone already decided. It
// exists because clustering demands it: a machine's peers are told
// where to find it before it boots, so its address is a promise made
// in the manifest, not an outcome negotiated on the wire. (The lab
// forces the same conclusion from below: the segment joining the QEMU
// guests is a dumb wire with no DHCP server on it.)

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"

	"github.com/chrisguidry/liken/machine"
)

// connection is what bringing up one interface learned: enough to
// print a report of how this machine is attached to the network, and
// to publish the same facts to the Machine's status.
type connection struct {
	ifname      string
	mac         net.HardwareAddr
	addr        *net.IPNet
	method      string // how the address arrived: DHCP or Static
	gateway     net.IP
	nameservers []net.IP
	leaseTime   time.Duration
	server      net.IP
}

// bringUpNetwork configures every interface the spec names, or the
// zero-configuration default (DHCP on the first interface that looks
// like real hardware) when it names none. One interface failing
// doesn't stop the others: a machine with its uplink down but its
// cluster segment up is degraded, not absent, and the console report
// says which is which. An error comes back only when nothing came up
// at all.
func bringUpNetwork(spec machine.NetworkSpec) ([]*connection, error) {
	// Loopback first: 127.0.0.1 is assumed by nearly all networked
	// software. The kernel creates the interface; we only have to
	// raise it.
	if lo, err := netlink.LinkByName("lo"); err == nil {
		if err := netlink.LinkSetUp(lo); err != nil {
			return nil, fmt.Errorf("raising lo: %w", err)
		}
	}

	interfaces := spec.Interfaces
	if len(interfaces) == 0 {
		link, err := pickInterface()
		if err != nil {
			return nil, err
		}
		interfaces = []machine.InterfaceSpec{{Name: link.Attrs().Name}}
	}

	var conns []*connection
	for _, ifc := range interfaces {
		conn, err := bringUpInterface(ifc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", ifc.Name, err)
			continue
		}
		conns = append(conns, conn)
	}
	if len(conns) == 0 {
		return nil, fmt.Errorf("no interface came up")
	}

	// One resolv.conf for the whole machine, gathered across every
	// interface: DHCP leases and manifest declarations both land here,
	// in interface order. The file is an ordinary one that resolvers
	// (including Go's) read by convention.
	var b strings.Builder
	for _, conn := range conns {
		for _, ns := range conn.nameservers {
			fmt.Fprintf(&b, "nameserver %s\n", ns)
		}
	}
	if b.Len() > 0 {
		if err := writeResolvConf(b.String()); err != nil {
			return conns, err
		}
	}
	return conns, nil
}

// bringUpInterface raises one link and gives it an address by
// whichever method its spec chose.
func bringUpInterface(ifc machine.InterfaceSpec) (*connection, error) {
	link, err := netlink.LinkByName(ifc.Name)
	if err != nil {
		return nil, fmt.Errorf("manifest names interface %q: %w", ifc.Name, err)
	}
	fmt.Printf("liken: bringing up %s\n", ifc.Name)
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, fmt.Errorf("raising %s: %w", ifc.Name, err)
	}

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

// pickInterface finds the hardware to configure when the manifest
// expresses no preference. The heuristic is simple: the first link
// that isn't loopback and has a MAC address, i.e. looks like a real
// NIC. On the hardware this default serves (one machine, one port)
// there's nothing to disambiguate; when there is, the manifest names
// interfaces explicitly.
func pickInterface() (netlink.Link, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}
	for _, link := range links {
		attrs := link.Attrs()
		if attrs.Flags&net.FlagLoopback == 0 && len(attrs.HardwareAddr) > 0 {
			return link, nil
		}
	}
	return nil, fmt.Errorf("no network interface found among %d links", len(links))
}

// applyStatic actuates a declared address: the same kernel state a
// DHCP ACK produces, minus the negotiation. The prefix length inside
// the CIDR is what tells the kernel which destinations are neighbors
// on this link versus somewhere beyond the gateway.
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
// DISCOVER/OFFER/REQUEST/ACK and its retries; we bound the whole
// exchange with a deadline, because a boot that hangs forever on a
// dead network is worse than one that reports failure and carries on
// to the console.
//
// The summary logger prints each packet of the exchange to the
// console; on a machine with no shell, the boot log is the only
// packet capture available.
//
// A note on entropy: the client draws a random transaction ID via
// getrandom(2), which blocks (uninterruptibly, immune to our context
// deadline) until the kernel's RNG has initialized. On hardware with
// no entropy source (like QEMU's default CPU model, which lacks
// RDRAND) that is forever. The host must provide entropy: RDRAND,
// virtio-rng, or patience with the kernel's jitter collector.
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

// applyLease turns the DHCP ACK into kernel state: the address goes
// on the link and the router becomes the default route. Nameservers
// come from the lease, plus any the manifest adds; they reach
// /etc/resolv.conf with every other interface's in bringUpNetwork.
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

func writeResolvConf(content string) error {
	return os.WriteFile("/etc/resolv.conf", []byte(content), 0o644)
}

func (c *connection) report() {
	fmt.Printf("liken: %s (%s) is %s (%s)\n", c.ifname, c.mac, c.addr, strings.ToLower(c.method))
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
