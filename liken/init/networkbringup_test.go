package main

// The bring-up of a wired port is a short list of netlink calls and
// one DHCP exchange, and the decisions around them are where a
// manifest's mistakes show: which port the boot picks, which address
// and route it asks the kernel for, and what reaches resolv.conf. The
// tests here run those decisions against a stand-in kernel that
// records what it was asked, so they need no root and no wire.

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

// netKernel stands in for the kernel's side of netlink. It lists its
// links, and it records each link the bring-up raises and each
// address and route the bring-up adds, in the form `ip` would print
// them. A refusal field makes the matching call fail with the
// kernel's own words.
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

// netCard is a link with a hardware address, the way a real network
// card appears in the kernel's list.
func netCard(name string, index int) netlink.Link {
	return &netlink.Device{LinkAttrs: netlink.LinkAttrs{
		Name: name, Index: index,
		HardwareAddr: net.HardwareAddr{0x52, 0x54, 0x00, 0x4c, 0x4b, byte(index)},
	}}
}

// netLoopback is the loopback link the kernel creates on its own.
func netLoopback() netlink.Link {
	return &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "lo", Index: 1, Flags: net.FlagLoopback}}
}

// netlinkAnswers installs the stand-in kernel behind every netlink
// seam the bring-up calls, and restores the real calls when the test
// ends.
func netlinkAnswers(t *testing.T, links ...netlink.Link) *netKernel {
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

// nameOf finds the link a route names by its index.
func (k *netKernel) nameOf(index int) string {
	for _, link := range k.links {
		if link.Attrs().Index == index {
			return link.Attrs().Name
		}
	}
	return fmt.Sprintf("index %d", index)
}

// staticPort is a wired port with the address, gateway, and
// nameserver of the lab's cluster segment.
func staticPort(name string) machine.InterfaceSpec {
	return machine.InterfaceSpec{
		Name: name, Address: "10.10.0.5/24", Gateway: "10.10.0.254",
		Nameservers: []string{"10.10.0.1"},
	}
}

// A declared static port must reach the kernel as exactly the address
// and default route the manifest wrote, and its nameserver must reach
// resolv.conf. Loopback is raised too, because nearly all networked
// software assumes 127.0.0.1 exists.
func TestBringUpNetworkAppliesADeclaredStaticPort(t *testing.T) {
	resolv := aimResolvConf(t)
	k := netlinkAnswers(t, netLoopback(), netCard("eth0", 2), netCard("eth1", 3))

	spec := machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{staticPort("eth1")}}
	conns, pass, err := bringUpNetwork(spec, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(k.raised, ","); got != "lo,eth1" {
		t.Errorf("raised %q, want lo and the declared port only", got)
	}
	if got := strings.Join(k.addrs, ";"); got != "eth1 10.10.0.5/24" {
		t.Errorf("addresses %q", got)
	}
	if got := strings.Join(k.routes, ";"); got != "eth1 via 10.10.0.254" {
		t.Errorf("routes %q", got)
	}
	if len(conns) != 1 || conns[0].method != machine.MethodStatic {
		t.Errorf("connections %v", conns)
	}
	if pass != nil {
		t.Error("a wired-only spec starts no radio pass")
	}
	if got := readFile(t, resolv); got != "nameserver 10.10.0.1\n" {
		t.Errorf("resolv.conf %q", got)
	}
}

// labLease builds the ACK a home router sends: an address on its /24,
// itself as router, DNS, and DHCP server, a public resolver beside it,
// and a one-hour lease.
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
		t.Fatal(err)
	}
	return &nclient4.Lease{ACK: ack}
}

