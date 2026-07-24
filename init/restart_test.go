package main

// Tests for the restart path's decisions and file effects: what a
// restart intent applies, what it refuses, and what the facts show
// afterward. The supervisor handles the restart itself, stopping and
// starting k3s, and the lab tests prove that part.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// restartFixture builds a restartState over tempdir stores. It uses
// recording seams instead of the real renderers, plus a current
// cluster document that stands in for the one this boot ran.
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
		t.Fatal(err)
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
		t.Fatal(err)
	}
	if err := machine.ClusterManifests(f.root).WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	return machine.ManifestHash(raw)
}

func (f *restartFixture) stageCredentials(t *testing.T) string {
	t.Helper()
	raw, hash, err := machine.RenderRegistryCredentials([]machine.RegistryCredential{
		{Host: "mirror.example:5000", Username: "liken", Password: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.RegistryCredentialsStore(f.root).WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestRestartAppliesAStagedFeatureToggle(t *testing.T) {
	f := newRestartFixture(t)
	hash := f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	})

	// Seed a feature that the restart's actuation no longer reports, so
	// the reconcile in WriteFeatures must remove its directory.
	if err := f.state.tree.WriteFeatures([]machine.FeatureStatus{{Name: "gone", State: machine.FeatureActive}}); err != nil {
		t.Fatal(err)
	}

	if !f.state.apply(machine.RestartIntent{Reason: "test"}) {
		t.Fatal("a staged features-only document is exactly what a restart applies")
	}
	if len(f.bootConfigs) != 1 || f.bootConfigs[0].Spec.Features["traefik"] == nil {
		t.Error("the boot drop-in must re-render under the staged document")
	}
	if len(f.actuations) != 1 {
		t.Error("features must re-actuate under the staged document")
	}
	if attempted, _ := machine.ClusterManifests(f.root).LoadAttempted(); attempted != hash {
		t.Errorf("the trial must be marked, exactly as a proving boot would: %q", attempted)
	}
	// The facts name the staged document for the operator's promotion.
	// boot/clusterManifest is a record file of key=value lines.
	record := f.factFile(t, "boot/clusterManifest")
	if !strings.Contains(record, "source=Staged") || !strings.Contains(record, "hash="+hash) {
		t.Errorf("boot/clusterManifest = %q", record)
	}
	if got := f.factFile(t, "boot/restarts"); got != "1" {
		t.Errorf("the restart counter is the observable: %q", got)
	}
	// The reconcile removes the vanished feature and keeps the current
	// one.
	if _, err := os.Stat(filepath.Join(f.state.tree.Dir, "features", "gone")); !os.IsNotExist(err) {
		t.Error("a retracted feature's directory must be gone")
	}
	if f.factFile(t, "features/traefik/state") != "Active" {
		t.Error("the re-actuated feature must be reported")
	}
	if f.state.clusterDoc.Spec.Features["traefik"] == nil {
		t.Error("the applied document becomes current")
	}
}

func TestRestartAppliesStagedCredentialsAlone(t *testing.T) {
	f := newRestartFixture(t)
	hash := f.stageCredentials(t)

	if !f.state.apply(machine.RestartIntent{Reason: "test"}) {
		t.Fatal("staged credentials are restart work")
	}
	if len(f.bootConfigs) != 0 {
		t.Error("an unchanged cluster document must not re-render the drop-in")
	}
	if len(f.renderedCreds) != 1 || f.renderedCreds[0] == nil ||
		f.renderedSources[0] != machine.ManifestSourceStaged {
		t.Errorf("the staged credentials must render as staged: %+v %+v", f.renderedCreds, f.renderedSources)
	}
	record := f.factFile(t, "boot/credentials")
	if !strings.Contains(record, "source=Staged") || !strings.Contains(record, "hash="+hash) {
		t.Errorf("boot/credentials = %q", record)
	}
	if got := f.factFile(t, "boot/restarts"); got != "1" {
		t.Errorf("got %q restarts", got)
	}
}

func TestRestartRefusesWithNothingStaged(t *testing.T) {
	f := newRestartFixture(t)
	if f.state.apply(machine.RestartIntent{Reason: "duplicate"}) {
		t.Error("nothing staged means nothing to bounce for")
	}
	if got := f.factFile(t, "boot/restarts"); got != "" {
		t.Errorf("a refused restart must not count: %q", got)
	}
}

func TestRestartRefusesAnAlreadyAttemptedDocument(t *testing.T) {
	// The intent only signals that work might exist; the stores
	// decide what to apply. A document that this restart, or a
	// previous boot, already tried waits for the operator's promotion
	// or the next boot's decision.
	f := newRestartFixture(t)
	hash := f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	})
	if err := machine.ClusterManifests(f.root).WriteAttempted(hash); err != nil {
		t.Fatal(err)
	}
	if f.state.apply(machine.RestartIntent{Reason: "duplicate"}) {
		t.Error("an attempted document is not new work")
	}
}

