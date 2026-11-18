package main

// Tests for bringing up a wired port: which port the boot picks, which
// address and route it asks the kernel to add, and what it writes to
// resolv.conf. They run against a fake netlink kernel that records the
// calls it receives, so they need no root and no network.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/liken/machine"
)

// netKernel is a fake kernel for the netlink calls. It lists its links
// and records each link that the bring-up raises and each address and
// route that it adds, in the format that `ip` prints. A field that ends
// in Err makes the matching call return that error.
type netKernel struct {
	links  []netlink.Link
	raised []string
	addrs  []string
	routes []string

	listErr  error
	raiseErr error
	addrErr  error
	routeErr error
}

// netCard is a link with a hardware address, like a physical network
// card.
func netCard(name string, index int) netlink.Link {
	return &netlink.Device{LinkAttrs: netlink.LinkAttrs{
		Name: name, Index: index,
		HardwareAddr: net.HardwareAddr{0x52, 0x54, 0x00, 0x4c, 0x4b, byte(index)},
	}}
}

// netLoopback is the loopback link, which has no hardware address.
func netLoopback() netlink.Link {
	return &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "lo", Index: 1, Flags: net.FlagLoopback}}
}

// useNetKernel replaces each netlink function that the bring-up calls
// with the fake kernel's, and restores them when the test ends.
func useNetKernel(t *testing.T, links ...netlink.Link) *netKernel {
	t.Helper()
	k := &netKernel{links: links}
	origList, origByName, origSetUp := listLinks, linkByName, linkSetUp
	origAddr, origRoute := addrAdd, routeAdd
	t.Cleanup(func() {
		listLinks, linkByName, linkSetUp = origList, origByName, origSetUp
		addrAdd, routeAdd = origAddr, origRoute
	})
	listLinks = func() ([]netlink.Link, error) { return k.links, k.listErr }
	linkByName = func(name string) (netlink.Link, error) {
		for _, link := range k.links {
			if link.Attrs().Name == name {
				return link, nil
			}
		}
		return nil, netlink.LinkNotFoundError{}
	}
	linkSetUp = func(link netlink.Link) error {
		if k.raiseErr != nil {
			return k.raiseErr
		}
		k.raised = append(k.raised, link.Attrs().Name)
		return nil
	}
	addrAdd = func(link netlink.Link, addr *netlink.Addr) error {
		if k.addrErr != nil {
			return k.addrErr
		}
		k.addrs = append(k.addrs, fmt.Sprintf("%s %s", link.Attrs().Name, addr.IPNet))
		return nil
	}
	routeAdd = func(route *netlink.Route) error {
		if k.routeErr != nil {
			return k.routeErr
		}
		k.routes = append(k.routes, fmt.Sprintf("%s via %s", k.nameOf(route.LinkIndex), route.Gw))
		return nil
	}
	return k
}

func (k *netKernel) nameOf(index int) string {
	for _, link := range k.links {
		if link.Attrs().Index == index {
			return link.Attrs().Name
		}
	}
	return fmt.Sprintf("index %d", index)
}

// staticPort is a port declared with a static address, a gateway, and
// a nameserver.
func staticPort(name string) machine.InterfaceSpec {
	return machine.InterfaceSpec{
		Name: name, Address: "10.10.0.5/24", Gateway: "10.10.0.254",
		Nameservers: []string{"10.10.0.1"},
	}
}

// A static port must add exactly the address and default route that the
// manifest declares, and its nameserver must reach resolv.conf. The
// bring-up also raises loopback, because networked software expects
// 127.0.0.1 to exist.
func TestBringUpNetworkAppliesADeclaredStaticPort(t *testing.T) {
	resolv := useResolvConf(t)
	k := useNetKernel(t, netLoopback(), netCard("eth0", 2), netCard("eth1", 3))

	spec := machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{staticPort("eth1")}}
	conns, pass, err := bringUpNetwork(spec, nil)
	if err != nil {
		t.Fatalf("bringing up the network: %v", err)
	}

	if got := strings.Join(k.raised, ","); got != "lo,eth1" {
		t.Errorf("raised %q, want lo and the declared port only", got)
	}
	if got := strings.Join(k.addrs, ";"); got != "eth1 10.10.0.5/24" {
		t.Errorf("addresses are %q, want %q", got, "eth1 10.10.0.5/24")
	}
	if got := strings.Join(k.routes, ";"); got != "eth1 via 10.10.0.254" {
		t.Errorf("routes are %q, want %q", got, "eth1 via 10.10.0.254")
	}
	if len(conns) != 1 || conns[0].method != machine.MethodStatic {
		t.Errorf("connections are %v, want one static connection", conns)
	}
	if pass != nil {
		t.Error("bringUpNetwork returned a radio pass for a spec with no radios, want nil")
	}
	if got := readFile(t, resolv); got != "nameserver 10.10.0.1\n" {
		t.Errorf("resolv.conf is %q, want %q", got, "nameserver 10.10.0.1\n")
	}
}