// leaseAnswers installs a stand-in DHCP exchange that answers every
// request with one lease or one error, and records which interfaces
// asked.
func leaseAnswers(t *testing.T, lease *nclient4.Lease, err error) *[]string {
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

// A manifest that names no interface gets the zero-configuration
// default: DHCP on the first link that carries a hardware address. A
// virtual device listed before the card must not take the lease.
func TestBringUpNetworkTakesDHCPOnTheFirstCardWhenTheSpecNamesNone(t *testing.T) {
	resolv := aimResolvConf(t)
	dummy := &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "dummy0", Index: 2}}
	k := netlinkAnswers(t, netLoopback(), dummy, netCard("eth0", 3))
	asked := leaseAnswers(t, labLease(t), nil)

	conns, _, err := bringUpNetwork(machine.NetworkSpec{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(*asked, ","); got != "eth0" {
		t.Errorf("DHCP ran on %q, want eth0", got)
	}
	if got := strings.Join(k.addrs, ";"); got != "eth0 192.168.1.20/24" {
		t.Errorf("addresses %q", got)
	}
	if got := strings.Join(k.routes, ";"); got != "eth0 via 192.168.1.1" {
		t.Errorf("routes %q", got)
	}
	if len(conns) != 1 || conns[0].method != machine.MethodDHCP {
		t.Errorf("connections %v", conns)
	}
	if got := readFile(t, resolv); got != "nameserver 192.168.1.1\nnameserver 9.9.9.9\n" {
		t.Errorf("resolv.conf %q", got)
	}
}

// Every way the bring-up can end with no usable interface must come
// back as an error that names the cause, and must leave resolv.conf
// unwritten, because a boot with no path reports to the console
// instead of going on.
func TestBringUpNetworkRefusals(t *testing.T) {
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
			resolv := aimResolvConf(t)
			k := netlinkAnswers(t, tc.links...)
			tc.refuse(k)

			_, _, err := bringUpNetwork(tc.spec, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("got %v, want an error with %q", err, tc.wantErr)
			}
			if len(k.addrs) != 0 {
				t.Errorf("the kernel was given addresses: %v", k.addrs)
			}
			if _, err := os.Stat(resolv); !os.IsNotExist(err) {
				t.Errorf("resolv.conf was written: %v", err)
			}
		})
	}
}

// A resolv.conf that cannot be written must fail the bring-up with the
// path in the error, and the connections must still come back, so the
// console report shows the ports that did come up.
func TestBringUpNetworkReportsAResolvConfItCannotWrite(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "etc", "resolv.conf")
	orig := resolvConfPath
	resolvConfPath = missing
	t.Cleanup(func() { resolvConfPath = orig })
	netlinkAnswers(t, netLoopback(), netCard("eth0", 2))

	spec := machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{staticPort("eth0")}}
	conns, _, err := bringUpNetwork(spec, nil)

	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("got %v, want an error naming %s", err, missing)
	}
	if !anyAddressed(conns) {
		t.Error("the addressed port is missing from the connections")
	}
}

// A port that cannot come up must say why in words a person can act
// on: the ports the machine has, or the kernel's own refusal.
func TestBringUpInterfaceNamesWhyAPortStayedDown(t *testing.T) {
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
			k := netlinkAnswers(t, netCard("eth0", 2))
			k.raiseErr = tc.refuse
			_, err := bringUpInterface(machine.InterfaceSpec{Name: tc.port}, tc.present)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("got %v, want an error with %q", err, tc.wantErr)
			}
		})
	}
}

// readFile reads a file the test expects to exist.
func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A declared address with no gateway is a cluster segment, not an
// uplink. Adding a default route for it would steal the machine's
// traffic from the port that has one.
func TestApplyStaticWithNoGatewayAddsNoRoute(t *testing.T) {
	k := netlinkAnswers(t, netCard("eth1", 3))
	conn, err := applyStatic(k.links[0], machine.InterfaceSpec{Name: "eth1", Address: "10.10.0.5/24"})
	if err != nil {
		t.Fatal(err)
	}
	if len(k.routes) != 0 || conn.gateway != nil {
		t.Errorf("routes %v, gateway %v", k.routes, conn.gateway)
	}
	if conn.addr.String() != "10.10.0.5/24" {
		t.Errorf("address %s", conn.addr)
	}
}

// Each value the manifest wrote and the kernel's refusal of each call
// must reach the error word for word, because the console is the only
// place a person can read why the port has no address.
func TestApplyStaticRefusesWhatItCannotApply(t *testing.T) {
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
			k := netlinkAnswers(t, netCard("eth0", 2))
			tc.refuse(k)
			_, err := applyStatic(k.links[0], tc.ifc)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("got %v, want an error with %q", err, tc.wantErr)
			}
		})
	}
}
