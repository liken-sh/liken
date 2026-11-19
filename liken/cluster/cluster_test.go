package cluster

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
)

func writeClusterManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the cluster manifest: %v", err)
	}
	return path
}

func TestLoadClusterMissingFileIsNoCluster(t *testing.T) {
	c, err := LoadCluster(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("loading a missing file returns the error %v, want no cluster and no error", err)
	}
	if c != nil {
		t.Fatalf("loading a missing file returns the cluster %+v, want nil", c)
	}
}

func TestLoadClusterParsesSpec(t *testing.T) {
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  origin: Adopted
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
		t.Fatalf("loading the cluster manifest: %v", err)
	}
	if c.Metadata.Name != "lab" {
		t.Errorf("the name is %q, want lab", c.Metadata.Name)
	}
	if len(c.Spec.Leaders) != 1 || c.Spec.Leaders[0] != "node-1" {
		t.Errorf("the leaders are %v, want [node-1]", c.Spec.Leaders)
	}
	if c.Spec.Endpoint != "https://10.10.0.1:6443" {
		t.Errorf("the endpoint is %q, want https://10.10.0.1:6443", c.Spec.Endpoint)
	}
	if c.Spec.Origin != OriginAdopted {
		t.Errorf("the origin is %q, want %q", c.Spec.Origin, OriginAdopted)
	}
	if c.Spec.Network.NodeCIDR != "10.10.0.0/24" {
		t.Errorf("the nodeCIDR is %q, want 10.10.0.0/24", c.Spec.Network.NodeCIDR)
	}
	if c.Spec.Network.ClusterDomain != "cluster.local" {
		t.Errorf("the clusterDomain is %q, want cluster.local", c.Spec.Network.ClusterDomain)
	}
	if len(c.Spec.Time.Upstreams) != 2 || c.Spec.Time.Upstreams[0] != "time.cloudflare.com" {
		t.Errorf("the time upstreams are %v, want [time.cloudflare.com 192.168.1.1]", c.Spec.Time.Upstreams)
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
		t.Fatal("loading a manifest of kind Machine returns no error, want one")
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
		t.Fatal("loading a manifest with the misspelled field leaderss returns no error, want one")
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
		t.Fatalf("loading the cluster manifest: %v", err)
	}
	if got := c.EnabledFeatures(); len(got) != 1 || got[0] != "metrics-server" {
		t.Errorf("the enabled features are %v, want one feature", got)
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
		t.Fatal("loading a feature with a null value returns no error, want one")
	}
	if !strings.Contains(err.Error(), `"metrics-server: {}"`) {
		t.Errorf("the error is %v, want it to give the replacement \"metrics-server: {}\"", err)
	}
}

func TestClusterFeaturesParseUnknownSlugs(t *testing.T) {
	// An unknown slug is not a parse error, on purpose. Each
	// machine's parser carries only its own image's vocabulary, and a
	// document that declares a feature this binary predates must
	// still parse. Otherwise a downgraded machine could not read its
	// own proven document. The feature pass reports the gap instead
	// (init/features.go). The CRD still refuses unknown slugs at
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
		t.Fatalf("parsing a feature from a newer vocabulary: %v", err)
	}
	if got := c.EnabledFeatures(); len(got) != 1 || got[0] != "from-the-future" {
		t.Errorf("the enabled features are %v, want one feature", got)
	}
}

