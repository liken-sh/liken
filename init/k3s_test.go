package main

// Tests for the cluster-derived k3s configuration: role, node
// address, and the drop-in's contents. k3s's on-disk state (the
// clusterState seeds, a demoted leader's datastore) is
// k3s_state_test.go's side of the split. Starting k3s itself runs
// only under QEMU. The derivations are pinned here.

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
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

func labCluster() *cluster.Cluster {
	return &cluster.Cluster{
		Metadata: api.ObjectMeta{Name: "lab"},
		Spec: cluster.ClusterSpec{
			Leaders:  []string{"node-1"},
			Endpoint: "https://10.10.0.1:6443",
			Network: cluster.ClusterNetworkSpec{
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

func haCluster() *cluster.Cluster {
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
	// The founder declares no static address (DHCP). The endpoint is
	// the one address that the deployment promised is reachable.
	clusterInit, joinURL := leaderJoinConfig(haCluster(), "node-3", t.TempDir())
	if clusterInit || joinURL != "https://10.10.0.1:6443" {
		t.Errorf("an unresolvable founder falls back to the endpoint: %v %q", clusterInit, joinURL)
	}
}

// adoptedCluster is an HA cluster whose datastore liken did not
// create. The endpoint points at the existing (foreign) control
// plane, and origin: Adopted says that no leader may initialize a
// new datastore.
func adoptedCluster() *cluster.Cluster {
	c := haCluster()
	c.Spec.Origin = cluster.OriginAdopted
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
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: haCluster(), nodeIP: "10.10.0.1", nodeInterface: "eth1", haveToken: true, clusterInit: true})
	if !strings.Contains(got, "cluster-init: true\n") {
		t.Errorf("the founding leader migrates to embedded etcd:\n%s", got)
	}
	if strings.Contains(got, "server:") {
		t.Errorf("the founding leader joins nothing:\n%s", got)
	}
}

func TestK3sBootConfigForAJoiningLeader(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: haCluster(), nodeIP: "10.10.0.3", nodeInterface: "eth1", haveToken: true, joinURL: "https://10.10.0.1:6443"})
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
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), nodeIP: "10.10.0.1", nodeInterface: "eth1", haveToken: true})
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
	got := k3sBootConfig(k3sBootInputs{role: api.RoleFollower, clusterDoc: labCluster(), nodeIP: "10.10.0.2", nodeInterface: "eth1", haveToken: true})
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
	// The address plan belongs to the control plane to declare. If a
	// follower told k3s about it, k3s would misread it as unknown
	// flags.
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
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: labCluster(), haveToken: true, nodeLabels: labels})
		// The + suffix asks k3s to append to the static file's list
		// instead of replacing it. Without it, the drop-in would erase
		// liken.sh/machine=true. Keys render sorted, so the drop-in is
		// deterministic for the same spec.
		want := "node-label+:\n  - guid.foo/gpu=true\n  - topology.kubernetes.io/zone=closet\n"
		if !strings.Contains(got, want) {
			t.Errorf("%s config should append the spec's node labels:\n%s", role, got)
		}
	}
}

func TestK3sBootConfigRendersNodeTaints(t *testing.T) {
	taints := []machine.NodeTaint{
		{Key: "node-role.kubernetes.io/database", Value: "primary", Effect: machine.TaintNoSchedule},
		{Key: "guid.foo/drill", Effect: machine.TaintPreferNoSchedule},
		{Key: "guid.foo/drill", Effect: machine.TaintNoExecute},
	}
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: labCluster(), haveToken: true, nodeTaints: taints})
		// A taint with no value renders key:Effect, and a taint with
		// one renders key=value:Effect, which is the grammar the
		// kubelet parses. Entries render sorted by key and then by
		// effect, so the drop-in is deterministic for the same spec.
		want := "node-taint+:\n" +
			"  - guid.foo/drill:NoExecute\n" +
			"  - guid.foo/drill:PreferNoSchedule\n" +
			"  - node-role.kubernetes.io/database=primary:NoSchedule\n"
		if !strings.Contains(got, want) {
			t.Errorf("%s config should append the spec's node taints:\n%s", role, got)
		}
	}
}

