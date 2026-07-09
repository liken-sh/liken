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
		conn(t, "eth0", "10.0.2.15/24"), // the uplink, outside the nodeCIDR
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

// adoptedCluster is an HA cluster whose datastore liken did not
// create: the endpoint points at the existing (foreign) control
// plane, and origin: adopted says nobody may initialize a new one.
func adoptedCluster() *machine.Cluster {
	c := haCluster()
	c.Spec.Origin = machine.OriginAdopted
	c.Spec.Endpoint = "https://10.10.0.250:6443"
	return c
}

func TestLeaderJoinConfigAdoptedFounderJoinsTheEndpoint(t *testing.T) {
	clusterInit, joinURL := leaderJoinConfig(adoptedCluster(), "node-1", t.TempDir())
	if clusterInit || joinURL != "https://10.10.0.250:6443" {
		t.Errorf("an adopted cluster's founding leader joins the existing datastore, never initializes one: %v %q", clusterInit, joinURL)
	}
}

func TestLeaderJoinConfigAdoptedSingleLeaderStillJoins(t *testing.T) {
	c := adoptedCluster()
	c.Spec.Leaders = []string{"node-1"}
	clusterInit, joinURL := leaderJoinConfig(c, "node-1", t.TempDir())
	if clusterInit || joinURL != "https://10.10.0.250:6443" {
		t.Errorf("adoption is never sqlite: even a lone leader joins the existing datastore: %v %q", clusterInit, joinURL)
	}
}

func TestLeaderJoinConfigAdoptedJoiningLeaderPrefersTheFounder(t *testing.T) {
	dir := manifestsDir(t, map[string]string{"node-1": "10.10.0.1/24"})
	clusterInit, joinURL := leaderJoinConfig(adoptedCluster(), "node-3", dir)
	if clusterInit || joinURL != "https://10.10.0.1:6443" {
		t.Errorf("an adopted joining leader points at the founder like any other: %v %q", clusterInit, joinURL)
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

func TestK3sBootConfigRendersNodeLabels(t *testing.T) {
	labels := map[string]string{
		"topology.kubernetes.io/zone": "closet",
		"guid.foo/gpu":                "true",
	}
	for _, role := range []machine.Role{machine.RoleLeader, machine.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, cluster: labCluster(), haveToken: true, nodeLabels: labels})
		// The + suffix asks k3s to append to the static file's list
		// instead of replacing it; without it the drop-in would erase
		// liken.sh/machine=true. Keys render sorted, so the drop-in is
		// deterministic for the same spec.
		want := "node-label+:\n  - guid.foo/gpu=true\n  - topology.kubernetes.io/zone=closet\n"
		if !strings.Contains(got, want) {
			t.Errorf("%s config should append the spec's node labels:\n%s", role, got)
		}
	}
}

func TestK3sBootConfigWithoutNodeLabelsRendersNone(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: labCluster(), haveToken: true})
	if strings.Contains(got, "node-label") {
		t.Errorf("no declared labels means no node-label key:\n%s", got)
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
	// A machine alone still renders the complete disable list: no
	// cluster document means no opt-ins, and the minimum viable
	// cluster is the default.
	if !strings.Contains(got, "disable:\n  - metrics-server\n  - servicelb\n  - traefik\n") {
		t.Errorf("a machine alone disables everything bundled:\n%s", got)
	}
}

func TestK3sBootConfigDisablesEverythingBundledByDefault(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: labCluster(), haveToken: true})
	if !strings.Contains(got, "disable:\n  - metrics-server\n  - servicelb\n  - traefik\n") {
		t.Errorf("a cluster with no features disables everything bundled:\n%s", got)
	}
}

func TestK3sBootConfigLeavesOptedInComponentsOffTheDisableList(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*machine.FeatureConfig{"metrics-server": {}}
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: c, haveToken: true})
	if !strings.Contains(got, "disable:\n  - servicelb\n  - traefik\n") {
		t.Errorf("an opt-in leaves the disable list:\n%s", got)
	}
	if strings.Contains(got, "metrics-server") {
		t.Errorf("an opted-in component should not appear at all:\n%s", got)
	}
}

func TestK3sBootConfigWithEveryFeatureRendersNoDisableList(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*machine.FeatureConfig{
		"traefik": {}, "servicelb": {}, "metrics-server": {},
	}
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: c, haveToken: true})
	if strings.Contains(got, "disable") {
		t.Errorf("all opt-ins means no disable key at all:\n%s", got)
	}
}