func TestClusterFeaturesParseParameters(t *testing.T) {
	// Parameters are not a parse error, for the same reason unknown
	// slugs are not: each machine's parser carries only its own image's
	// parameter vocabulary, and a document from a newer vocabulary
	// must still parse, or a downgraded machine could not read its
	// own proven document. The CRD refuses a parameter its vocabulary
	// does not list at admission, and init's feature pass reports one
	// this image cannot honor (init/features.go).
	path := writeClusterManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  features:
    flux:
      repository: ssh://git@forge.example/fleet.git
    metrics-server:
      replicas: 2
`)
	c, err := LoadCluster(path)
	if err != nil {
		t.Fatalf("parsing feature parameters: %v", err)
	}
	flux := *c.Spec.Features["flux"]
	if flux["repository"] != "ssh://git@forge.example/fleet.git" {
		t.Errorf("the parsed feature parameters are %v, want the declared parameter", flux)
	}
}

func TestRoleDerivation(t *testing.T) {
	cluster := &Cluster{Spec: ClusterSpec{Leaders: []string{"node-1"}}}
	for _, tc := range []struct {
		name    string
		cluster *Cluster
		machine string
		want    api.Role
	}{
		{"named leader", cluster, "node-1", api.RoleLeader},
		{"unnamed machine is a follower", cluster, "node-2", api.RoleFollower},
		{"no cluster manifest means a machine alone", nil, "node-1", api.RoleLeader},
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
		t.Errorf("MaxUnavailableOrDefault is %d, want 1", got)
	}
}

func TestMaxUnavailableHonorsTheDeclaredBudget(t *testing.T) {
	spec := ClusterSpec{Disruption: ClusterDisruptionSpec{MaxUnavailable: 2}}
	if got := spec.Disruption.MaxUnavailableOrDefault(); got != 2 {
		t.Errorf("MaxUnavailableOrDefault is %d, want 2", got)
	}
}

func TestLoadClusterReportsAnUnreadableFile(t *testing.T) {
	path := unreadableFile(t, filepath.Join(t.TempDir(), "cluster.yaml"))
	if _, err := LoadCluster(path); err == nil {
		t.Error("loading an unreadable cluster manifest returns no error, want one")
	}
}

func TestReleasesEntryFindsItsVersion(t *testing.T) {
	releases := ClusterReleasesSpec{Catalog: []ReleaseCatalogEntry{
		{Version: "0.1.0", Digest: "sha256:aaa"},
		{Version: "0.2.0", Digest: "sha256:bbb"},
	}}
	entry := releases.Entry("0.2.0")
	if entry == nil || entry.Digest != "sha256:bbb" {
		t.Errorf("the entry is %+v, want the digest sha256:bbb", entry)
	}
}

func TestReleasesEntryUnknownVersionIsNil(t *testing.T) {
	releases := ClusterReleasesSpec{Catalog: []ReleaseCatalogEntry{{Version: "0.1.0"}}}
	if entry := releases.Entry("0.9.9"); entry != nil {
		t.Errorf("the entry for a version missing from the catalog is %+v, want nil", entry)
	}
}

func TestReleasesEntryEmptyCatalogIsNil(t *testing.T) {
	if entry := (ClusterReleasesSpec{}).Entry("0.1.0"); entry != nil {
		t.Errorf("the entry in an empty catalog is %+v, want nil", entry)
	}
}

func TestNewestVersion(t *testing.T) {
	catalog := []ReleaseCatalogEntry{
		{Version: "2026.07.09-002"},
		{Version: "2026.07.11-001"},
		{Version: "2026.07.10-001"},
	}
	if got := NewestVersion(catalog); got != "2026.07.11-001" {
		t.Errorf("the newest version is %q, want 2026.07.11-001", got)
	}
	if got := NewestVersion(nil); got != "" {
		t.Errorf("the newest version of an empty catalog is %q, want an empty string", got)
	}
}

// A cluster that names no NodePort networks answers on the node IP
// alone, which is kube-proxy's own "primary" keyword.
func TestNodePortAddressesDefaultsToTheNodeIP(t *testing.T) {
	if got := (&Cluster{}).NodePortAddresses(); !slices.Equal(got, []string{"primary"}) {
		t.Errorf("with an unset list, the addresses are %q, want primary", got)
	}
	var absent *Cluster
	if got := absent.NodePortAddresses(); !slices.Equal(got, []string{"primary"}) {
		t.Errorf("with no cluster, the addresses are %q, want primary", got)
	}
}

// A cluster that names networks gets exactly those, because the list
// replaces the default rather than adding to it.
func TestNodePortAddressesUsesTheDocumentsList(t *testing.T) {
	c := &Cluster{}
	c.Spec.Network.NodePortCIDRs = []string{"10.0.0.0/24", "10.10.10.0/24"}
	want := []string{"10.0.0.0/24", "10.10.10.0/24"}
	if got := c.NodePortAddresses(); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNetworkValidateRefusesWhatIsNotASubnet(t *testing.T) {
	cases := map[string]string{
		"a bare address": "10.10.10.11",
		"nonsense":       "the tunnel",
		"a missing size": "10.10.10.0/",
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			err := ClusterNetworkSpec{NodePortCIDRs: []string{entry}}.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %q, want an error because it is not a subnet", entry)
			}
			if !strings.Contains(err.Error(), "nodePortCIDRs[0]") {
				t.Errorf("the error is %v, want it to name nodePortCIDRs[0]", err)
			}
		})
	}
}

func TestNetworkValidateAcceptsSubnets(t *testing.T) {
	spec := ClusterNetworkSpec{NodePortCIDRs: []string{"10.0.0.0/24", "10.10.10.11/32", "fd00::/64"}}
	if err := spec.Validate(); err != nil {
		t.Errorf("Validate returned %v, want no error", err)
	}
}

// A Cluster answers its own metadata to a watch's store and the memo.
func TestAClusterAnswersItsMetadata(t *testing.T) {
	c := &Cluster{Metadata: api.ObjectMeta{Name: "lab", ResourceVersion: "42"}}
	if meta := c.GetObjectMeta(); meta.GetName() != "lab" || meta.GetResourceVersion() != "42" {
		t.Errorf("GetObjectMeta = %q at %q, want lab at 42", meta.GetName(), meta.GetResourceVersion())
	}
}