// kube-proxy runs on every node, so both roles carry the setting,
// and the drop-in always states it whole. The static config files
// name it nowhere, so this drop-in is its only author.
func TestK3sBootConfigRendersTheNodePortNetworks(t *testing.T) {
	cases := map[string]struct {
		cidrs []string
		want  string
	}{
		"unset means the node IP alone": {nil, "kube-proxy-arg:\n  - nodeport-addresses=primary\n"},
		"a declared list replaces it": {
			[]string{"10.10.0.0/24", "10.10.10.0/24"},
			"kube-proxy-arg:\n  - nodeport-addresses=10.10.0.0/24,10.10.10.0/24\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
				clusterDoc := labCluster()
				clusterDoc.Spec.Network.NodePortCIDRs = tc.cidrs
				got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: clusterDoc, haveToken: true})
				if !strings.Contains(got, tc.want) {
					t.Errorf("%s config should carry %q:\n%s", role, tc.want, got)
				}
			}
		})
	}
}

// imageGCCluster is the lab cluster with an image collection policy
// that names every field, so a test can watch all four reach the
// kubelet's configuration file.
func imageGCCluster() *cluster.Cluster {
	c := labCluster()
	high, low := 70, 60
	c.Spec.Runtime.Kubelet.ImageGC = cluster.ImageGCSpec{
		HighThresholdPercent: &high,
		LowThresholdPercent:  &low,
		MinimumAge:           "5m",
		MaximumAge:           "168h",
	}
	return c
}

// The configuration file carries only the fields the document names,
// under the kubelet's own field names, so nothing the cluster left
// unset gets an author here.
func TestKubeletImageGCSettingsRenderOnlyWhatIsNamed(t *testing.T) {
	high := 70
	cases := map[string]struct {
		spec cluster.ImageGCSpec
		want []string
	}{
		"nothing named": {cluster.ImageGCSpec{}, nil},
		"one threshold": {
			cluster.ImageGCSpec{HighThresholdPercent: &high},
			[]string{"imageGCHighThresholdPercent: 70"},
		},
		"the age ceiling alone": {
			cluster.ImageGCSpec{MaximumAge: "168h"},
			[]string{"imageMaximumGCAge: 168h"},
		},
		"every field": {
			imageGCCluster().Spec.Runtime.Kubelet.ImageGC,
			[]string{
				"imageGCHighThresholdPercent: 70",
				"imageGCLowThresholdPercent: 60",
				"imageMinimumGCAge: 5m",
				"imageMaximumGCAge: 168h",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := kubeletImageGCSettings(tc.spec); !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The file is a KubeletConfiguration document, because the age
// ceiling has no kubelet flag and exists only in this file.
func TestKubeletBootConfigIsAKubeletConfiguration(t *testing.T) {
	got := kubeletBootConfig([]string{"imageMaximumGCAge: 168h"})
	for _, want := range []string{
		"apiVersion: kubelet.config.k8s.io/v1beta1\n",
		"kind: KubeletConfiguration\n",
		"imageMaximumGCAge: 168h\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the kubelet configuration should carry %q:\n%s", want, got)
		}
	}
}

// A cluster that names no policy gets no file at all, so the kubelet
// keeps its whole default policy.
func TestKubeletBootConfigIsEmptyWhenNothingIsNamed(t *testing.T) {
	if got := kubeletBootConfig(nil); got != "" {
		t.Errorf("no named setting means no file:\n%s", got)
	}
}

// The kubelet runs on every node, so both roles name the file. k3s
// passes no image collection flag of its own, and a kubelet flag would
// beat the file, so the drop-in and the file have one author between
// them.
func TestK3sBootConfigNamesTheKubeletConfig(t *testing.T) {
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{
			role: role, clusterDoc: imageGCCluster(), haveToken: true,
			kubeletConfig: "/etc/rancher/k3s/kubelet.yaml",
		})
		want := "kubelet-arg:\n  - config=/etc/rancher/k3s/kubelet.yaml\n"
		if !strings.Contains(got, want) {
			t.Errorf("%s config should carry %q:\n%s", role, want, got)
		}
	}
}

func TestK3sBootConfigWithoutAKubeletConfigNamesNone(t *testing.T) {
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: labCluster(), haveToken: true})
		if strings.Contains(got, "kubelet-arg") {
			t.Errorf("%s: no policy means no kubelet-arg key:\n%s", role, got)
		}
	}
}

