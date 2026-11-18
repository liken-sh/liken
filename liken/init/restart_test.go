package main

// Tests for what a restart intent applies, what it refuses, and the
// facts and files it leaves. The supervisor stops and starts k3s, and
// the lab tests cover that part.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// restartFixture builds a restartState over temporary stores. Its
// renderers record their arguments, and its current cluster document
// stands in for the one this boot started with.
type restartFixture struct {
	state           *restartState
	root            string
	bootConfigs     []*cluster.Cluster
	actuations      []*cluster.Cluster
	renderedCreds   []*machine.RegistryCredentials
	renderedSources []machine.ManifestSource
}

func newRestartFixture(t *testing.T) *restartFixture {
	t.Helper()
	root := t.TempDir()
	current := &cluster.Cluster{
		APIVersion: api.APIVersion,
		Kind:       "Cluster",
		Metadata:   api.ObjectMeta{Name: "lab"},
		Spec:       cluster.ClusterSpec{Leaders: []string{"node-1"}},
	}
	f := &restartFixture{root: root}
	f.state = &restartState{
		root:       root,
		m:          &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1"}},
		tree:       machine.FactsTree{Dir: filepath.Join(t.TempDir(), "facts")},
		clusterDoc: current,
		writeBootConfig: func(c *cluster.Cluster, _ *machine.Machine, _ []*connection) (api.Role, error) {
			f.bootConfigs = append(f.bootConfigs, c)
			return api.RoleLeader, nil
		},
		actuateFeatures: func(c *cluster.Cluster, _ string) []machine.FeatureStatus {
			f.actuations = append(f.actuations, c)
			return []machine.FeatureStatus{{Name: "traefik", State: machine.FeatureActive}}
		},
		renderRegistries: func(_ *cluster.Cluster, creds *machine.RegistryCredentials,
			_ machine.ManifestStore, source machine.ManifestSource) machine.RegistriesStatus {
			f.renderedCreds = append(f.renderedCreds, creds)
			f.renderedSources = append(f.renderedSources, source)
			return machine.RegistriesStatus{}
		},
	}
	return f
}

// factFile reads one file of the restart's facts tree, trimming the
// single trailing newline. A missing file reads as the empty string,
// which is the tree's zero value for an absent fact.
func (f *restartFixture) factFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.state.tree.Dir, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("reading a restart fact: %v", err)
	}
	return strings.TrimSuffix(string(raw), "\n")
}

// stageCluster stages a change to the fixture's current document.
func (f *restartFixture) stageCluster(t *testing.T, mutate func(*cluster.ClusterSpec)) string {
	t.Helper()
	doc := *f.state.clusterDoc
	mutate(&doc.Spec)
	raw, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatalf("marshalling the staged cluster document: %v", err)
	}
	if err := machine.ClusterManifests(f.root).WriteStaged(raw); err != nil {
		t.Fatalf("staging the cluster document: %v", err)
	}
	return machine.ManifestHash(raw)
}

func (f *restartFixture) stageCredentials(t *testing.T) string {
	t.Helper()
	raw, hash, err := machine.RenderRegistryCredentials([]machine.RegistryCredential{
		{Host: "mirror.example:5000", Username: "liken", Password: "p"},
	})
	if err != nil {
		t.Fatalf("rendering the registry credentials: %v", err)
	}
	if err := machine.RegistryCredentialsStore(f.root).WriteStaged(raw); err != nil {
		t.Fatalf("staging the registry credentials: %v", err)
	}
	return hash
}

func TestRestartAppliesAStagedFeatureToggle(t *testing.T) {
	f := newRestartFixture(t)
	hash := f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	})

	// Write a feature that the restart's actuation does not report, so
	// WriteFeatures has to remove its directory.
	if err := f.state.tree.WriteFeatures([]machine.FeatureStatus{{Name: "gone", State: machine.FeatureActive}}); err != nil {
		t.Fatalf("writing the feature status: %v", err)
	}

	if !f.state.apply(machine.RestartIntent{Reason: "test"}) {
		t.Fatal("apply returned false, want true for a staged document that changes only features")
	}
	if len(f.bootConfigs) != 1 || f.bootConfigs[0].Spec.Features["traefik"] == nil {
		t.Errorf("the boot drop-in was rendered %d times, want once under the staged document with traefik", len(f.bootConfigs))
	}
	if len(f.actuations) != 1 {
		t.Errorf("the features were actuated %d times, want once under the staged document", len(f.actuations))
	}
	if attempted, _ := machine.ClusterManifests(f.root).LoadAttempted(); attempted != hash {
		t.Errorf("the attempted hash is %q, want %q, as a trial boot marks it", attempted, hash)
	}
	// boot/clusterManifest is a file of key=value lines. The operator
	// promotes the staged document when it reads its hash here.
	record := f.factFile(t, "boot/clusterManifest")
	if !strings.Contains(record, "source=Staged") || !strings.Contains(record, "hash="+hash) {
		t.Errorf("boot/clusterManifest is %q, want source=Staged and hash=%s", record, hash)
	}
	if got := f.factFile(t, "boot/restarts"); got != "1" {
		t.Errorf("boot/restarts is %q, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(f.state.tree.Dir, "features", "gone")); !os.IsNotExist(err) {
		t.Error("the directory of a retracted feature still exists, want it deleted")
	}
	if f.factFile(t, "features/traefik/state") != "Active" {
		t.Errorf("features/traefik/state is %q, want Active", f.factFile(t, "features/traefik/state"))
	}
	if f.state.clusterDoc.Spec.Features["traefik"] == nil {
		t.Error("the current document has no traefik feature, want the applied document to be current")
	}
}