// labLease builds a DHCP ACK like a router's: an address on a /24, the
// router as gateway, DNS server, and DHCP server, a public resolver
// as a second DNS server, and a one-hour lease.
func labLease(t *testing.T) *nclient4.Lease {
	t.Helper()
	router := net.ParseIP("192.168.1.1")
	ack, err := dhcpv4.New(
		dhcpv4.WithMessageType(dhcpv4.MessageTypeAck),
		dhcpv4.WithYourIP(net.ParseIP("192.168.1.20")),
		dhcpv4.WithNetmask(net.CIDRMask(24, 32)),
		dhcpv4.WithOption(dhcpv4.OptRouter(router)),
		dhcpv4.WithOption(dhcpv4.OptDNS(router, net.ParseIP("9.9.9.9"))),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(router)),
		dhcpv4.WithLeaseTime(3600),
	)
	if err != nil {
		t.Fatalf("building the DHCP ACK: %v", err)
	}
	return &nclient4.Lease{ACK: ack}
}

// useFakeDHCP replaces the DHCP exchange with a fake that returns the
// given lease or error for every request, and records which interfaces
// requested a lease.
func useFakeDHCP(t *testing.T, lease *nclient4.Lease, err error) *[]string {
	t.Helper()
	var asked []string
	orig := requestLease
	requestLease = func(ifname string) (*nclient4.Lease, error) {
		asked = append(asked, ifname)
		return lease, err
	}
	t.Cleanup(func() { requestLease = orig })
	return &asked
}

// A manifest that declares no interface gets the default: DHCP on the
// first link that has a hardware address. A virtual device listed
// before the card must not get the lease.
func TestBringUpNetworkDefaultsToDHCPOnTheFirstCard(t *testing.T) {
	resolv := useResolvConf(t)
	dummy := &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "dummy0", Index: 2}}
	k := useNetKernel(t, netLoopback(), dummy, netCard("eth0", 3))
	asked := useFakeDHCP(t, labLease(t), nil)

	conns, _, err := bringUpNetwork(machine.NetworkSpec{}, nil)
	if err != nil {
		t.Fatalf("bringing up the network: %v", err)
	}

	if got := strings.Join(*asked, ","); got != "eth0" {
		t.Errorf("DHCP ran on %q, want eth0", got)
	}
	if got := strings.Join(k.addrs, ";"); got != "eth0 192.168.1.20/24" {
		t.Errorf("addresses are %q, want %q", got, "eth0 192.168.1.20/24")
	}
	if got := strings.Join(k.routes, ";"); got != "eth0 via 192.168.1.1" {
		t.Errorf("routes are %q, want %q", got, "eth0 via 192.168.1.1")
	}
	if len(conns) != 1 || conns[0].method != machine.MethodDHCP {
		t.Errorf("connections are %v, want one DHCP connection", conns)
	}
	if got := readFile(t, resolv); got != "nameserver 192.168.1.1\nnameserver 9.9.9.9\n" {
		t.Errorf("resolv.conf is %q, want the lease's nameservers", got)
	}
}

// Every way the bring-up can end with no usable interface must return
// an error that gives the cause, and must not write resolv.conf. The
// boot prints that error on the console and goes on with no network.
func TestBringUpNetworkFailsWithNoUsableInterface(t *testing.T) {
	refused := errors.New("operation not permitted")
	for _, tc := range []struct {
		name    string
		spec    machine.NetworkSpec
		links   []netlink.Link
		refuse  func(*netKernel)
		wantErr string
	}{
		{
			name:    "the kernel refuses to raise loopback",
			links:   []netlink.Link{netLoopback(), netCard("eth0", 2)},
			refuse:  func(k *netKernel) { k.raiseErr = refused },
			wantErr: "raising lo: operation not permitted",
		},
		{
			name: "the spec declares one port twice",
			spec: machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{
				staticPort("eth0"), staticPort("eth0"),
			}},
			links:   []netlink.Link{netLoopback(), netCard("eth0", 2)},
			refuse:  func(*netKernel) {},
			wantErr: "both declare eth0",
		},
		{
			name:    "the kernel cannot list its links",
			links:   []netlink.Link{netLoopback()},
			refuse:  func(k *netKernel) { k.listErr = refused },
			wantErr: "listing interfaces: operation not permitted",
		},
		{
			name:    "no link is a card",
			links:   []netlink.Link{netLoopback()},
			refuse:  func(*netKernel) {},
			wantErr: "no network interface found",
		},
		{
			name:    "the declared port is missing",
			spec:    machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{staticPort("enp3s0")}},
			links:   []netlink.Link{netLoopback(), netCard("eth0", 2)},
			refuse:  func(*netKernel) {},
			wantErr: "no interface came up",
		},
		{
			name:    "the kernel refuses the address",
			spec:    machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{staticPort("eth0")}},
			links:   []netlink.Link{netLoopback(), netCard("eth0", 2)},
			refuse:  func(k *netKernel) { k.addrErr = refused },
			wantErr: "no interface came up",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolv := useResolvConf(t)
			k := useNetKernel(t, tc.links...)
			tc.refuse(k)

			_, _, err := bringUpNetwork(tc.spec, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the error is %v, want one that contains %q", err, tc.wantErr)
			}
			if len(k.addrs) != 0 {
				t.Errorf("addresses are %v, want none after the failure", k.addrs)
			}
			if _, err := os.Stat(resolv); !os.IsNotExist(err) {
				t.Errorf("stat of resolv.conf is %v, want it not to exist", err)
			}
		})
	}
}

