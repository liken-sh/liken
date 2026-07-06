package main

// Tests for the cluster-derived k3s configuration: role, node
// address, and the drop-in's contents. Writing the file and starting
// k3s are QEMU territory; the derivations are pinned here.

import (
	"net"
	"os"
	"path/filepath"
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
			Leaders:  []string{"node-1"},
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

func TestLeaderJoinConfigWithOneLeaderStaysAlone(t *testing.T) {
	clusterInit, joinURL := leaderJoinConfig(labCluster(), "node-1", t.TempDir())
	if clusterInit || joinURL != "" {
		t.Errorf("a single leader is sqlite-backed and joins nothing: %v %q", clusterInit, joinURL)
	}
}

func haCluster() *machine.Cluster {
	c := labCluster()
	c.Spec.Leaders = []string{"node-1", "node-3", "node-4"}
	return c
}

func TestLeaderJoinConfigForTheFoundingLeader(t *testing.T) {
	clusterInit, joinURL := leaderJoinConfig(haCluster(), "node-1", t.TempDir())
	if !clusterInit || joinURL != "" {
		t.Errorf("the founding leader renders cluster-init and joins nothing: %v %q", clusterInit, joinURL)
	}
}

func TestLeaderJoinConfigForAJoiningLeader(t *testing.T) {
	dir := manifestsDir(t, map[string]string{"node-1": "10.10.0.1/24"})
	clusterInit, joinURL := leaderJoinConfig(haCluster(), "node-3", dir)
	if clusterInit || joinURL != "https://10.10.0.1:6443" {
		t.Errorf("a joining leader points at the founder: %v %q", clusterInit, joinURL)
	}
}

func TestLeaderJoinConfigFallsBackToTheEndpoint(t *testing.T) {
	// The founder declares no static address (DHCP); the endpoint is
	// the one address the deployment promised is reachable.
	clusterInit, joinURL := leaderJoinConfig(haCluster(), "node-3", t.TempDir())
	if clusterInit || joinURL != "https://10.10.0.1:6443" {
		t.Errorf("an unresolvable founder falls back to the endpoint: %v %q", clusterInit, joinURL)
	}
}

func TestK3sBootConfigForTheFoundingLeader(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: haCluster(), nodeIP: "10.10.0.1", nodeInterface: "eth1", haveToken: true, clusterInit: true})
	if !strings.Contains(got, "cluster-init: true\n") {
		t.Errorf("the founding leader migrates to embedded etcd:\n%s", got)
	}
	if strings.Contains(got, "server:") {
		t.Errorf("the founding leader joins nothing:\n%s", got)
	}
}

func TestK3sBootConfigForAJoiningLeader(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: haCluster(), nodeIP: "10.10.0.3", nodeInterface: "eth1", haveToken: true, joinURL: "https://10.10.0.1:6443"})
	if !strings.Contains(got, "server: https://10.10.0.1:6443\n") {
		t.Errorf("a joining leader points at the founder:\n%s", got)
	}
	if strings.Contains(got, "cluster-init") {
		t.Errorf("only the founding leader renders cluster-init:\n%s", got)
	}
	// Every leader carries the address plan; they must all agree.
	if !strings.Contains(got, "cluster-cidr: 10.42.0.0/16\n") {
		t.Errorf("a joining leader still declares the address plan:\n%s", got)
	}
}

func TestK3sBootConfigForALeader(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: labCluster(), nodeIP: "10.10.0.1", nodeInterface: "eth1", haveToken: true})
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
			t.Errorf("leader config should carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "server:") {
		t.Errorf("a leader doesn't join an endpoint:\n%s", got)
	}
}

func TestK3sBootConfigForAFollower(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleFollower, cluster: labCluster(), nodeIP: "10.10.0.2", nodeInterface: "eth1", haveToken: true})
	for _, want := range []string{
		"token-file: /etc/liken/token\n",
		"server: https://10.10.0.1:6443\n",
		"node-ip: 10.10.0.2\n",
		"flannel-iface: eth1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("follower config should carry %q:\n%s", want, got)
		}
	}
	// The address plan is the control plane's to declare; a follower
	// telling k3s about it would be misread as unknown flags.
	for _, reject := range []string{"cluster-cidr", "service-cidr", "cluster-dns", "cluster-domain"} {
		if strings.Contains(got, reject) {
			t.Errorf("follower config should not carry %s:\n%s", reject, got)
		}
	}
}

func TestK3sBootConfigWithNoClusterIsNearlyEmpty(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, haveToken: true})
	if !strings.Contains(got, "token-file:") {
		t.Errorf("even a machine alone holds its token:\n%s", got)
	}
	for _, reject := range []string{"cluster-cidr", "node-ip", "server:"} {
		if strings.Contains(got, reject) {
			t.Errorf("a machine alone derives no %s:\n%s", reject, got)
		}
	}
}

func leaderDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(filepath.Join(db, "etcd", "member"), 0o755); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAProvenFollowerPurgesLeaderLeftovers(t *testing.T) {
	db := leaderDB(t)
	purgeLeaderLeftovers(machine.RoleFollower, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Error("a proven follower keeps no control-plane datastore")
	}
}

func TestAStagedFollowerBootKeepsTheDatastore(t *testing.T) {
	// The trial boot of a document that demotes this machine must not
	// destroy anything: if the document fails to prove, the fallback
	// boots the leader role again and needs its datastore intact.
	db := leaderDB(t)
	purgeLeaderLeftovers(machine.RoleFollower, machine.ManifestSourceStaged, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("an unproven demotion must leave the datastore alone")
	}
}

func TestALeaderKeepsItsDatastore(t *testing.T) {
	db := leaderDB(t)
	purgeLeaderLeftovers(machine.RoleLeader, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("a leader's datastore is the cluster; hands off")
	}
}

func TestK3sBootConfigWithoutAToken(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: labCluster(), nodeIP: "10.10.0.1", nodeInterface: "eth1"})
	if strings.Contains(got, "token-file") {
		t.Errorf("no token file means no token-file entry:\n%s", got)
	}
}
