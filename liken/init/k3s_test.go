package main

// Tests for the k3s configuration that init derives from the cluster:
// the role, the node address, and the contents of the drop-in. The
// state that init prepares on disk (the clusterState seed files and a
// demoted leader's datastore) is tested in k3s_state_test.go. Starting
// k3s itself is only tested under QEMU.

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// conn builds a connection with an interface name and an address given
// in CIDR form, which is all that the address derivation reads.
func conn(t *testing.T, ifname, cidr string) *connection {
	t.Helper()
	ip, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("parsing %q: %v", cidr, err)
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
		t.Errorf("the node address is %s on %s, want 10.10.0.2 on eth1", ip, ifname)
	}
}

func TestNodeAddressWithoutAClusterIsEmpty(t *testing.T) {
	conns := []*connection{conn(t, "eth0", "10.0.2.15/24")}
	if ip, ifname := nodeAddress(nil, conns); ip != "" || ifname != "" {
		t.Errorf("nodeAddress returned %q on %q, want none with no cluster", ip, ifname)
	}
}

func TestNodeAddressOutsideTheNodeCIDRIsEmpty(t *testing.T) {
	conns := []*connection{conn(t, "eth0", "10.0.2.15/24")}
	if ip, ifname := nodeAddress(labCluster(), conns); ip != "" || ifname != "" {
		t.Errorf("nodeAddress returned %q on %q, want none when no address is in the nodeCIDR", ip, ifname)
	}
}

func TestLeaderJoinConfigWithOneLeaderStaysAlone(t *testing.T) {
	clusterInit, joinURL := leaderJoinConfig(labCluster(), "node-1", t.TempDir())
	if clusterInit || joinURL != "" {
		t.Errorf("clusterInit is %v and the join URL is %q, want false and none for a single leader, which uses sqlite", clusterInit, joinURL)
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
		t.Errorf("clusterInit is %v and the join URL is %q, want true and none for the founding leader", clusterInit, joinURL)
	}
}

func TestLeaderJoinConfigForAJoiningLeader(t *testing.T) {
	dir := manifestsDir(t, map[string]string{"node-1": "10.10.0.1/24"})
	clusterInit, joinURL := leaderJoinConfig(haCluster(), "node-3", dir)
	if clusterInit || joinURL != "https://10.10.0.1:6443" {
		t.Errorf("clusterInit is %v and the join URL is %q, want false and the founder's https://10.10.0.1:6443 for a joining leader", clusterInit, joinURL)
	}
}

func TestLeaderJoinConfigFallsBackToTheEndpoint(t *testing.T) {
	// The founder has no manifest with a static address, so the joining
	// leader joins spec.endpoint.
	clusterInit, joinURL := leaderJoinConfig(haCluster(), "node-3", t.TempDir())
	if clusterInit || joinURL != "https://10.10.0.1:6443" {
		t.Errorf("clusterInit is %v and the join URL is %q, want false and the endpoint when the founder does not resolve", clusterInit, joinURL)
	}
}

// adoptedCluster is a cluster of several leaders whose datastore liken
// did not create. The endpoint is the existing control plane.
func adoptedCluster() *cluster.Cluster {
	c := haCluster()
	c.Spec.Origin = cluster.OriginAdopted
	c.Spec.Endpoint = "https://10.10.0.250:6443"
	return c
}

func TestLeaderJoinConfigAdoptedFounderJoinsTheEndpoint(t *testing.T) {
	clusterInit, joinURL := leaderJoinConfig(adoptedCluster(), "node-1", t.TempDir())
	if clusterInit || joinURL != "https://10.10.0.250:6443" {
		t.Errorf("clusterInit is %v and the join URL is %q, want false and the existing datastore's endpoint for an adopted cluster's founding leader", clusterInit, joinURL)
	}
}

func TestLeaderJoinConfigAdoptedSingleLeaderStillJoins(t *testing.T) {
	c := adoptedCluster()
	c.Spec.Leaders = []string{"node-1"}
	clusterInit, joinURL := leaderJoinConfig(c, "node-1", t.TempDir())
	if clusterInit || joinURL != "https://10.10.0.250:6443" {
		t.Errorf("clusterInit is %v and the join URL is %q, want false and the existing datastore's endpoint for a lone leader of an adopted cluster", clusterInit, joinURL)
	}
}