// debugCluster is the lab cluster with both log level knobs turned
// away from their defaults, so a test can watch each one reach the
// file that carries it.
func debugCluster() *cluster.Cluster {
	c := labCluster()
	c.Spec.Runtime.K3s.Debug = true
	c.Spec.Runtime.Containerd.LogLevel = "warn"
	return c
}

// A follower's components are as loud as a leader's, so both roles
// render the key.
func TestK3sBootConfigRendersTheDebugKey(t *testing.T) {
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: labCluster(), haveToken: true, debug: true})
		if !strings.Contains(got, "debug: true\n") {
			t.Errorf("%s config should carry the debug key:\n%s", role, got)
		}
	}
}

// A cluster that leaves debug unset renders the bytes it rendered
// before the field existed, so the upgrade that added it restarts no
// machine.
func TestK3sBootConfigWithoutDebugNamesNone(t *testing.T) {
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: labCluster(), haveToken: true})
		if strings.Contains(got, "debug:") {
			t.Errorf("%s: an unset field means no debug key:\n%s", role, got)
		}
	}
}

// The drop-in carries one table and the version that k3s renders. It
// restates nothing else from k3s's configuration, and it is not a
// template, so nothing here can break the file that starts containerd.
func TestContainerdLogLevelDropInCarriesOnlyTheLevel(t *testing.T) {
	got := containerdLogLevelDropIn("warn")
	for _, want := range []string{
		"version = 3\n",
		"[debug]\n",
		`level = "warn"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the drop-in should carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "{{") {
		t.Errorf("the drop-in is TOML, not a template:\n%s", got)
	}
}

func TestContainerdLogLevelDropInIsEmptyWhenNoLevelIsNamed(t *testing.T) {
	if got := containerdLogLevelDropIn(""); got != "" {
		t.Errorf("no named level means no drop-in:\n%s", got)
	}
}

// A machine with no cluster document is a leader of one, and it
// still states the setting rather than leaving kube-proxy on its own
// broad default.
func TestK3sBootConfigWithNoClusterStillNamesTheNodePortNetworks(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, haveToken: true})
	if !strings.Contains(got, "nodeport-addresses=primary") {
		t.Errorf("a machine alone answers NodePorts on its node IP:\n%s", got)
	}
}

// A declared list that omits the node network is legal and may even
// be deliberate, so the boot says so and carries on.
func TestUnreachableNodePortsWarning(t *testing.T) {
	cases := map[string]struct {
		cidrs  []string
		nodeIP string
		warns  bool
	}{
		"the node network is named":     {[]string{"10.10.0.0/24"}, "10.10.0.1", false},
		"one of several names it":       {[]string{"10.10.10.0/24", "10.10.0.0/24"}, "10.10.0.1", false},
		"only the tunnel is named":      {[]string{"10.10.10.0/24"}, "10.10.0.1", true},
		"nothing is named":              {nil, "10.10.0.1", false},
		"no node IP was derived":        {[]string{"10.10.10.0/24"}, "", false},
		"a single address names itself": {[]string{"10.10.0.1/32"}, "10.10.0.1", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			clusterDoc := labCluster()
			clusterDoc.Spec.Network.NodePortCIDRs = tc.cidrs
			got := unreachableNodePortsWarning(clusterDoc, tc.nodeIP)
			if (got != "") != tc.warns {
				t.Errorf("warning %q, want warns=%v", got, tc.warns)
			}
		})
	}
}

func TestUnreachableNodePortsWarningWithNoCluster(t *testing.T) {
	if got := unreachableNodePortsWarning(nil, "10.10.0.1"); got != "" {
		t.Errorf("a machine alone declares no list: %q", got)
	}
}

func TestK3sBootConfigWithoutNodeLabelsRendersNone(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if strings.Contains(got, "node-label") {
		t.Errorf("no declared labels means no node-label key:\n%s", got)
	}
}

func TestK3sBootConfigWithoutNodeTaintsRendersNone(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if strings.Contains(got, "node-taint") {
		t.Errorf("no declared taints means no node-taint key:\n%s", got)
	}
}

func TestK3sBootConfigWithNoClusterIsNearlyEmpty(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, haveToken: true})
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
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if !strings.Contains(got, "disable:\n  - metrics-server\n  - servicelb\n  - traefik\n") {
		t.Errorf("a cluster with no features disables everything bundled:\n%s", got)
	}
}

func TestK3sBootConfigLeavesOptedInComponentsOffTheDisableList(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"metrics-server": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if !strings.Contains(got, "disable:\n  - servicelb\n  - traefik\n") {
		t.Errorf("an opt-in leaves the disable list:\n%s", got)
	}
	if strings.Contains(got, "metrics-server") {
		t.Errorf("an opted-in component should not appear at all:\n%s", got)
	}
}

func TestK3sBootConfigWithEveryFeatureRendersNoDisableList(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"traefik": {}, "servicelb": {}, "metrics-server": {}, "network-policy": {},
	}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
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
		"no cluster":  {role: api.RoleLeader, haveToken: true},
		"no features": {role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true},
	} {
		if got := k3sBootConfig(in); !strings.Contains(got, "disable-helm-controller: true\n") {
			t.Errorf("%s: the helm controller is an opt-in:\n%s", name, got)
		}
	}
}

func TestK3sBootConfigHelmFeatureKeepsTheHelmController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"helm": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable-helm-controller") {
		t.Errorf("declaring helm keeps the controller:\n%s", got)
	}
}

func TestK3sBootConfigTraefikImpliesTheHelmController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable-helm-controller") {
		t.Errorf("traefik requires helm, so the controller stays:\n%s", got)
	}
}

func TestK3sBootConfigDisablesTheCloudControllerByDefault(t *testing.T) {
	for name, in := range map[string]k3sBootInputs{
		"no cluster":  {role: api.RoleLeader, haveToken: true},
		"no features": {role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true},
	} {
		if got := k3sBootConfig(in); !strings.Contains(got, "disable-cloud-controller: true\n") {
			t.Errorf("%s: the embedded cloud controller runs only for servicelb:\n%s", name, got)
		}
	}
}

func TestK3sBootConfigDisablesNetworkPolicyByDefault(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if !strings.Contains(got, "disable-network-policy: true\n") {
		t.Errorf("network policy enforcement is an opt-in:\n%s", got)
	}
}

func TestK3sBootConfigNetworkPolicyFeatureKeepsTheController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"network-policy": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable-network-policy") {
		t.Errorf("declaring network-policy keeps the controller:\n%s", got)
	}
}

func TestK3sBootConfigServiceLBKeepsTheCloudController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"servicelb": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable-cloud-controller") {
		t.Errorf("servicelb runs inside the cloud controller, so it must stay:\n%s", got)
	}
}

func TestK3sBootConfigFollowersNeverRenderTheDisableList(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleFollower, clusterDoc: labCluster(), haveToken: true})
	if strings.Contains(got, "disable") {
		t.Errorf("disable is a server-side key an agent would refuse:\n%s", got)
	}
}

func TestK3sBootConfigEmbeddedRegistryOnLeaders(t *testing.T) {
	c := labCluster()
	c.Spec.Registries.Embedded = true
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if !strings.Contains(got, "embedded-registry: true\n") {
		t.Errorf("an embedded opt-in renders the server key:\n%s", got)
	}
}

func TestK3sBootConfigEmbeddedRegistryOffByDefault(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if strings.Contains(got, "embedded-registry") {
		t.Errorf("the embedded registry is an opt-in:\n%s", got)
	}
}

func TestK3sBootConfigFollowersNeverRenderEmbeddedRegistry(t *testing.T) {
	c := labCluster()
	c.Spec.Registries.Embedded = true
	got := k3sBootConfig(k3sBootInputs{role: api.RoleFollower, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "embedded-registry") {
		t.Errorf("embedded-registry is a server-side key an agent would refuse:\n%s", got)
	}
}

func TestK3sBootConfigWithoutAToken(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), nodeIP: "10.10.0.1", nodeInterface: "eth1"})
	if strings.Contains(got, "token-file") {
		t.Errorf("no token file means no token-file entry:\n%s", got)
	}
}

// fakeK3sConfigs points the drop-in writers at a temporary directory
// that substitutes for /etc/rancher/k3s, and the token path at a
// temporary file (present or not). It restores the real paths when
// the test ends.
func fakeK3sConfigs(t *testing.T, withToken bool) (serverDropIns, agentDropIns string) {
	t.Helper()
	dir := t.TempDir()
	oldServer, oldAgent, oldKubelet, oldCopy := k3sServerConfig, k3sAgentConfig, k3sKubeletConfig, k3sKubeletConfigCopy
	oldDropIn, oldToken := k3sContainerdDropIn, tokenPath
	k3sServerConfig = filepath.Join(dir, "config.yaml")
	k3sAgentConfig = filepath.Join(dir, "agent.yaml")
	k3sKubeletConfig = filepath.Join(dir, "kubelet.yaml")
	k3sKubeletConfigCopy = filepath.Join(dir, "kubelet.conf.d", "10-cli-config.conf")
	k3sContainerdDropIn = filepath.Join(dir, "containerd", "config-v3.toml.d", "10-liken-log-level.toml")
	tokenPath = filepath.Join(dir, "token")
	t.Cleanup(func() {
		k3sServerConfig, k3sAgentConfig, k3sKubeletConfig, k3sKubeletConfigCopy = oldServer, oldAgent, oldKubelet, oldCopy
		k3sContainerdDropIn, tokenPath = oldDropIn, oldToken
	})
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
		Metadata: api.ObjectMeta{Name: name},
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
	if role != api.RoleLeader {
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
	clusterDoc := labCluster()
	conns := []*connection{conn(t, "eth1", "10.10.0.2/24")}

	role, err := writeK3sBootConfig(clusterDoc, bootMachine("node-2", nil), conns)
	if err != nil {
		t.Fatal(err)
	}
	if role != api.RoleFollower {
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
	clusterDoc := labCluster()
	clusterDoc.Spec.Endpoint = ""
	if _, err := writeK3sBootConfig(clusterDoc, bootMachine("node-2", nil), nil); err == nil {
		t.Error("a follower with nowhere to join must refuse")
	}
}

func TestWriteK3sBootConfigRefusesAFollowerWithoutAToken(t *testing.T) {
	fakeK3sConfigs(t, false)
	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-2", nil), nil); err == nil {
		t.Error("a follower with no join token can never register")
	}
}

// Both roles write the same file and name it the same way, because
// the kubelet runs on every node of the cluster.
func TestWriteK3sBootConfigWritesTheKubeletConfig(t *testing.T) {
	cases := map[string]struct {
		name string
		role api.Role
	}{
		"a leader":   {"node-1", api.RoleLeader},
		"a follower": {"node-2", api.RoleFollower},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fakeK3sConfigs(t, true)
			conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

			role, err := writeK3sBootConfig(imageGCCluster(), bootMachine(tc.name, nil), conns)
			if err != nil {
				t.Fatal(err)
			}
			if role != tc.role {
				t.Fatalf("got role %s, want %s", role, tc.role)
			}
			base := k3sServerConfig
			if role == api.RoleFollower {
				base = k3sAgentConfig
			}
			dropIn, err := os.ReadFile(filepath.Join(base+".d", "boot.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			want := "kubelet-arg:\n  - config=" + k3sKubeletConfig + "\n"
			if !strings.Contains(string(dropIn), want) {
				t.Errorf("the drop-in should name the kubelet's file:\n%s", dropIn)
			}
			written, err := os.ReadFile(k3sKubeletConfig)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range []string{
				"kind: KubeletConfiguration\n",
				"imageGCHighThresholdPercent: 70\n",
				"imageGCLowThresholdPercent: 60\n",
				"imageMinimumGCAge: 5m\n",
				"imageMaximumGCAge: 168h\n",
			} {
				if !strings.Contains(string(written), line) {
					t.Errorf("the kubelet configuration should carry %q:\n%s", line, written)
				}
			}
		})
	}
}

// A cluster that names no policy renders exactly what it rendered
// before the section existed: no file, and no key in the drop-in. This
// is what keeps an upgrade from restarting k3s on every machine of
// every cluster that never asked for a policy.
func TestWriteK3sBootConfigWithoutAPolicyWritesNoKubeletConfig(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-1", nil), conns); err != nil {
		t.Fatal(err)
	}
	dropIn, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dropIn), "kubelet-arg") {
		t.Errorf("no policy means no kubelet-arg key:\n%s", dropIn)
	}
	if _, err := os.Stat(k3sKubeletConfig); err == nil {
		t.Error("no policy means no kubelet configuration file")
	}
}

// The restart path runs writeK3sBootConfig again without a reboot, so
// a cluster that retracts the section has to take its file away too.
// A file left on disk would describe a policy the kubelet no longer
// runs.
func TestWriteK3sBootConfigRetractsTheKubeletConfig(t *testing.T) {
	fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}
	machineDoc := bootMachine("node-1", nil)

	if _, err := writeK3sBootConfig(imageGCCluster(), machineDoc, conns); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(k3sKubeletConfig); err != nil {
		t.Fatalf("the first boot writes the file: %v", err)
	}

	// When k3s starts with the config argument, it copies the named
	// file into the drop-in directory it hands the kubelet, and that
	// directory persists on clusterState. The test plants that copy the
	// way k3s leaves it.
	if err := os.MkdirAll(filepath.Dir(k3sKubeletConfigCopy), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(k3sKubeletConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(k3sKubeletConfigCopy, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := writeK3sBootConfig(labCluster(), machineDoc, conns); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(k3sKubeletConfig); err == nil {
		t.Error("a retracted section takes its file away")
	}
	if _, err := os.Stat(k3sKubeletConfigCopy); err == nil {
		t.Error("a retracted section takes k3s's copy away too; k3s refreshes the copy only when the argument is present, so a stale copy would keep the policy running forever")
	}
}

// containerd runs on every node, so both roles write the drop-in, and
// the k3s drop-in carries k3s's own debug key beside it.
func TestWriteK3sBootConfigWritesTheLogLevels(t *testing.T) {
	cases := map[string]struct {
		name string
		role api.Role
	}{
		"a leader":   {"node-1", api.RoleLeader},
		"a follower": {"node-2", api.RoleFollower},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fakeK3sConfigs(t, true)
			conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

			role, err := writeK3sBootConfig(debugCluster(), bootMachine(tc.name, nil), conns)
			if err != nil {
				t.Fatal(err)
			}
			if role != tc.role {
				t.Fatalf("got role %s, want %s", role, tc.role)
			}
			base := k3sServerConfig
			if role == api.RoleFollower {
				base = k3sAgentConfig
			}
			bootDropIn, err := os.ReadFile(filepath.Join(base+".d", "boot.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(bootDropIn), "debug: true\n") {
				t.Errorf("the k3s drop-in should carry k3s's debug key:\n%s", bootDropIn)
			}
			written, err := os.ReadFile(k3sContainerdDropIn)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(written), `level = "warn"`) {
				t.Errorf("the containerd drop-in should carry the cluster's level:\n%s", written)
			}
		})
	}
}

// A cluster that names neither knob renders what it rendered before
// the fields existed: no debug key, and no containerd drop-in.
func TestWriteK3sBootConfigWithoutLevelsWritesNeither(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-1", nil), conns); err != nil {
		t.Fatal(err)
	}
	bootDropIn, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bootDropIn), "debug:") {
		t.Errorf("an unset field means no debug key:\n%s", bootDropIn)
	}
	if _, err := os.Stat(k3sContainerdDropIn); err == nil {
		t.Error("no named level means no containerd drop-in")
	}
}

// The drop-in sits on clusterState, and containerd imports whatever the
// directory holds on every start. A cluster that stops naming a level
// must have the drop-in removed, or containerd would keep the retracted
// level. The retraction takes liken's own file and leaves the
// operator's, because the directory is a place both of them write.
func TestWriteK3sBootConfigRetractsTheContainerdDropIn(t *testing.T) {
	fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}
	machineDoc := bootMachine("node-1", nil)

	if _, err := writeK3sBootConfig(debugCluster(), machineDoc, conns); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(k3sContainerdDropIn); err != nil {
		t.Fatalf("the first boot writes the drop-in: %v", err)
	}

	operators := filepath.Join(filepath.Dir(k3sContainerdDropIn), "20-operator.toml")
	if err := os.WriteFile(operators, []byte("version = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := writeK3sBootConfig(labCluster(), machineDoc, conns); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(k3sContainerdDropIn); err == nil {
		t.Error("a retracted level takes liken's drop-in away")
	}
	if _, err := os.Stat(operators); err != nil {
		t.Errorf("a retraction must leave every other file in the directory: %v", err)
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
	if err != nil || role != api.RoleLeader {
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
	if err != nil || role != api.RoleLeader {
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
	// The follower's one address is outside the cluster's nodeCIDR.
	// The warning prints, and k3s is left to guess.
	conns := []*connection{conn(t, "eth0", "192.168.1.5/24")}

	role, err := writeK3sBootConfig(labCluster(), bootMachine("node-2", nil), conns)
	if err != nil || role != api.RoleFollower {
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
	// directory cannot be created.
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
