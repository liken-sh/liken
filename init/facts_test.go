package main

// Tests for the facts derivations that are pure over their inputs:
// how connections become network status. Publishing to /run is boot
// territory.

import (
	"net"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

var factsNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// fullConn builds a connection the way DHCP or static assignment
// would have: k3s_test's conn covers derivations that only need an
// address; these tests need every field filled in.
func fullConn(t *testing.T, ifname, cidr string, method machine.AddressMethod) *connection {
	t.Helper()
	c := conn(t, ifname, cidr)
	c.method = method
	c.mac = net.HardwareAddr{0x52, 0x54, 0x00, 0x00, 0x00, 0x01}
	c.gateway = net.ParseIP("10.0.2.2")
	c.nameservers = []net.IP{net.ParseIP("10.0.2.3")}
	if method == machine.MethodDHCP {
		c.leaseTime = time.Hour
	}
	return c
}

func TestNetworkFactsWithNoConnections(t *testing.T) {
	status := networkFacts(nil, nil, factsNow)
	if status.Interface != "" || len(status.Interfaces) != 0 {
		t.Errorf("no connections means no facts: %+v", status)
	}
}

func TestNetworkFactsSummarizesTheClusterFacingInterface(t *testing.T) {
	conns := []*connection{
		fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP),
		fullConn(t, "eth1", "10.10.0.2/24", machine.MethodStatic),
	}
	status := networkFacts(labCluster(), conns, factsNow)
	if status.Interface != "eth1" {
		t.Errorf("the nodeCIDR identifies the primary interface, got %s", status.Interface)
	}
	if len(status.Interfaces) != 2 {
		t.Errorf("every interface is reported: %+v", status.Interfaces)
	}
	if status.Addresses[0] != "10.10.0.2/24" {
		t.Errorf("got %v", status.Addresses)
	}
}

func TestNetworkFactsFallsBackToTheFirstConnection(t *testing.T) {
	conns := []*connection{
		fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP),
	}
	status := networkFacts(nil, conns, factsNow)
	if status.Interface != "eth0" {
		t.Errorf("with no cluster the first connection is primary, got %s", status.Interface)
	}
	if status.Gateway != "10.0.2.2" || status.Nameservers[0] != "10.0.2.3" {
		t.Errorf("the summary carries the lease's answers: %+v", status)
	}
}

func TestInterfaceFactsForALease(t *testing.T) {
	got := interfaceFacts(fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP), factsNow)
	if got.Method != machine.MethodDHCP {
		t.Errorf("got %s", got.Method)
	}
	if got.LeaseExpires == nil || !got.LeaseExpires.Equal(factsNow.Add(time.Hour)) {
		t.Errorf("a lease has an expiry: %v", got.LeaseExpires)
	}
}

func TestInterfaceFactsForAStaticAddress(t *testing.T) {
	got := interfaceFacts(fullConn(t, "eth1", "10.10.0.2/24", machine.MethodStatic), factsNow)
	if got.Method != machine.MethodStatic {
		t.Errorf("got %s", got.Method)
	}
	if got.LeaseExpires != nil {
		t.Error("a static address never expires")
	}
}
