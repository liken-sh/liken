package machine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeClusterManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadClusterMissingFileIsNoCluster(t *testing.T) {
	c, err := LoadCluster(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("expected no cluster, got error: %v", err)
	}
	if c != nil {
		t.Fatalf("expected nil cluster, got %+v", c)
	}
}

func TestLoadClusterParsesSpec(t *testing.T) {
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  origin: adopted
  leaders: [node-1]
  endpoint: https://10.10.0.1:6443
  network:
    nodeCIDR: 10.10.0.0/24
    clusterCIDR: 10.42.0.0/16
    serviceCIDR: 10.43.0.0/16
    clusterDNS: 10.43.0.10
    clusterDomain: cluster.local
  time:
    upstreams: [time.cloudflare.com, 192.168.1.1]
`)
	c, err := LoadCluster(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Metadata.Name != "lab" {
		t.Errorf("name: got %q", c.Metadata.Name)
	}
	if len(c.Spec.Leaders) != 1 || c.Spec.Leaders[0] != "node-1" {
		t.Errorf("leaders: got %v", c.Spec.Leaders)
	}
	if c.Spec.Endpoint != "https://10.10.0.1:6443" {
		t.Errorf("endpoint: got %q", c.Spec.Endpoint)
	}
	if c.Spec.Origin != OriginAdopted {
		t.Errorf("origin: got %q", c.Spec.Origin)
	}
	if c.Spec.Network.NodeCIDR != "10.10.0.0/24" {
		t.Errorf("nodeCIDR: got %q", c.Spec.Network.NodeCIDR)
	}
	if c.Spec.Network.ClusterDomain != "cluster.local" {
		t.Errorf("clusterDomain: got %q", c.Spec.Network.ClusterDomain)
	}
	if len(c.Spec.Time.Upstreams) != 2 || c.Spec.Time.Upstreams[0] != "time.cloudflare.com" {
		t.Errorf("time upstreams: got %v", c.Spec.Time.Upstreams)
	}
}

func TestLoadClusterRejectsWrongKind(t *testing.T) {
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: lab
`)
	if _, err := LoadCluster(path); err == nil {
		t.Fatal("expected an error for kind Machine")
	}
}

func TestLoadClusterRejectsUnknownFields(t *testing.T) {
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  leaderss: [node-1]
`)
	if _, err := LoadCluster(path); err == nil {
		t.Fatal("expected an error for the misspelled field")
	}
}

func TestClusterFeaturesParse(t *testing.T) {
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  features:
    metrics-server: {}
`)
	c, err := LoadCluster(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.EnabledFeatures(); len(got) != 1 || got[0] != "metrics-server" {
		t.Errorf("enabled features: got %v", got)
	}
}

func TestClusterFeaturesRejectNull(t *testing.T) {
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  features:
    metrics-server:
`)
	_, err := LoadCluster(path)
	if err == nil {
		t.Fatal("expected an error for a null feature")
	}
	if !strings.Contains(err.Error(), `"metrics-server: {}"`) {
		t.Errorf("the error should say what to write instead, got: %v", err)
	}
}

func TestClusterFeaturesParseUnknownSlugs(t *testing.T) {
	// An unknown slug is not a parse error, deliberately: each
	// machine's parser knows only its own image's vocabulary, and a
	// document declaring a feature this binary predates must still
	// parse, or a downgraded machine could not read its own proven
	// document. The gap is reported through the feature pass instead
	// (init/features.go); the CRD still refuses unknown slugs at
	// admission, where the fleet's one current vocabulary lives.
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  features:
    from-the-future: {}
`)
	c, err := LoadCluster(path)
	if err != nil {
		t.Fatalf("a feature from a newer vocabulary must parse: %v", err)
	}
	if got := c.EnabledFeatures(); len(got) != 1 || got[0] != "from-the-future" {
		t.Errorf("enabled features: got %v", got)
	}
}

func TestClusterFeaturesRejectParameters(t *testing.T) {
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  features:
    metrics-server:
      replicas: 2
`)
	if _, err := LoadCluster(path); err == nil {
		t.Fatal("expected an error: no feature has parameters yet")
	}
}

func TestRoleDerivation(t *testing.T) {
	cluster := &Cluster{Spec: ClusterSpec{Leaders: []string{"node-1"}}}
	for _, tc := range []struct {
		name    string
		cluster *Cluster
		machine string
		want    Role
	}{
		{"named leader", cluster, "node-1", RoleLeader},
		{"unnamed machine is a follower", cluster, "node-2", RoleFollower},
		{"no cluster manifest means a machine alone", nil, "node-1", RoleLeader},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cluster.Role(tc.machine); got != tc.want {
				t.Errorf("Role(%q) = %q, want %q", tc.machine, got, tc.want)
			}
		})
	}
}

func TestMaxUnavailableDefaultsToOne(t *testing.T) {
	var spec ClusterSpec
	if got := spec.Disruption.MaxUnavailableOrDefault(); got != 1 {
		t.Errorf("got %d", got)
	}
}

func TestMaxUnavailableHonorsTheDeclaredBudget(t *testing.T) {
	spec := ClusterSpec{Disruption: ClusterDisruptionSpec{MaxUnavailable: 2}}
	if got := spec.Disruption.MaxUnavailableOrDefault(); got != 2 {
		t.Errorf("got %d", got)
	}
}