func TestLeaderJoinConfigAdoptedJoiningLeaderPrefersTheFounder(t *testing.T) {
	dir := manifestsDir(t, map[string]string{"node-1": "10.10.0.1/24"})
	clusterInit, joinURL := leaderJoinConfig(adoptedCluster(), "node-3", dir)
	if clusterInit || joinURL != "https://10.10.0.1:6443" {
		t.Errorf("clusterInit is %v and the join URL is %q, want false and the founder's URL for a joining leader of an adopted cluster", clusterInit, joinURL)
	}
}

func TestK3sBootConfigForTheFoundingLeader(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: haCluster(), nodeIP: "10.10.0.1", nodeInterface: "eth1", haveToken: true, clusterInit: true})
	if !strings.Contains(got, "cluster-init: true\n") {
		t.Errorf("the founding leader's config has no cluster-init: true, want it:\n%s", got)
	}
	if strings.Contains(got, "server:") {
		t.Errorf("the founding leader's config has a server: key, want none:\n%s", got)
	}
}

func TestK3sBootConfigForAJoiningLeader(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: haCluster(), nodeIP: "10.10.0.3", nodeInterface: "eth1", haveToken: true, joinURL: "https://10.10.0.1:6443"})
	if !strings.Contains(got, "server: https://10.10.0.1:6443\n") {
		t.Errorf("the joining leader's config has no server: https://10.10.0.1:6443, want it:\n%s", got)
	}
	if strings.Contains(got, "cluster-init") {
		t.Errorf("the joining leader's config has cluster-init, want it only on the founding leader:\n%s", got)
	}
	// Every leader renders the cluster's CIDRs.
	if !strings.Contains(got, "cluster-cidr: 10.42.0.0/16\n") {
		t.Errorf("the joining leader's config has no cluster-cidr, want the cluster's CIDRs on every leader:\n%s", got)
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
			t.Errorf("leader config does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "server:") {
		t.Errorf("the leader's config has a server: key, want none:\n%s", got)
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
			t.Errorf("follower config does not contain %q:\n%s", want, got)
		}
	}
	// A k3s agent refuses these keys.
	for _, reject := range []string{"cluster-cidr", "service-cidr", "cluster-dns", "cluster-domain"} {
		if strings.Contains(got, reject) {
			t.Errorf("follower config contains %s, want it absent:\n%s", reject, got)
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
		want := "node-label+:\n  - guid.foo/gpu=true\n  - topology.kubernetes.io/zone=closet\n"
		if !strings.Contains(got, want) {
			t.Errorf("the %s config does not append the spec's node labels, want %q:\n%s", role, want, got)
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
		want := "node-taint+:\n" +
			"  - guid.foo/drill:NoExecute\n" +
			"  - guid.foo/drill:PreferNoSchedule\n" +
			"  - node-role.kubernetes.io/database=primary:NoSchedule\n"
		if !strings.Contains(got, want) {
			t.Errorf("the %s config does not append the spec's node taints, want %q:\n%s", role, want, got)
		}
	}
}

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
					t.Errorf("%s config does not contain %q:\n%s", role, tc.want, got)
				}
			}
		})
	}
}

// imageGCCluster is the lab cluster with every image garbage
// collection field set.
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

// A field that the cluster leaves unset is not written, so the kubelet
// keeps its default.
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
				t.Errorf("the result is %v, want %v", got, tc.want)
			}
		})
	}
}

func TestKubeletBootConfigIsAKubeletConfiguration(t *testing.T) {
	got := kubeletBootConfig([]string{"imageMaximumGCAge: 168h"})
	for _, want := range []string{
		"apiVersion: kubelet.config.k8s.io/v1beta1\n",
		"kind: KubeletConfiguration\n",
		"imageMaximumGCAge: 168h\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the kubelet configuration does not contain %q:\n%s", want, got)
		}
	}
}

func TestKubeletBootConfigIsEmptyWhenNothingIsNamed(t *testing.T) {
	if got := kubeletBootConfig(nil); got != "" {
		t.Errorf("kubeletBootConfig(nil) is %q, want empty when no setting is set", got)
	}
}

func TestK3sBootConfigNamesTheKubeletConfig(t *testing.T) {
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{
			role: role, clusterDoc: imageGCCluster(), haveToken: true,
			kubeletConfig: "/etc/rancher/k3s/kubelet.yaml",
		})
		want := "kubelet-arg:\n  - config=/etc/rancher/k3s/kubelet.yaml\n"
		if !strings.Contains(got, want) {
			t.Errorf("%s config does not contain %q:\n%s", role, want, got)
		}
	}
}

