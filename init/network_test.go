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
