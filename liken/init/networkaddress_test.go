package main

// These tests cover addressing a link that is already up: a wired port
// right after it comes up, and a radio after it joins its network.
// They use the fake kernel and lease from networkbringup_test.go and
// check what is written to the kernel and what the connection records
// for status.

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/liken/machine"
)

// applyLease must add the lease's address and default route. The
// connection must record the expiry as the time of the ACK plus the
// lease time, so that rewriting the facts hours later reports the same
// expiry. The manifest's nameservers follow the lease's, and a
// manifest nameserver that is not an IP address is dropped.
func TestApplyLeaseRecordsTheLeaseAsTheKernelAndStatusSeeIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		k := useNetKernel(t, netCard("eth0", 2))
		ifc := machine.InterfaceSpec{Name: "eth0", Nameservers: []string{"10.10.0.1", "dns"}}

		ackTime := time.Now()
		conn, err := applyLease(k.links[0], labLease(t), ifc)
		if err != nil {
			t.Fatalf("applying the lease: %v", err)
		}

		if got := strings.Join(k.addrs, ";"); got != "eth0 192.168.1.20/24" {
			t.Errorf("the addresses are %q, want \"eth0 192.168.1.20/24\"", got)
		}
		if got := strings.Join(k.routes, ";"); got != "eth0 via 192.168.1.1" {
			t.Errorf("the routes are %q, want \"eth0 via 192.168.1.1\"", got)
		}
		if got := joinIPs(conn.nameservers); got != "192.168.1.1, 9.9.9.9, 10.10.0.1" {
			t.Errorf("the nameservers are %q, want \"192.168.1.1, 9.9.9.9, 10.10.0.1\"", got)
		}
		if conn.leaseTime != time.Hour || !conn.leaseExpires.Equal(ackTime.Add(time.Hour)) {
			t.Errorf("the lease is %s and expires at %s, want one hour from %s", conn.leaseTime, conn.leaseExpires, ackTime)
		}
		if conn.method != machine.MethodDHCP || !conn.server.Equal(net.ParseIP("192.168.1.1")) {
			t.Errorf("the method is %s and the server is %s, want %s and 192.168.1.1", conn.method, conn.server, machine.MethodDHCP)
		}
	})
}

// A lease with no router option must add no default route, because
// there is no gateway to route through.
func TestApplyLeaseWithNoRouterAddsNoRoute(t *testing.T) {
	k := useNetKernel(t, netCard("eth0", 2))
	lease := labLease(t)
	lease.ACK.Options.Del(dhcpv4.OptionRouter)

	conn, err := applyLease(k.links[0], lease, machine.InterfaceSpec{Name: "eth0"})
	if err != nil {
		t.Fatalf("applying the lease: %v", err)
	}
	if len(k.routes) != 0 || conn.gateway != nil {
		t.Errorf("the routes are %v and the gateway is %v, want neither", k.routes, conn.gateway)
	}
}

// When the kernel rejects the leased address or route, applyLease
// must return the kernel's error with the rejected value.
func TestApplyLeaseReturnsTheKernelsError(t *testing.T) {
	exists := errors.New("file exists")
	for _, tc := range []struct {
		name   string
		refuse func(*netKernel)
	}{
		{"the address", func(k *netKernel) { k.addrErr = exists }},
		{"the route", func(k *netKernel) { k.routeErr = exists }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := useNetKernel(t, netCard("eth0", 2))
			tc.refuse(k)
			_, err := applyLease(k.links[0], labLease(t), machine.InterfaceSpec{Name: "eth0"})
			if !errors.Is(err, exists) {
				t.Errorf("the error is %v, want one that wraps %q", err, exists)
			}
		})
	}
}

// When the DHCP exchange fails, addressInterface must return its
// error and add no address.
func TestAddressInterfaceReportsAFailedExchange(t *testing.T) {
	k := useNetKernel(t, netCard("eth0", 2))
	useFakeDHCP(t, nil, context.DeadlineExceeded)

	_, err := addressInterface(k.links[0], machine.InterfaceSpec{Name: "eth0"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the error is %v, want one that wraps %q", err, context.DeadlineExceeded)
	}
	if len(k.addrs) != 0 {
		t.Errorf("the kernel has the addresses %v, want none", k.addrs)
	}
}

// readdressSpec is a spec with one wired port and one radio that uses
// DHCP after it joins.
func readdressSpec() []machine.InterfaceSpec {
	return []machine.InterfaceSpec{staticPort("eth0"), declaredRadio("wlan0", "homenet")}
}

// A radio that joins while the boot waits for it (waitForRadio in
// wireless.go) must get an address and replace its addressless entry in
// the same position, so status lists one wlan0, with an address and the
// radio's state.
func TestReaddressRadioReplacesTheAddresslessEntry(t *testing.T) {
	useNetKernel(t, netCard("eth0", 2), netCard("wlan0", 3))
	useFakeDHCP(t, labLease(t), nil)
	conns := []*connection{addressed("eth0", "10.10.0.5/24"), refusedRadio("wlan0", "homenet")}
	r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessConnected}

	got := readdressRadio(conns, readdressSpec(), r)

	if names(got) != "eth0,wlan0" {
		t.Fatalf("the connections are %q, want \"eth0,wlan0\"", names(got))
	}
	if got[1].addr.String() != "192.168.1.20/24" || got[1].radio != r {
		t.Errorf("wlan0 has the address %v and the radio %v, want 192.168.1.20/24 and %v", got[1].addr, got[1].radio, r)
	}
}

// A radio that is not in the list yet must be appended, so that its
// address appears in status.
func TestReaddressRadioAppendsARadioTheListLacks(t *testing.T) {
	useNetKernel(t, netCard("wlan0", 3))
	useFakeDHCP(t, labLease(t), nil)
	r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessConnected}

	got := readdressRadio([]*connection{addressed("eth0", "10.10.0.5/24")}, readdressSpec(), r)
	if names(got) != "eth0,wlan0" || got[1].addr == nil {
		t.Errorf("the connections are %q and the address of wlan0 is %v, want eth0,wlan0 and an address", names(got), got[1].addr)
	}
}

// When readdressRadio gets no address for the radio, it must leave
// the list unchanged, so status keeps the failed join and not an
// entry with no address and no radio state.
func TestReaddressRadioKeepsTheListWhenNoAddressResults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state machine.WirelessState
		links []netlink.Link
		lease error
	}{
		{"the radio has not joined", machine.WirelessWrongKey, []netlink.Link{netCard("wlan0", 3)}, nil},
		{"the kernel has no such link", machine.WirelessConnected, nil, nil},
		{"the exchange fails", machine.WirelessConnected, []netlink.Link{netCard("wlan0", 3)}, errors.New("no offer")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := useNetKernel(t, tc.links...)
			asked := useFakeDHCP(t, labLease(t), tc.lease)
			left := refusedRadio("wlan0", "homenet")
			conns := []*connection{addressed("eth0", "10.10.0.5/24"), left}
			r := &radio{ifname: "wlan0", ssid: "homenet", state: tc.state}

			got := readdressRadio(conns, readdressSpec(), r)
			if names(got) != "eth0,wlan0" || got[1] != left {
				t.Errorf("the connections are %q and wlan0 was replaced: %v, want eth0,wlan0 and the entry kept", names(got), got[1] != left)
			}
			if len(k.addrs) != 0 {
				t.Errorf("the kernel has the addresses %v after %d DHCP exchanges, want none", k.addrs, len(*asked))
			}
		})
	}
}
