package main

// Tests for the cluster-derived k3s configuration: role, node
// address, and the drop-in's contents. Writing the file and starting
// k3s are QEMU territory; the derivations are pinned here.

import (
	"net"
	"strings"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// conn builds a connection with just enough shape for derivation
// tests: an interface name and its address in CIDR form.
func conn(t *testing.T, ifname, cidr string) *connection {
	t.Helper()
	ip, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	return &connection{ifname: ifname, addr: &net.IPNet{IP: ip, Mask: subnet.Mask}}
}

func labCluster() *machine.Cluster {
	return &machine.Cluster{
		Metadata: machine.ObjectMeta{Name: "lab"},
		Spec: machine.ClusterSpec{
			Servers:  []string{"node-1"},
			Endpoint: "https://10.10.0.1:6443",
			Network: machine.ClusterNetworkSpec{
				NodeCIDR:      "10.10.0.0/24",
				ClusterCIDR:   "10.42.0.0/16",
				ServiceCIDR:   "10.43.0.0/16",
				ClusterDNS:    "10.43.0.10",
				ClusterDomain: "cluster.local",
			},
		},
	}
}

func TestNodeAddressPicksTheClusterFacingInterface(t *testing.T) {
	conns := []*connection{
		conn(t, "eth0", "10.0.2.15/24"), // the uplink: right machine, wrong wire
		conn(t, "eth1", "10.10.0.2/24"), // the cluster segment
	}
	ip, ifname := nodeAddress(labCluster(), conns)
	if ip != "10.10.0.2" || ifname != "eth1" {
		t.Errorf("got %s on %s, want 10.10.0.2 on eth1", ip, ifname)
	}
}

func TestNodeAddressWithoutAClusterIsUndecided(t *testing.T) {
	conns := []*connection{conn(t, "eth0", "10.0.2.15/24")}
	if ip, ifname := nodeAddress(nil, conns); ip != "" || ifname != "" {
		t.Errorf("no cluster should mean no derivation, got %s on %s", ip, ifname)
	}
}

func TestNodeAddressOutsideTheNodeCIDRIsUndecided(t *testing.T) {
	conns := []*connection{conn(t, "eth0", "10.0.2.15/24")}
	if ip, ifname := nodeAddress(labCluster(), conns); ip != "" || ifname != "" {
		t.Errorf("no address in the nodeCIDR should mean no derivation, got %s on %s", ip, ifname)
	}
}

func TestK3sBootConfigForAServer(t *testing.T) {
	got := k3sBootConfig(machine.RoleServer, labCluster(), "10.10.0.1", "eth1", true)
	for _, want := range []string{
		"token-file: /etc/liken/token\n",
		"cluster-cidr: 10.42.0.0/16\n",
		"service-cidr: 10.43.0.0/16\n",
		"cluster-dns: 10.43.0.10\n",
		"cluster-domain: cluster.local\n",
		"node-ip: 10.10.0.1\n",
		"flannel-iface: eth1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("server config should carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "server:") {
		t.Errorf("a server doesn't join an endpoint:\n%s", got)
	}
}

func TestK3sBootConfigForAnAgent(t *testing.T) {
	got := k3sBootConfig(machine.RoleAgent, labCluster(), "10.10.0.2", "eth1", true)
	for _, want := range []string{
		"token-file: /etc/liken/token\n",
		"server: https://10.10.0.1:6443\n",
		"node-ip: 10.10.0.2\n",
		"flannel-iface: eth1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("agent config should carry %q:\n%s", want, got)
		}
	}
	// The address plan is the control plane's to declare; an agent
	// telling k3s about it would be misread as unknown flags.
	for _, reject := range []string{"cluster-cidr", "service-cidr", "cluster-dns", "cluster-domain"} {
		if strings.Contains(got, reject) {
			t.Errorf("agent config should not carry %s:\n%s", reject, got)
		}
	}
}

func TestK3sBootConfigWithNoClusterIsNearlyEmpty(t *testing.T) {
	got := k3sBootConfig(machine.RoleServer, nil, "", "", true)
	if !strings.Contains(got, "token-file:") {
		t.Errorf("even a machine alone holds its token:\n%s", got)
	}
	for _, reject := range []string{"cluster-cidr", "node-ip", "server:"} {
		if strings.Contains(got, reject) {
			t.Errorf("a machine alone derives no %s:\n%s", reject, got)
		}
	}
}

func TestK3sBootConfigWithoutAToken(t *testing.T) {
	got := k3sBootConfig(machine.RoleServer, labCluster(), "10.10.0.1", "eth1", false)
	if strings.Contains(got, "token-file") {
		t.Errorf("no token file means no token-file entry:\n%s", got)
	}
}
