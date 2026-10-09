package main

// Addressing is the half of the bring-up that runs after a link is up,
// on a wired port at once and on a radio once it joins. These tests
// drive it through the stand-in kernel and lease in
// networkbringup_test.go, and check what reaches the kernel and the
// connection that status reports.

import (
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

// A lease must become the address and default route it names, and
// the connection must record its expiry at the moment the ACK landed,
// so a facts rewrite hours later reports the same expiry. The
// manifest's own nameservers come after the lease's, and a manifest
// value that is not an address is left out.
func TestApplyLeaseRecordsTheLeaseAsTheKernelAndStatusSeeIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		k := netlinkAnswers(t, netCard("eth0", 2))
		ifc := machine.InterfaceSpec{Name: "eth0", Nameservers: []string{"10.10.0.1", "dns"}}

		landed := time.Now()
		conn, err := applyLease(k.links[0], labLease(t), ifc)
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(k.addrs, ";"); got != "eth0 192.168.1.20/24" {
			t.Errorf("addresses %q", got)
		}
		if got := strings.Join(k.routes, ";"); got != "eth0 via 192.168.1.1" {
			t.Errorf("routes %q", got)
		}
		if got := joinIPs(conn.nameservers); got != "192.168.1.1, 9.9.9.9, 10.10.0.1" {
			t.Errorf("nameservers %q", got)
		}
		if conn.leaseTime != time.Hour || !conn.leaseExpires.Equal(landed.Add(time.Hour)) {
			t.Errorf("lease %s, expires %s, want an hour from %s", conn.leaseTime, conn.leaseExpires, landed)
		}
		if conn.method != machine.MethodDHCP || !conn.server.Equal(net.ParseIP("192.168.1.1")) {
			t.Errorf("method %s, server %s", conn.method, conn.server)
		}
	})
}

// A lease that names no router is a segment with no way out. The boot
// must add no default route for it, or the route would point at
// nothing.
func TestApplyLeaseWithNoRouterAddsNoRoute(t *testing.T) {
	k := netlinkAnswers(t, netCard("eth0", 2))
	lease := labLease(t)
	lease.ACK.Options.Del(dhcpv4.OptionRouter)

	conn, err := applyLease(k.links[0], lease, machine.InterfaceSpec{Name: "eth0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(k.routes) != 0 || conn.gateway != nil {
		t.Errorf("routes %v, gateway %v", k.routes, conn.gateway)
	}
}

// The kernel's refusal of the leased address or route must reach the
// error word for word, beside the value it refused.
func TestApplyLeaseCarriesTheKernelsRefusal(t *testing.T) {
	exists := errors.New("file exists")
	for _, tc := range []struct {
		name    string
		refuse  func(*netKernel)
		wantErr string
	}{
		{"the address", func(k *netKernel) { k.addrErr = exists }, "assigning 192.168.1.20/24: file exists"},
		{"the route", func(k *netKernel) { k.routeErr = exists }, "default route via 192.168.1.1: file exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := netlinkAnswers(t, netCard("eth0", 2))
			tc.refuse(k)
			_, err := applyLease(k.links[0], labLease(t), machine.InterfaceSpec{Name: "eth0"})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("got %v, want an error with %q", err, tc.wantErr)
			}
		})
	}
}

// A DHCP exchange that gets no answer must fail the addressing with
// the exchange's own error, and must give the kernel no address.
func TestAddressInterfaceReportsAFailedExchange(t *testing.T) {
	k := netlinkAnswers(t, netCard("eth0", 2))
	leaseAnswers(t, nil, errors.New("DHCP on eth0: context deadline exceeded"))

	_, err := addressInterface(k.links[0], machine.InterfaceSpec{Name: "eth0"})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("got %v", err)
	}
	if len(k.addrs) != 0 {
		t.Errorf("the kernel was given addresses: %v", k.addrs)
	}
}

// readdressSpec is a spec with a wired uplink and one radio that
// takes DHCP once it joins.
func readdressSpec() []machine.InterfaceSpec {
	return []machine.InterfaceSpec{staticPort("eth0"), declaredRadio("wlan0", "homenet")}
}

// A radio that joins after the park released the boot must take its
// address and replace the addressless entry its failed join left, in
// place, so status reports one wlan0 that now has an address and
// still carries the radio's verdict.
func TestReaddressRadioReplacesTheAddresslessEntry(t *testing.T) {
	netlinkAnswers(t, netCard("eth0", 2), netCard("wlan0", 3))
	leaseAnswers(t, labLease(t), nil)
	conns := []*connection{addressed("eth0", "10.10.0.5/24"), refusedRadio("wlan0", "homenet")}
	r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessConnected}

	got := readdressRadio(conns, readdressSpec(), r)

	if names(got) != "eth0,wlan0" {
		t.Fatalf("connections %q", names(got))
	}
	if got[1].addr.String() != "192.168.1.20/24" || got[1].radio != r {
		t.Errorf("wlan0 is %v with radio %v", got[1].addr, got[1].radio)
	}
}

// A radio missing from the list still lands in it, so its address is
// never lost from status.
func TestReaddressRadioAppendsARadioTheListLacks(t *testing.T) {
	netlinkAnswers(t, netCard("wlan0", 3))
	leaseAnswers(t, labLease(t), nil)
	r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessConnected}

	got := readdressRadio([]*connection{addressed("eth0", "10.10.0.5/24")}, readdressSpec(), r)
	if names(got) != "eth0,wlan0" || got[1].addr == nil {
		t.Errorf("connections %q", names(got))
	}
}

// Every path that leaves the radio without an address must leave the
// list as it was, so status keeps reporting the failed join rather
// than an entry with neither an address nor a verdict.
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
			k := netlinkAnswers(t, tc.links...)
			asked := leaseAnswers(t, labLease(t), tc.lease)
			left := refusedRadio("wlan0", "homenet")
			conns := []*connection{addressed("eth0", "10.10.0.5/24"), left}
			r := &radio{ifname: "wlan0", ssid: "homenet", state: tc.state}

			got := readdressRadio(conns, readdressSpec(), r)
			if names(got) != "eth0,wlan0" || got[1] != left {
				t.Errorf("connections %q, wlan0 replaced: %v", names(got), got[1] != left)
			}
			if len(k.addrs) != 0 {
				t.Errorf("the kernel was given addresses %v after %d exchanges", k.addrs, len(*asked))
			}
		})
	}
}