func TestRestartAppliesStagedCredentialsAlone(t *testing.T) {
	f := newRestartFixture(t)
	hash := f.stageCredentials(t)

	if !f.state.apply(machine.RestartIntent{Reason: "test"}) {
		t.Fatal("apply returned false, want true for staged credentials")
	}
	if len(f.bootConfigs) != 0 {
		t.Errorf("the drop-in was rendered %d times, want none when the cluster document did not change", len(f.bootConfigs))
	}
	if len(f.renderedCreds) != 1 || f.renderedCreds[0] == nil ||
		f.renderedSources[0] != machine.ManifestSourceStaged {
		t.Errorf("the rendered credentials are %+v from %+v, want one set from the staged source", f.renderedCreds, f.renderedSources)
	}
	record := f.factFile(t, "boot/credentials")
	if !strings.Contains(record, "source=Staged") || !strings.Contains(record, "hash="+hash) {
		t.Errorf("boot/credentials is %q, want source=Staged and hash=%s", record, hash)
	}
	if got := f.factFile(t, "boot/restarts"); got != "1" {
		t.Errorf("the restart counter is %q, want 1", got)
	}
}

func TestRestartAppliesANextBootDocumentWhenAnotherDocumentRaisesTheIntent(t *testing.T) {
	// For an endpoint change alone, the operator stages the document
	// and requests no restart (machine-operator/cluster.go). When a
	// restart runs for another document, it applies the staged
	// endpoint too, because it renders the same drop-in that a boot
	// would.
	f := newRestartFixture(t)
	hash := f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Endpoint = "https://10.10.0.9:6443"
	})
	f.stageCredentials(t)

	if !f.state.apply(machine.RestartIntent{Reason: "credentials"}) {
		t.Fatal("apply returned false, want true for staged credentials")
	}
	if len(f.bootConfigs) != 1 || f.bootConfigs[0].Spec.Endpoint != "https://10.10.0.9:6443" {
		t.Errorf("the drop-in renders are %+v, want one with the staged endpoint https://10.10.0.9:6443", f.bootConfigs)
	}
	if attempted, _ := machine.ClusterManifests(f.root).LoadAttempted(); attempted != hash {
		t.Errorf("the attempted hash is %q, want %q for the applied document", attempted, hash)
	}
}

func TestRestartRefusesWithNothingStaged(t *testing.T) {
	f := newRestartFixture(t)
	if f.state.apply(machine.RestartIntent{Reason: "duplicate"}) {
		t.Error("apply returned true with nothing staged, want false and no restart")
	}
	if got := f.factFile(t, "boot/restarts"); got != "" {
		t.Errorf("boot/restarts is %q, want empty after a refused restart", got)
	}
}

func TestRestartRefusesAnAlreadyAttemptedDocument(t *testing.T) {
	// An attempted document waits for the operator's promotion or the
	// next boot's rejection, so a restart does not apply it again.
	f := newRestartFixture(t)
	hash := f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	})
	if err := machine.ClusterManifests(f.root).WriteAttempted(hash); err != nil {
		t.Fatalf("marking the document attempted: %v", err)
	}
	if f.state.apply(machine.RestartIntent{Reason: "duplicate"}) {
		t.Error("apply returned true for a document already attempted, want false")
	}
}

func TestRestartLeavesARebootClassDocumentStaged(t *testing.T) {
	f := newRestartFixture(t)
	f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Network.ClusterCIDR = "10.44.0.0/16"
	})

	if f.state.apply(machine.RestartIntent{Reason: "racing intent"}) {
		t.Error("apply returned true for a document that needs a reboot, want false")
	}
	if staged, _ := machine.ClusterManifests(f.root).LoadStaged(); staged == nil {
		t.Error("no document is staged, want it to stay staged for the reboot")
	}
	if attempted, _ := machine.ClusterManifests(f.root).LoadAttempted(); attempted != "" {
		t.Errorf("the attempted hash is %q, want none for a document that was not applied", attempted)
	}
}

