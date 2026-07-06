package main

// Tests for the network derivations that are pure over their inputs.
// Interfaces, DHCP exchanges, and routing tables are QEMU territory.

import (
	"net"
	"testing"
)

func TestJoinIPsReadsAsAList(t *testing.T) {
	ips := []net.IP{net.ParseIP("10.0.2.3"), net.ParseIP("10.0.2.4")}
	if got := joinIPs(ips); got != "10.0.2.3, 10.0.2.4" {
		t.Errorf("got %q", got)
	}
}
