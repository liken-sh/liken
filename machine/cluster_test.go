package machine

import (
	"os"
	"path/filepath"
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