func TestK3sBootConfigFollowersNeverRenderTheDisableList(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleFollower, cluster: labCluster(), haveToken: true})
	if strings.Contains(got, "disable") {
		t.Errorf("disable is a server-side key an agent would refuse:\n%s", got)
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

// fakeK3sConfigs points the drop-in writers at a tempdir standing in
// for /etc/rancher/k3s, and the token path at a tempdir file (present
// or not), restoring the real paths when the test ends.
func fakeK3sConfigs(t *testing.T, withToken bool) (serverDropIns, agentDropIns string) {
	t.Helper()
	dir := t.TempDir()
	oldServer, oldAgent, oldToken := k3sServerConfig, k3sAgentConfig, tokenPath
	k3sServerConfig = filepath.Join(dir, "config.yaml")
	k3sAgentConfig = filepath.Join(dir, "agent.yaml")
	tokenPath = filepath.Join(dir, "token")
	t.Cleanup(func() { k3sServerConfig, k3sAgentConfig, tokenPath = oldServer, oldAgent, oldToken })
	if withToken {
		if err := os.WriteFile(tokenPath, []byte("K10abc::server:secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return k3sServerConfig + ".d", k3sAgentConfig + ".d"
}

// bootMachine builds the winning manifest as writeK3sBootConfig
// receives it: a name, and whatever spec fields the boot renders.
func bootMachine(name string, labels map[string]string) *machine.Machine {
	return &machine.Machine{
		Metadata: machine.ObjectMeta{Name: name},
		Spec:     machine.MachineSpec{NodeLabels: labels},
	}
}

func TestWriteK3sBootConfigForALeader(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

	role, err := writeK3sBootConfig(labCluster(), bootMachine("node-1", map[string]string{"guid.foo/gpu": "true"}), conns)
	if err != nil {
		t.Fatal(err)
	}
	if role != machine.RoleLeader {
		t.Errorf("node-1 is in spec.leaders: %s", role)
	}
	raw, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, want := range []string{"token-file:", "cluster-cidr: 10.42.0.0/16", "node-ip: 10.10.0.1", "flannel-iface: eth1", "node-label+:\n  - guid.foo/gpu=true"} {
		if !strings.Contains(content, want) {
			t.Errorf("the leader drop-in should carry %q:\n%s", want, content)
		}
	}
}

func TestWriteK3sBootConfigForAFollower(t *testing.T) {
	_, agentDropIns := fakeK3sConfigs(t, true)
	cluster := labCluster()
	conns := []*connection{conn(t, "eth1", "10.10.0.2/24")}

	role, err := writeK3sBootConfig(cluster, bootMachine("node-2", nil), conns)
	if err != nil {
		t.Fatal(err)
	}
	if role != machine.RoleFollower {
		t.Errorf("node-2 is not in spec.leaders: %s", role)
	}
	raw, err := os.ReadFile(filepath.Join(agentDropIns, "boot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "server: https://10.10.0.1:6443") {
		t.Errorf("a follower points at the endpoint:\n%s", raw)
	}
}

func TestWriteK3sBootConfigRefusesAFollowerWithoutAnEndpoint(t *testing.T) {
	fakeK3sConfigs(t, true)
	cluster := labCluster()
	cluster.Spec.Endpoint = ""
	if _, err := writeK3sBootConfig(cluster, bootMachine("node-2", nil), nil); err == nil {
		t.Error("a follower with nowhere to join must refuse")
	}
}

func TestWriteK3sBootConfigRefusesAFollowerWithoutAToken(t *testing.T) {
	fakeK3sConfigs(t, false)
	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-2", nil), nil); err == nil {
		t.Error("a follower with no join token can never register")
	}
}

// fakeSeedSource stands in for the image's /var/lib/rancher tree.
func fakeSeedSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range []string{"k3s/server/tls", "k3s/server/manifests", "k3s/agent/images"} {
		if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel, "seeded"), []byte(rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := seedSourceDir
	seedSourceDir = dir
	t.Cleanup(func() { seedSourceDir = old })
	return dir
}

func TestSeedClusterStateCopiesTheSeeds(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	if err := seedClusterState(root); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"k3s/server/tls", "k3s/server/manifests", "k3s/agent/images"} {
		if _, err := os.Stat(filepath.Join(root, rel, "seeded")); err != nil {
			t.Errorf("%s should be seeded: %v", rel, err)
		}
	}
}

func TestSeedClusterStateKeepsIdentityAndRefreshesManifests(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	// The disk already carries an identity and an old manifest tree.
	for _, rel := range []string{"k3s/server/tls", "k3s/server/manifests"} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel, "existing"), []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := seedClusterState(root); err != nil {
		t.Fatal(err)
	}
	// TLS is identity: the disk's copy wins, and the seed never lands.
	if _, err := os.Stat(filepath.Join(root, "k3s/server/tls", "existing")); err != nil {
		t.Error("a disk that has an identity keeps it")
	}
	if _, err := os.Stat(filepath.Join(root, "k3s/server/tls", "seeded")); err == nil {
		t.Error("the seed must not overwrite existing TLS material")
	}
	// Manifests are the running image's: refreshed wholesale.
	if _, err := os.Stat(filepath.Join(root, "k3s/server/manifests", "existing")); err == nil {
		t.Error("old manifests are replaced by the image's")
	}
	if _, err := os.Stat(filepath.Join(root, "k3s/server/manifests", "seeded")); err != nil {
		t.Error("the image's manifests land on every boot")
	}
}

func TestSeedClusterStateWithoutSeedsIsANoOp(t *testing.T) {
	old := seedSourceDir
	seedSourceDir = filepath.Join(t.TempDir(), "nothing")
	t.Cleanup(func() { seedSourceDir = old })
	if err := seedClusterState(t.TempDir()); err != nil {
		t.Errorf("an image without k3s has no seed files, and that's fine: %v", err)
	}
}

func TestPurgeLeaderLeftoversReportsAFailedRemoval(t *testing.T) {
	parent := t.TempDir()
	db := filepath.Join(parent, "db")
	if err := os.MkdirAll(filepath.Join(db, "etcd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	purgeLeaderLeftovers(machine.RoleFollower, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("a failed purge leaves the datastore; the error is reported, not hidden")
	}
}
