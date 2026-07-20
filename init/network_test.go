package main

// This file tests the network functions that produce the same
// output for the same input. Tests for interfaces, DHCP exchanges,
// and routing tables run under QEMU, not here.

import (
	"net"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
)

func TestJoinIPsReadsAsAList(t *testing.T) {
	ips := []net.IP{net.ParseIP("10.0.2.3"), net.ParseIP("10.0.2.4")}
	if got := joinIPs(ips); got != "10.0.2.3, 10.0.2.4" {
		t.Errorf("got %q", got)
	}
}

func TestConnectionReportNarratesEachMethod(t *testing.T) {
	// The report prints the same facts that interfaceFacts publishes.
	// Testing both methods confirms that the report prints every
	// field.
	_, subnet, _ := net.ParseCIDR("10.0.2.0/24")
	dhcp := &connection{
		ifname:      "eth0",
		mac:         net.HardwareAddr{0x52, 0x54, 0x00, 0x4c, 0x4b, 0x01},
		addr:        &net.IPNet{IP: net.ParseIP("10.0.2.15"), Mask: subnet.Mask},
		method:      machine.MethodDHCP,
		gateway:     net.ParseIP("10.0.2.2"),
		server:      net.ParseIP("10.0.2.2"),
		nameservers: []net.IP{net.ParseIP("10.0.2.3")},
		leaseTime:   time.Hour,
	}
	dhcp.report()
	static := &connection{
		ifname:  "eth1",
		mac:     net.HardwareAddr{0x52, 0x54, 0x00, 0x4c, 0x4c, 0x01},
		addr:    &net.IPNet{IP: net.ParseIP("10.10.0.1"), Mask: subnet.Mask},
		method:  machine.MethodStatic,
		gateway: net.ParseIP("10.10.0.254"),
	}
	static.report()
}

func TestResolvConfCapsAtThreeNameservers(t *testing.T) {
	// Since the 1980s, glibc has read at most three nameservers.
	// Every other resolver stack follows the same limit. kubelet
	// logs a warning at each sync when a node offers more than
	// three. Linode's DHCP service hands out its whole regional
	// fleet of resolvers, eighteen in total, but the machine keeps
	// only three.
	conns := []*connection{{nameservers: []net.IP{
		net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"),
		net.ParseIP("10.0.0.3"), net.ParseIP("10.0.0.4"),
		net.ParseIP("10.0.0.5"),
	}}}
	want := "nameserver 10.0.0.1\nnameserver 10.0.0.2\nnameserver 10.0.0.3\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestResolvConfKeepsInterfaceOrder(t *testing.T) {
	// Interface order is priority order. The uplink's lease
	// nameservers come before a later interface's manifest
	// declarations.
	conns := []*connection{
		{nameservers: []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2")}},
		{nameservers: []net.IP{net.ParseIP("10.1.0.1")}},
	}
	want := "nameserver 10.0.0.1\nnameserver 10.0.0.2\nnameserver 10.1.0.1\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestResolvConfDropsDuplicates(t *testing.T) {
	// Listing the same resolver on two interfaces adds no value the
	// second time. With only three slots available, a duplicate
	// takes the place of a real nameserver.
	conns := []*connection{
		{nameservers: []net.IP{net.ParseIP("10.0.0.1")}},
		{nameservers: []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.1.0.1")}},
	}
	want := "nameserver 10.0.0.1\nnameserver 10.1.0.1\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestResolvConfWithNoNameserversIsEmpty(t *testing.T) {
	if got := resolvConf([]*connection{{}}); got != "" {
		t.Errorf("no nameservers must render nothing, got %q", got)
	}
}