func TestK3sBootConfigWithoutAKubeletConfigNamesNone(t *testing.T) {
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: labCluster(), haveToken: true})
		if strings.Contains(got, "kubelet-arg") {
			t.Errorf("the %s config has a kubelet-arg key, want none with no policy:\n%s", role, got)
		}
	}
}

// debugCluster is the lab cluster with k3s's debug flag and a
// containerd log level set.
func debugCluster() *cluster.Cluster {
	c := labCluster()
	c.Spec.Runtime.K3s.Debug = true
	c.Spec.Runtime.Containerd.LogLevel = "warn"
	return c
}

func TestK3sBootConfigRendersTheDebugKey(t *testing.T) {
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: labCluster(), haveToken: true, debug: true})
		if !strings.Contains(got, "debug: true\n") {
			t.Errorf("the %s config has no debug: true, want it:\n%s", role, got)
		}
	}
}

func TestK3sBootConfigWithoutDebugNamesNone(t *testing.T) {
	for _, role := range []api.Role{api.RoleLeader, api.RoleFollower} {
		got := k3sBootConfig(k3sBootInputs{role: role, clusterDoc: labCluster(), haveToken: true})
		if strings.Contains(got, "debug:") {
			t.Errorf("the %s config has a debug key, want none when the field is unset:\n%s", role, got)
		}
	}
}

