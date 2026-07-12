package main

// Tests for the cluster-derived k3s configuration: role, node
// address, and the drop-in's contents. k3s's on-disk state (the
// clusterState seeds, a demoted leader's datastore) is
// k3s_state_test.go's side of the seam; starting k3s itself is QEMU
// territory. The derivations are pinned here.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
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
		"traefik": {}, "servicelb": {}, "metrics-server": {}, "network-policy": {},
	}
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: c, haveToken: true})
	if strings.Contains(got, "disable") {
		t.Errorf("all opt-ins means no disable key at all:\n%s", got)
	}
}

func TestK3sBootConfigDisablesTheHelmControllerByDefault(t *testing.T) {
	// Both shapes of "no opt-ins": a machine alone and a cluster
	// document with no features. The Helm controller lives inside the
	// k3s server process, so its key is its own, not a disable-list
	// entry.
	for name, in := range map[string]k3sBootInputs{
		"no cluster":  {role: machine.RoleLeader, haveToken: true},
		"no features": {role: machine.RoleLeader, cluster: labCluster(), haveToken: true},
	} {
		if got := k3sBootConfig(in); !strings.Contains(got, "disable-helm-controller: true\n") {
			t.Errorf("%s: the helm controller is an opt-in:\n%s", name, got)
		}
	}
}

func TestK3sBootConfigHelmFeatureKeepsTheHelmController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*machine.FeatureConfig{"helm": {}}
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: c, haveToken: true})
	if strings.Contains(got, "disable-helm-controller") {
		t.Errorf("declaring helm keeps the controller:\n%s", got)
	}
}

func TestK3sBootConfigTraefikImpliesTheHelmController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*machine.FeatureConfig{"traefik": {}}
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: c, haveToken: true})
	if strings.Contains(got, "disable-helm-controller") {
		t.Errorf("traefik requires helm, so the controller stays:\n%s", got)
	}
}

func TestK3sBootConfigDisablesTheCloudControllerByDefault(t *testing.T) {
	for name, in := range map[string]k3sBootInputs{
		"no cluster":  {role: machine.RoleLeader, haveToken: true},
		"no features": {role: machine.RoleLeader, cluster: labCluster(), haveToken: true},
	} {
		if got := k3sBootConfig(in); !strings.Contains(got, "disable-cloud-controller: true\n") {
			t.Errorf("%s: the embedded cloud controller runs only for servicelb:\n%s", name, got)
		}
	}
}

func TestK3sBootConfigDisablesNetworkPolicyByDefault(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: labCluster(), haveToken: true})
	if !strings.Contains(got, "disable-network-policy: true\n") {
		t.Errorf("network policy enforcement is an opt-in:\n%s", got)
	}
}

func TestK3sBootConfigNetworkPolicyFeatureKeepsTheController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*machine.FeatureConfig{"network-policy": {}}
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: c, haveToken: true})
	if strings.Contains(got, "disable-network-policy") {
		t.Errorf("declaring network-policy keeps the controller:\n%s", got)
	}
}

func TestK3sBootConfigServiceLBKeepsTheCloudController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*machine.FeatureConfig{"servicelb": {}}
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: c, haveToken: true})
	if strings.Contains(got, "disable-cloud-controller") {
		t.Errorf("servicelb runs inside the cloud controller, so it must stay:\n%s", got)
	}
}

func TestK3sBootConfigFollowersNeverRenderTheDisableList(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleFollower, cluster: labCluster(), haveToken: true})
	if strings.Contains(got, "disable") {
		t.Errorf("disable is a server-side key an agent would refuse:\n%s", got)
	}
}

func TestK3sBootConfigEmbeddedRegistryOnLeaders(t *testing.T) {
	c := labCluster()
	c.Spec.Registries.Embedded = true
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: c, haveToken: true})
	if !strings.Contains(got, "embedded-registry: true\n") {
		t.Errorf("an embedded opt-in renders the server key:\n%s", got)
	}
}

func TestK3sBootConfigEmbeddedRegistryOffByDefault(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleLeader, cluster: labCluster(), haveToken: true})
	if strings.Contains(got, "embedded-registry") {
		t.Errorf("the embedded registry is an opt-in:\n%s", got)
	}
}

func TestK3sBootConfigFollowersNeverRenderEmbeddedRegistry(t *testing.T) {
	c := labCluster()
	c.Spec.Registries.Embedded = true
	got := k3sBootConfig(k3sBootInputs{role: machine.RoleFollower, cluster: c, haveToken: true})
	if strings.Contains(got, "embedded-registry") {
		t.Errorf("embedded-registry is a server-side key an agent would refuse:\n%s", got)
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

func TestNodeAddressReportsAGarbageNodeCIDR(t *testing.T) {
	c := labCluster()
	c.Spec.Network.NodeCIDR = "not-a-cidr"
	conns := []*connection{conn(t, "eth1", "10.10.0.2/24")}
	if ip, ifname := nodeAddress(c, conns); ip != "" || ifname != "" {
		t.Errorf("a CIDR that won't parse derives nothing: %s on %s", ip, ifname)
	}
}

func TestWriteK3sBootConfigNarratesTheFoundingLeader(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

	role, err := writeK3sBootConfig(haCluster(), bootMachine("node-1", nil), conns)
	if err != nil || role != machine.RoleLeader {
		t.Fatalf("the founding leader is a leader: %s, %v", role, err)
	}
	raw, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "cluster-init: true") {
		t.Errorf("the founding leader's drop-in initializes etcd:\n%s", raw)
	}
}

func TestWriteK3sBootConfigNarratesAJoiningLeader(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	// The founder declares no resolvable address (the image carries no
	// manifest for it here), so the join falls back to the endpoint.
	conns := []*connection{conn(t, "eth1", "10.10.0.3/24")}

	role, err := writeK3sBootConfig(haCluster(), bootMachine("node-3", nil), conns)
	if err != nil || role != machine.RoleLeader {
		t.Fatalf("a joining leader is a leader: %s, %v", role, err)
	}
	raw, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "server: https://10.10.0.1:6443") {
		t.Errorf("a joining leader points at its control plane:\n%s", raw)
	}
}

func TestWriteK3sBootConfigWarnsAFollowerOutsideTheNodeCIDR(t *testing.T) {
	_, agentDropIns := fakeK3sConfigs(t, true)
	// The follower's one address is outside the cluster's nodeCIDR:
	// the warning prints and k3s is left to guess.
	conns := []*connection{conn(t, "eth0", "192.168.1.5/24")}

	role, err := writeK3sBootConfig(labCluster(), bootMachine("node-2", nil), conns)
	if err != nil || role != machine.RoleFollower {
		t.Fatalf("still a follower: %s, %v", role, err)
	}
	raw, err := os.ReadFile(filepath.Join(agentDropIns, "boot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "node-ip") {
		t.Errorf("no derived address means no node-ip key:\n%s", raw)
	}
}

func TestWriteK3sBootConfigReportsAnUnwritableDropInDir(t *testing.T) {
	fakeK3sConfigs(t, true)
	// The config path sits under a plain file, so the drop-in
	// directory can't be created.
	blocked := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := k3sServerConfig
	k3sServerConfig = filepath.Join(blocked, "config.yaml")
	t.Cleanup(func() { k3sServerConfig = old })

	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-1", nil), nil); err == nil {
		t.Error("an unwritable drop-in directory must refuse")
	}
}
