package main

// Network bring-up, from nothing to routed, in userspace Go.
//
// Two different kernel interfaces cooperate here, and neither is a
// classic syscall. Interface configuration — links up, addresses,
// routes — happens over netlink, a socket-based protocol the kernel
// speaks (it's what `ip` does under the hood); the vishvananda/netlink
// library composes the messages. Getting an address is a different
// story entirely: DHCP is a network protocol, not a kernel feature, so
// liken must *speak* it — broadcast DISCOVER, receive OFFER, REQUEST,
// receive ACK. The insomniacslk/dhcp library (the same one Talos boots
// with) does this over a raw AF_PACKET socket, which is how a machine
// can send UDP before it has an IP address to send from.

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"
)

// connection is what bringUpNetwork learned: enough to print an honest
// report of how this machine is attached to the world.
type connection struct {
	ifname      string
	mac         net.HardwareAddr
	addr        *net.IPNet
	gateway     net.IP
	nameservers []net.IP
	leaseTime   time.Duration
	server      net.IP
}

func bringUpNetwork(spec NetworkSpec) (*connection, error) {
	// Loopback first: 127.0.0.1 is assumed by so much software that a
	// machine without it barely counts as booted. The kernel creates
	// the interface; we only have to raise it.
	if lo, err := netlink.LinkByName("lo"); err == nil {
		if err := netlink.LinkSetUp(lo); err != nil {
			return nil, fmt.Errorf("raising lo: %w", err)
		}
	}

	link, err := pickInterface(spec)
	if err != nil {
		return nil, err
	}
	fmt.Printf("liken: bringing up %s\n", link.Attrs().Name)
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, fmt.Errorf("raising %s: %w", link.Attrs().Name, err)
	}

	fmt.Printf("liken: negotiating DHCP on %s\n", link.Attrs().Name)
	lease, err := acquireLease(link.Attrs().Name)
	if err != nil {
		return nil, err
	}

	return applyLease(link, lease)
}

// pickInterface finds the hardware to configure. With no manifest
// preference, the heuristic is simple: the first link that isn't
// loopback and has a MAC address — i.e. looks like a real NIC. On the
// hardware liken targets (one machine, one port) there's nothing to
// disambiguate; when there is, the manifest pins it by name.
func pickInterface(spec NetworkSpec) (netlink.Link, error) {
	if spec.Interface != "" {
		link, err := netlink.LinkByName(spec.Interface)
		if err != nil {
			return nil, fmt.Errorf("manifest names interface %q: %w", spec.Interface, err)
		}
		return link, nil
	}

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

// acquireLease runs the DHCP conversation. The library handles the
// DISCOVER/OFFER/REQUEST/ACK exchange and its retries; we just bound
// the whole affair with a deadline, because a boot that hangs forever
// on a dead network is worse than one that reports failure and carries
// on to the console.
//
// The summary logger prints each packet of the exchange to the console
// — on a machine with no shell, the boot log is the packet capture.
//
// A note on entropy: the client draws a random transaction ID via
// getrandom(2), which blocks — uninterruptibly, immune to our context
// deadline — until the kernel's RNG has initialized. On hardware with
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

// applyLease turns the DHCP ACK into kernel state: the address goes on
// the link, the router becomes the default route, and the nameservers
// are written to /etc/resolv.conf — which is not magic, just a file
// that resolvers (including Go's) read by convention.
func applyLease(link netlink.Link, lease *nclient4.Lease) (*connection, error) {
	ack := lease.ACK

	addr := &net.IPNet{IP: ack.YourIPAddr, Mask: ack.SubnetMask()}
	if err := netlink.AddrAdd(link, &netlink.Addr{IPNet: addr}); err != nil {
		return nil, fmt.Errorf("assigning %s: %w", addr, err)
	}

	conn := &connection{
		ifname:      link.Attrs().Name,
		mac:         link.Attrs().HardwareAddr,
		addr:        addr,
		nameservers: ack.DNS(),
		leaseTime:   ack.IPAddressLeaseTime(0),
		server:      ack.ServerIdentifier(),
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

	if len(conn.nameservers) > 0 {
		var b strings.Builder
		for _, ns := range conn.nameservers {
			fmt.Fprintf(&b, "nameserver %s\n", ns)
		}
		if err := writeResolvConf(b.String()); err != nil {
			return nil, err
		}
	}

	return conn, nil
}

func writeResolvConf(content string) error {
	return os.WriteFile("/etc/resolv.conf", []byte(content), 0o644)
}

func (c *connection) report() {
	fmt.Printf("liken: %s (%s) is %s\n", c.ifname, c.mac, c.addr)
	fmt.Printf("liken:   gateway %s, dhcp server %s, lease %s\n",
		c.gateway, c.server, c.leaseTime)
	fmt.Printf("liken:   nameservers %s\n", joinIPs(c.nameservers))
}

func joinIPs(ips []net.IP) string {
	strs := make([]string, len(ips))
	for i, ip := range ips {
		strs[i] = ip.String()
	}
	return strings.Join(strs, ", ")
}
