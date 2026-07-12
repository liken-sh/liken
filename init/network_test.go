package main

// Tests for the network derivations that are pure over their inputs.
// Interfaces, DHCP exchanges, and routing tables are QEMU territory.

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
	// The report prints the same facts interfaceFacts publishes;
	// exercising both methods pins that every field has a voice.
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
	// glibc has read at most three nameservers since the 1980s, every
	// other resolver stack follows it, and kubelet logs a warning on
	// every sync when a node offers more. Linode's DHCP hands out its
	// whole regional fleet (eighteen); the machine keeps three.
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
	// Interface order is priority order: the uplink's lease speaks
	// before a later interface's manifest declarations.
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
	// A resolver listed on two interfaces buys nothing twice, and
	// with only three slots a duplicate costs a real one.
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