func TestRestartRejectsAGarbageStagedDocument(t *testing.T) {
	f := newRestartFixture(t)
	store := machine.ClusterManifests(f.root)
	if err := store.WriteStaged([]byte("kind: Nonsense\n")); err != nil {
		t.Fatalf("staging the document: %v", err)
	}

	if f.state.apply(machine.RestartIntent{Reason: "test"}) {
		t.Error("apply returned true for a staged document that does not parse, want false")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the document that does not parse is still staged, want it rejected as at boot")
	}
	if rejection, _ := store.LoadRejection(); rejection == nil {
		t.Error("the store has no rejection, want it recorded")
	}
}

// retractionFixture sets up a feature as a boot that declared it
// leaves it: the files in names under the image's features directory,
// and copies of them in k3s's auto-deploy directory. It returns both
// directories, so a test can add files that init renders.
func retractionFixture(t *testing.T, slug string, names ...string) (features, seeded string) {
	t.Helper()
	features = t.TempDir()
	seeded = filepath.Join(t.TempDir(), "manifests", "liken")
	originalFeatures, originalManifests := featuresDir, k3sManifestsDir
	featuresDir, k3sManifestsDir = features, seeded
	t.Cleanup(func() { featuresDir, k3sManifestsDir = originalFeatures, originalManifests })
	if err := os.MkdirAll(filepath.Join(features, slug, "manifests"), 0o755); err != nil {
		t.Fatalf("creating the feature manifests directory: %v", err)
	}
	if err := os.MkdirAll(seeded, 0o755); err != nil {
		t.Fatalf("creating the k3s manifests directory: %v", err)
	}
	for _, name := range names {
		for _, dir := range []string{filepath.Join(features, slug, "manifests"), seeded} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("kind: DaemonSet\n"), 0o644); err != nil {
				t.Fatalf("writing a feature manifest: %v", err)
			}
		}
	}
	return features, seeded
}

// This test calls retractFeatureManifests directly. It does not cover
// the classifier in cluster/changes.go, which decides whether a change
// reaches the restart path.
func TestRetractionRemovesADroppedFeaturesManifests(t *testing.T) {
	f := newRestartFixture(t)
	_, seeded := retractionFixture(t, "iscsi", "iscsid.yaml")

	// This boot ran with iscsi declared. The new document drops it.
	declared := &cluster.Cluster{Spec: cluster.ClusterSpec{
		Features: map[string]*cluster.FeatureConfig{"iscsi": {}},
	}}
	f.state.retractFeatureManifests(declared, f.state.clusterDoc)

	if _, err := os.Stat(filepath.Join(seeded, "iscsid.yaml")); !os.IsNotExist(err) {
		t.Error("iscsid.yaml is still in the auto-deploy directory, want it deleted with its feature")
	}
	if len(f.state.queuedRetractions) != 0 {
		t.Errorf("the queued retractions are %v, want none, because a feature with the default teardown has its files deleted at once", f.state.queuedRetractions)
	}
}

// retractFeatureManifests explains why flux's files must stay while
// k3s runs.
func TestRestartDeletesFluxManifestsOnlyAfterK3sStops(t *testing.T) {
	f := newRestartFixture(t)
	// The image ships flux-system.yaml, and init renders
	// flux-sync.yaml, so the retraction has to remove both.
	_, seeded := retractionFixture(t, "flux", "flux-system.yaml")
	if err := os.WriteFile(filepath.Join(seeded, "flux-sync.yaml"), []byte("kind: GitRepository\n"), 0o644); err != nil {
		t.Fatalf("writing flux-sync.yaml: %v", err)
	}

	f.state.clusterDoc.Spec.Features = map[string]*cluster.FeatureConfig{
		"flux": {"repository": "ssh://git@forge.example/fleet.git"},
	}
	f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Features = nil
	})

	if !f.state.apply(machine.RestartIntent{Reason: "retraction"}) {
		t.Fatal("apply returned false, want true for a flux retraction")
	}
	for _, file := range []string{"flux-system.yaml", "flux-sync.yaml"} {
		if _, err := os.Stat(filepath.Join(seeded, file)); err != nil {
			t.Errorf("%s is missing while k3s runs: %v, want it kept until k3s stops", file, err)
		}
	}

	f.state.removeQueuedRetractions()
	for _, file := range []string{"flux-system.yaml", "flux-sync.yaml"} {
		if _, err := os.Stat(filepath.Join(seeded, file)); !os.IsNotExist(err) {
			t.Errorf("%s still exists after k3s stopped, want it deleted", file)
		}
	}
}