func TestRestartLeavesARebootClassDocumentStanding(t *testing.T) {
	f := newRestartFixture(t)
	f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Endpoint = "https://10.10.0.9:6443"
	})

	if f.state.apply(machine.RestartIntent{Reason: "racing intent"}) {
		t.Error("a reboot-class document is the reboot path's business")
	}
	if staged, _ := machine.ClusterManifests(f.root).LoadStaged(); staged == nil {
		t.Error("the document must stay staged for the reboot")
	}
	if attempted, _ := machine.ClusterManifests(f.root).LoadAttempted(); attempted != "" {
		t.Error("an unapplied document must not be marked attempted")
	}
}

func TestRestartRejectsAGarbageStagedDocument(t *testing.T) {
	f := newRestartFixture(t)
	store := machine.ClusterManifests(f.root)
	if err := store.WriteStaged([]byte("kind: Nonsense\n")); err != nil {
		t.Fatal(err)
	}

	if f.state.apply(machine.RestartIntent{Reason: "test"}) {
		t.Error("garbage applies nothing")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("garbage must be quarantined, exactly as at boot")
	}
	if rejection, _ := store.LoadRejection(); rejection == nil {
		t.Error("the rejection must be recorded")
	}
}

func TestRestartRetractsADroppedFeaturesManifests(t *testing.T) {
	f := newRestartFixture(t)

	// The image includes a manifest for the iscsi feature, and a
	// previous boot seeded it into k3s's auto-deploy directory.
	features := t.TempDir()
	seeded := t.TempDir()
	originalFeatures, originalManifests := featuresDir, k3sManifestsDir
	featuresDir, k3sManifestsDir = features, seeded
	t.Cleanup(func() { featuresDir, k3sManifestsDir = originalFeatures, originalManifests })
	if err := os.MkdirAll(filepath.Join(features, "iscsi", "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(features, "iscsi", "manifests", "iscsid.yaml"),
		filepath.Join(seeded, "iscsid.yaml"),
	} {
		if err := os.WriteFile(path, []byte("kind: DaemonSet\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// This boot ran with iscsi declared. The staged document drops it.
	f.state.clusterDoc.Spec.Features = map[string]*cluster.FeatureConfig{"iscsi": {}}
	f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Features = nil
	})

	if !f.state.apply(machine.RestartIntent{Reason: "retraction"}) {
		t.Fatal("a feature retraction is restart work")
	}
	if _, err := os.Stat(filepath.Join(seeded, "iscsid.yaml")); !os.IsNotExist(err) {
		t.Error("the retracted feature's manifest must leave the auto-deploy directory while k3s watches")
	}
}

// A janitor-teardown feature's files must survive the apply, while
// k3s still watches the directory, and leave only after the stop.
// If k3s saw the removal, it would delete the sync objects while
// their controller still runs, and the engine's deletion finalizer
// would prune everything the repository ever applied.
func TestRestartRetractsFluxOnlyAfterTheStop(t *testing.T) {
	f := newRestartFixture(t)

	features := t.TempDir()
	seeded := t.TempDir()
	originalFeatures, originalManifests := featuresDir, k3sManifestsDir
	featuresDir, k3sManifestsDir = features, seeded
	t.Cleanup(func() { featuresDir, k3sManifestsDir = originalFeatures, originalManifests })
	if err := os.MkdirAll(filepath.Join(features, "flux", "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The feature's ground rides the image; the sync objects are
	// rendered, so retraction must know both.
	for _, path := range []string{
		filepath.Join(features, "flux", "manifests", "flux-system.yaml"),
		filepath.Join(seeded, "flux-system.yaml"),
		filepath.Join(seeded, "flux-sync.yaml"),
	} {
		if err := os.WriteFile(path, []byte("kind: Namespace\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	f.state.clusterDoc.Spec.Features = map[string]*cluster.FeatureConfig{
		"flux": {"repository": "ssh://git@forge.example/fleet.git"},
	}
	f.stageCluster(t, func(s *cluster.ClusterSpec) {
		s.Features = nil
	})

	if !f.state.apply(machine.RestartIntent{Reason: "retraction"}) {
		t.Fatal("a flux retraction is restart work")
	}
	for _, file := range []string{"flux-system.yaml", "flux-sync.yaml"} {
		if _, err := os.Stat(filepath.Join(seeded, file)); err != nil {
			t.Errorf("%s must still exist while k3s watches: %v", file, err)
		}
	}

	f.state.removeOfflineRetractions()
	for _, file := range []string{"flux-system.yaml", "flux-sync.yaml"} {
		if _, err := os.Stat(filepath.Join(seeded, file)); !os.IsNotExist(err) {
			t.Errorf("%s must leave once k3s is down", file)
		}
	}
}