// The drop-in sets only the level, so it cannot break the
// configuration that k3s renders for containerd.
func TestContainerdLogLevelDropInSetsOnlyTheLevel(t *testing.T) {
	got := containerdLogLevelDropIn("warn")
	for _, want := range []string{
		"version = 3\n",
		"[debug]\n",
		`level = "warn"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the drop-in does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "{{") {
		t.Errorf("the drop-in has template markers {{, want TOML:\n%s", got)
	}
}

func TestContainerdLogLevelDropInIsEmptyWhenNoLevelIsNamed(t *testing.T) {
	if got := containerdLogLevelDropIn(""); got != "" {
		t.Errorf("containerdLogLevelDropIn(\"\") is %q, want empty when no level is set", got)
	}
}

// A machine with no cluster document still renders the setting,
// because kube-proxy's own default is every local address.
func TestK3sBootConfigWithNoClusterStillNamesTheNodePortNetworks(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, haveToken: true})
	if !strings.Contains(got, "nodeport-addresses=primary") {
		t.Errorf("the config has no nodeport-addresses=primary, want it:\n%s", got)
	}
}

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
				t.Errorf("the warning is %q, want a warning: %v", got, tc.warns)
			}
		})
	}
}

func TestUnreachableNodePortsWarningWithNoCluster(t *testing.T) {
	if got := unreachableNodePortsWarning(nil, "10.10.0.1"); got != "" {
		t.Errorf("unreachableNodePortsWarning(nil) is %q, want empty with no cluster", got)
	}
}

func TestK3sBootConfigWithoutNodeLabelsRendersNone(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if strings.Contains(got, "node-label") {
		t.Errorf("the config has a node-label key, want none with no declared labels:\n%s", got)
	}
}

func TestK3sBootConfigWithoutNodeTaintsRendersNone(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if strings.Contains(got, "node-taint") {
		t.Errorf("the config has a node-taint key, want none with no declared taints:\n%s", got)
	}
}

func TestK3sBootConfigWithNoClusterIsNearlyEmpty(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, haveToken: true})
	if !strings.Contains(got, "token-file:") {
		t.Errorf("the config has no token-file:, want one even with no cluster document:\n%s", got)
	}
	for _, reject := range []string{"cluster-cidr", "node-ip", "server:"} {
		if strings.Contains(got, reject) {
			t.Errorf("the config of a machine with no cluster has %s, want none:\n%s", reject, got)
		}
	}
	if !strings.Contains(got, "disable:\n  - metrics-server\n  - servicelb\n  - traefik\n") {
		t.Errorf("the config of a machine with no cluster does not disable metrics-server, servicelb, and traefik, want all three disabled:\n%s", got)
	}
}

func TestK3sBootConfigDisablesEverythingBundledByDefault(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if !strings.Contains(got, "disable:\n  - metrics-server\n  - servicelb\n  - traefik\n") {
		t.Errorf("the config does not disable metrics-server, servicelb, and traefik, want all three disabled with no features:\n%s", got)
	}
}

func TestK3sBootConfigLeavesOptedInComponentsOffTheDisableList(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"metrics-server": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if !strings.Contains(got, "disable:\n  - servicelb\n  - traefik\n") {
		t.Errorf("the disable list is not servicelb and traefik, want metrics-server removed from it by its feature:\n%s", got)
	}
	if strings.Contains(got, "metrics-server") {
		t.Errorf("the config names metrics-server, want it absent when its feature is enabled:\n%s", got)
	}
}

func TestK3sBootConfigWithEveryFeatureRendersNoDisableList(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"traefik": {}, "servicelb": {}, "metrics-server": {}, "network-policy": {},
	}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable") {
		t.Errorf("the config has a disable key, want none when every feature is enabled:\n%s", got)
	}
}

func TestK3sBootConfigDisablesTheHelmControllerByDefault(t *testing.T) {
	for name, in := range map[string]k3sBootInputs{
		"no cluster":  {role: api.RoleLeader, haveToken: true},
		"no features": {role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true},
	} {
		if got := k3sBootConfig(in); !strings.Contains(got, "disable-helm-controller: true\n") {
			t.Errorf("%s: the config has no disable-helm-controller: true, want it when neither helm nor traefik is enabled:\n%s", name, got)
		}
	}
}

func TestK3sBootConfigHelmFeatureKeepsTheHelmController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"helm": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable-helm-controller") {
		t.Errorf("the config has disable-helm-controller, want none when helm is enabled:\n%s", got)
	}
}

func TestK3sBootConfigTraefikImpliesTheHelmController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable-helm-controller") {
		t.Errorf("the config has disable-helm-controller, want none because traefik needs helm:\n%s", got)
	}
}

func TestK3sBootConfigDisablesTheCloudControllerByDefault(t *testing.T) {
	for name, in := range map[string]k3sBootInputs{
		"no cluster":  {role: api.RoleLeader, haveToken: true},
		"no features": {role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true},
	} {
		if got := k3sBootConfig(in); !strings.Contains(got, "disable-cloud-controller: true\n") {
			t.Errorf("%s: the config has no disable-cloud-controller: true, want it unless servicelb is enabled:\n%s", name, got)
		}
	}
}

func TestK3sBootConfigDisablesNetworkPolicyByDefault(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if !strings.Contains(got, "disable-network-policy: true\n") {
		t.Errorf("the config has no disable-network-policy: true, want it unless network-policy is enabled:\n%s", got)
	}
}

func TestK3sBootConfigNetworkPolicyFeatureKeepsTheController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"network-policy": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable-network-policy") {
		t.Errorf("the config has disable-network-policy, want none when network-policy is enabled:\n%s", got)
	}
}

func TestK3sBootConfigServiceLBKeepsTheCloudController(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"servicelb": {}}
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "disable-cloud-controller") {
		t.Errorf("the config has disable-cloud-controller, want none because servicelb runs in the cloud controller:\n%s", got)
	}
}

func TestK3sBootConfigFollowersNeverRenderTheDisableList(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleFollower, clusterDoc: labCluster(), haveToken: true})
	if strings.Contains(got, "disable") {
		t.Errorf("the follower's config has a disable key, want none because k3s agent refuses it:\n%s", got)
	}
}

func TestK3sBootConfigEmbeddedRegistryOnLeaders(t *testing.T) {
	c := labCluster()
	c.Spec.Registries.Embedded = true
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: c, haveToken: true})
	if !strings.Contains(got, "embedded-registry: true\n") {
		t.Errorf("the config has no embedded-registry: true, want it when the feature is enabled:\n%s", got)
	}
}

func TestK3sBootConfigEmbeddedRegistryOffByDefault(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), haveToken: true})
	if strings.Contains(got, "embedded-registry") {
		t.Errorf("the config has embedded-registry, want none unless the feature is enabled:\n%s", got)
	}
}

func TestK3sBootConfigFollowersNeverRenderEmbeddedRegistry(t *testing.T) {
	c := labCluster()
	c.Spec.Registries.Embedded = true
	got := k3sBootConfig(k3sBootInputs{role: api.RoleFollower, clusterDoc: c, haveToken: true})
	if strings.Contains(got, "embedded-registry") {
		t.Errorf("the follower's config has embedded-registry, want none because k3s agent refuses it:\n%s", got)
	}
}

func TestK3sBootConfigWithoutAToken(t *testing.T) {
	got := k3sBootConfig(k3sBootInputs{role: api.RoleLeader, clusterDoc: labCluster(), nodeIP: "10.10.0.1", nodeInterface: "eth1"})
	if strings.Contains(got, "token-file") {
		t.Errorf("the config has a token-file key, want none with no token file:\n%s", got)
	}
}

// fakeK3sConfigs points every path that writeK3sBootConfig writes or
// reads at a temporary directory. The token file exists only when
// withToken is true.
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
			t.Fatalf("writing the token file: %v", err)
		}
	}
	return k3sServerConfig + ".d", k3sAgentConfig + ".d"
}

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
		t.Fatalf("writing the boot config of node-1: %v", err)
	}
	if role != api.RoleLeader {
		t.Errorf("the role is %s, want leader for node-1, which spec.leaders lists", role)
	}
	raw, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatalf("reading the leader boot.yaml: %v", err)
	}
	content := string(raw)
	for _, want := range []string{"token-file:", "cluster-cidr: 10.42.0.0/16", "node-ip: 10.10.0.1", "flannel-iface: eth1", "node-label+:\n  - guid.foo/gpu=true"} {
		if !strings.Contains(content, want) {
			t.Errorf("the leader drop-in does not contain %q:\n%s", want, content)
		}
	}
}

func TestWriteK3sBootConfigForAFollower(t *testing.T) {
	_, agentDropIns := fakeK3sConfigs(t, true)
	clusterDoc := labCluster()
	conns := []*connection{conn(t, "eth1", "10.10.0.2/24")}

	role, err := writeK3sBootConfig(clusterDoc, bootMachine("node-2", nil), conns)
	if err != nil {
		t.Fatalf("writing the boot config of node-2: %v", err)
	}
	if role != api.RoleFollower {
		t.Errorf("the role is %s, want follower for node-2, which spec.leaders does not list", role)
	}
	raw, err := os.ReadFile(filepath.Join(agentDropIns, "boot.yaml"))
	if err != nil {
		t.Fatalf("reading the follower boot.yaml: %v", err)
	}
	if !strings.Contains(string(raw), "server: https://10.10.0.1:6443") {
		t.Errorf("the follower's config has no server: https://10.10.0.1:6443, want the endpoint:\n%s", raw)
	}
}

func TestWriteK3sBootConfigRefusesAFollowerWithoutAnEndpoint(t *testing.T) {
	fakeK3sConfigs(t, true)
	clusterDoc := labCluster()
	clusterDoc.Spec.Endpoint = ""
	if _, err := writeK3sBootConfig(clusterDoc, bootMachine("node-2", nil), nil); err == nil {
		t.Error("no error for a follower of a cluster with no endpoint, want one")
	}
}

func TestWriteK3sBootConfigRefusesAFollowerWithoutAToken(t *testing.T) {
	fakeK3sConfigs(t, false)
	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-2", nil), nil); err == nil {
		t.Error("no error for a follower with no join token, want one")
	}
}

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
				t.Fatalf("writing the boot config: %v", err)
			}
			if role != tc.role {
				t.Fatalf("the role is %s, want %s", role, tc.role)
			}
			base := k3sServerConfig
			if role == api.RoleFollower {
				base = k3sAgentConfig
			}
			dropIn, err := os.ReadFile(filepath.Join(base+".d", "boot.yaml"))
			if err != nil {
				t.Fatalf("reading boot.yaml: %v", err)
			}
			want := "kubelet-arg:\n  - config=" + k3sKubeletConfig + "\n"
			if !strings.Contains(string(dropIn), want) {
				t.Errorf("the drop-in does not name the kubelet's file, want %q:\n%s", want, dropIn)
			}
			written, err := os.ReadFile(k3sKubeletConfig)
			if err != nil {
				t.Fatalf("reading the kubelet configuration: %v", err)
			}
			for _, line := range []string{
				"kind: KubeletConfiguration\n",
				"imageGCHighThresholdPercent: 70\n",
				"imageGCLowThresholdPercent: 60\n",
				"imageMinimumGCAge: 5m\n",
				"imageMaximumGCAge: 168h\n",
			} {
				if !strings.Contains(string(written), line) {
					t.Errorf("the kubelet configuration does not contain %q, want it:\n%s", line, written)
				}
			}
		})
	}
}

func TestWriteK3sBootConfigWithoutAPolicyWritesNoKubeletConfig(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-1", nil), conns); err != nil {
		t.Fatalf("writing the boot config: %v", err)
	}
	dropIn, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatalf("reading boot.yaml: %v", err)
	}
	if strings.Contains(string(dropIn), "kubelet-arg") {
		t.Errorf("the drop-in has a kubelet-arg key, want none with no policy:\n%s", dropIn)
	}
	if _, err := os.Stat(k3sKubeletConfig); err == nil {
		t.Errorf("the kubelet configuration file %s exists, want none with no policy", k3sKubeletConfig)
	}
}

// The restart path calls writeK3sBootConfig again without a reboot, so
// the files from the earlier call are still on disk.
func TestWriteK3sBootConfigRetractsTheKubeletConfig(t *testing.T) {
	fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}
	machineDoc := bootMachine("node-1", nil)

	if _, err := writeK3sBootConfig(imageGCCluster(), machineDoc, conns); err != nil {
		t.Fatalf("writing the boot config: %v", err)
	}
	if _, err := os.Stat(k3sKubeletConfig); err != nil {
		t.Fatalf("the first boot writes the file: %v", err)
	}

	// Make the copy that k3s makes when it starts with kubelet-arg
	// config=.
	if err := os.MkdirAll(filepath.Dir(k3sKubeletConfigCopy), 0o755); err != nil {
		t.Fatalf("creating the directory of the kubelet configuration copy: %v", err)
	}
	content, err := os.ReadFile(k3sKubeletConfig)
	if err != nil {
		t.Fatalf("reading the kubelet configuration: %v", err)
	}
	if err := os.WriteFile(k3sKubeletConfigCopy, content, 0o600); err != nil {
		t.Fatalf("writing the kubelet configuration copy: %v", err)
	}

	if _, err := writeK3sBootConfig(labCluster(), machineDoc, conns); err != nil {
		t.Fatalf("writing the boot config again: %v", err)
	}
	if _, err := os.Stat(k3sKubeletConfig); err == nil {
		t.Error("the kubelet configuration file still exists after the section was removed, want it deleted")
	}
	if _, err := os.Stat(k3sKubeletConfigCopy); err == nil {
		t.Error("k3s's copy of the kubelet configuration still exists after the section was removed, want it deleted")
	}
}

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
				t.Fatalf("writing the boot config: %v", err)
			}
			if role != tc.role {
				t.Fatalf("the role is %s, want %s", role, tc.role)
			}
			base := k3sServerConfig
			if role == api.RoleFollower {
				base = k3sAgentConfig
			}
			bootDropIn, err := os.ReadFile(filepath.Join(base+".d", "boot.yaml"))
			if err != nil {
				t.Fatalf("reading boot.yaml: %v", err)
			}
			if !strings.Contains(string(bootDropIn), "debug: true\n") {
				t.Errorf("the k3s drop-in has no debug: true, want it:\n%s", bootDropIn)
			}
			written, err := os.ReadFile(k3sContainerdDropIn)
			if err != nil {
				t.Fatalf("reading the containerd drop-in: %v", err)
			}
			if !strings.Contains(string(written), `level = "warn"`) {
				t.Errorf("the containerd drop-in has no level = \"warn\", want the cluster's level:\n%s", written)
			}
		})
	}
}

func TestWriteK3sBootConfigWithoutLevelsWritesNeither(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-1", nil), conns); err != nil {
		t.Fatalf("writing the boot config: %v", err)
	}
	bootDropIn, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatalf("reading boot.yaml: %v", err)
	}
	if strings.Contains(string(bootDropIn), "debug:") {
		t.Errorf("the k3s drop-in has a debug key, want none when the field is unset:\n%s", bootDropIn)
	}
	if _, err := os.Stat(k3sContainerdDropIn); err == nil {
		t.Errorf("the containerd drop-in %s exists, want none when no level is set", k3sContainerdDropIn)
	}
}

// The directory is on clusterState and survives reboots, so the file
// must be deleted when the level is unset. Other files in the directory
// stay.
func TestWriteK3sBootConfigRetractsTheContainerdDropIn(t *testing.T) {
	fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}
	machineDoc := bootMachine("node-1", nil)

	if _, err := writeK3sBootConfig(debugCluster(), machineDoc, conns); err != nil {
		t.Fatalf("writing the boot config: %v", err)
	}
	if _, err := os.Stat(k3sContainerdDropIn); err != nil {
		t.Fatalf("the first boot writes the drop-in: %v", err)
	}

	operators := filepath.Join(filepath.Dir(k3sContainerdDropIn), "20-operator.toml")
	if err := os.WriteFile(operators, []byte("version = 3\n"), 0o644); err != nil {
		t.Fatalf("writing the operator drop-in: %v", err)
	}

	if _, err := writeK3sBootConfig(labCluster(), machineDoc, conns); err != nil {
		t.Fatalf("writing the boot config again: %v", err)
	}
	if _, err := os.Stat(k3sContainerdDropIn); err == nil {
		t.Error("the containerd drop-in still exists after the level was removed, want it deleted")
	}
	if _, err := os.Stat(operators); err != nil {
		t.Errorf("the retraction deleted another file in the directory: %v, want it kept", err)
	}
}

func TestNodeAddressReportsAnInvalidNodeCIDR(t *testing.T) {
	c := labCluster()
	c.Spec.Network.NodeCIDR = "not-a-cidr"
	conns := []*connection{conn(t, "eth1", "10.10.0.2/24")}
	if ip, ifname := nodeAddress(c, conns); ip != "" || ifname != "" {
		t.Errorf("nodeAddress returned %q on %q, want none for a nodeCIDR that does not parse", ip, ifname)
	}
}

func TestWriteK3sBootConfigPrintsThatThisMachineIsTheFoundingLeader(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	conns := []*connection{conn(t, "eth1", "10.10.0.1/24")}

	role, err := writeK3sBootConfig(haCluster(), bootMachine("node-1", nil), conns)
	if err != nil || role != api.RoleLeader {
		t.Fatalf("writeK3sBootConfig returned the role %s and %v, want leader for the founding leader", role, err)
	}
	raw, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatalf("reading boot.yaml: %v", err)
	}
	if !strings.Contains(string(raw), "cluster-init: true") {
		t.Errorf("the founding leader's drop-in has no cluster-init: true, want it:\n%s", raw)
	}
}

func TestWriteK3sBootConfigPrintsTheJoinURLOfAJoiningLeader(t *testing.T) {
	serverDropIns, _ := fakeK3sConfigs(t, true)
	// No manifest declares the founder's address, so the join uses
	// spec.endpoint.
	conns := []*connection{conn(t, "eth1", "10.10.0.3/24")}

	role, err := writeK3sBootConfig(haCluster(), bootMachine("node-3", nil), conns)
	if err != nil || role != api.RoleLeader {
		t.Fatalf("writeK3sBootConfig returned the role %s and %v, want leader for a joining leader", role, err)
	}
	raw, err := os.ReadFile(filepath.Join(serverDropIns, "boot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "server: https://10.10.0.1:6443") {
		t.Errorf("the joining leader's drop-in has no server: https://10.10.0.1:6443, want it:\n%s", raw)
	}
}

func TestWriteK3sBootConfigWarnsAFollowerOutsideTheNodeCIDR(t *testing.T) {
	_, agentDropIns := fakeK3sConfigs(t, true)
	// The follower's only address is outside the nodeCIDR.
	conns := []*connection{conn(t, "eth0", "192.168.1.5/24")}

	role, err := writeK3sBootConfig(labCluster(), bootMachine("node-2", nil), conns)
	if err != nil || role != api.RoleFollower {
		t.Fatalf("writeK3sBootConfig returned the role %s and %v, want follower", role, err)
	}
	raw, err := os.ReadFile(filepath.Join(agentDropIns, "boot.yaml"))
	if err != nil {
		t.Fatalf("reading boot.yaml: %v", err)
	}
	if strings.Contains(string(raw), "node-ip") {
		t.Errorf("the drop-in has a node-ip key, want none when no address is derived:\n%s", raw)
	}
}

func TestWriteK3sBootConfigReportsAnUnwritableDropInDir(t *testing.T) {
	fakeK3sConfigs(t, true)
	// The drop-in directory's parent is a regular file.
	blocked := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatalf("writing the file that blocks the drop-in directory: %v", err)
	}
	old := k3sServerConfig
	k3sServerConfig = filepath.Join(blocked, "config.yaml")
	t.Cleanup(func() { k3sServerConfig = old })

	if _, err := writeK3sBootConfig(labCluster(), bootMachine("node-1", nil), nil); err == nil {
		t.Error("no error when the drop-in directory cannot be written, want one")
	}
}