// When resolv.conf cannot be written, the bring-up must fail with the
// path in the error and still return the connections, so the console
// report lists the ports that came up.
func TestBringUpNetworkReportsAResolvConfItCannotWrite(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "etc", "resolv.conf")
	orig := resolvConfPath
	resolvConfPath = missing
	t.Cleanup(func() { resolvConfPath = orig })
	useNetKernel(t, netLoopback(), netCard("eth0", 2))

	spec := machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{staticPort("eth0")}}
	conns, _, err := bringUpNetwork(spec, nil)

	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("the error is %v, want one that contains %s", err, missing)
	}
	if !anyAddressed(conns) {
		t.Error("the connections list no addressed port, want the port that came up")
	}
}

// A port that cannot come up must fail with an error that a person can
// act on: either the ports that the machine has, or the kernel's error.
func TestBringUpInterfaceSaysWhyAPortStayedDown(t *testing.T) {
	refused := errors.New("operation not permitted")
	for _, tc := range []struct {
		name    string
		port    string
		present []interfaceIdentity
		refuse  error
		wantErr string
	}{
		{"no such port", "enp3s0", twoPorts(), nil, "this machine has eth0, eth1"},
		{"the kernel has no link by that name", "eth2", []interfaceIdentity{{name: "eth2"}}, nil, `opening interface "eth2"`},
		{"the kernel refuses the raise", "eth0", []interfaceIdentity{{name: "eth0"}}, refused, "raising eth0: operation not permitted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := useNetKernel(t, netCard("eth0", 2))
			k.raiseErr = tc.refuse
			_, err := bringUpInterface(machine.InterfaceSpec{Name: tc.port}, tc.present)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the error is %v, want one that contains %q", err, tc.wantErr)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// A port with an address and no gateway reaches only its own subnet. A
// default route through it would take the machine's traffic away from
// the port that has a gateway.
func TestApplyStaticWithNoGatewayAddsNoRoute(t *testing.T) {
	k := useNetKernel(t, netCard("eth1", 3))
	conn, err := applyStatic(k.links[0], machine.InterfaceSpec{Name: "eth1", Address: "10.10.0.5/24"})
	if err != nil {
		t.Fatalf("applying the static address: %v", err)
	}
	if len(k.routes) != 0 || conn.gateway != nil {
		t.Errorf("routes are %v and the gateway is %v, want neither", k.routes, conn.gateway)
	}
	if conn.addr.String() != "10.10.0.5/24" {
		t.Errorf("address is %s, want 10.10.0.5/24", conn.addr)
	}
}

// Each error must contain the manifest's values and the kernel's error
// text unchanged, because the console is the only place that a person
// can read why the port has no address.
func TestApplyStaticFailures(t *testing.T) {
	exists := errors.New("file exists")
	for _, tc := range []struct {
		name    string
		ifc     machine.InterfaceSpec
		refuse  func(*netKernel)
		wantErr string
	}{
		{"an address with no prefix", machine.InterfaceSpec{Address: "10.10.0.5"}, func(*netKernel) {}, `address "10.10.0.5"`},
		{"the kernel refuses the address", staticPort("eth0"), func(k *netKernel) { k.addrErr = exists }, "assigning 10.10.0.5/24: file exists"},
		{"a gateway that is a name", machine.InterfaceSpec{Address: "10.10.0.5/24", Gateway: "router"}, func(*netKernel) {}, `gateway "router"`},
		{"the kernel refuses the route", staticPort("eth0"), func(k *netKernel) { k.routeErr = exists }, "default route via 10.10.0.254: file exists"},
		{"a nameserver that is a name", machine.InterfaceSpec{Address: "10.10.0.5/24", Nameservers: []string{"dns"}}, func(*netKernel) {}, `nameserver "dns"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := useNetKernel(t, netCard("eth0", 2))
			tc.refuse(k)
			_, err := applyStatic(k.links[0], tc.ifc)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the error is %v, want one that contains %q", err, tc.wantErr)
			}
		})
	}
}
