package main

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// liveLoadFixture builds everything a live load touches: a manifest
// store with a staged Machine document, a fabricated module tree
// where "loop" is builtin (so outcomes need no real kernel), and a
// facts owner publishing into a tempdir. It returns the store, the
// tree, the facts, and the staged document's hash.
func liveLoadFixture(t *testing.T, stagedModules []string, bootModules []string, stagedStorage machine.StorageSpec) (machine.ManifestStore, string, *factsFile, string) {
	t.Helper()

	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "modules.dep"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "modules.builtin"), []byte("kernel/block/loop.ko\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bootStorage := machine.StorageSpec{
		MachineState: &machine.StorageRole{Device: "/dev/vda", Size: "64Mi"},
	}
	doc := machine.Machine{
		APIVersion: api.APIVersion,
		Kind:       "Machine",
		Metadata:   api.ObjectMeta{Name: "lab"},
		Spec:       machine.MachineSpec{Modules: stagedModules, Storage: stagedStorage},
	}
	raw, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}
	store := machine.MachineManifests(t.TempDir())
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}

	restore := factsPath
	factsPath = filepath.Join(t.TempDir(), "facts.yaml")
	t.Cleanup(func() { factsPath = restore })
	facts := &factsFile{status: &machine.MachineStatus{
		Boot: machine.BootStatus{
			ManifestSource: machine.ManifestSourceProven,
			ManifestHash:   "before",
			Storage:        bootStorage,
			Modules:        bootModules,
		},
	}}
	return store, base, facts, machine.ManifestHash(raw)
}

// bootStorageSpec is the storage the fixture's boot record actuated;
// staging the same spec means no storage drift.
func bootStorageSpec() machine.StorageSpec {
	return machine.StorageSpec{
		MachineState: &machine.StorageRole{Device: "/dev/vda", Size: "64Mi"},
	}
}

func TestLiveLoadAppliesAnAdditiveSpec(t *testing.T) {
	store, base, facts, hash := liveLoadFixture(t, []string{"loop"}, nil, bootStorageSpec())

	applyModulesIntent(machine.ModulesIntent{ManifestHash: hash}, store, base, facts)

	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged manifest should have been promoted")
	}
	if proven, _ := store.LoadProven(); machine.ManifestHash(proven) != hash {
		t.Error("the proven manifest should be the staged one")
	}
	if facts.status.Boot.ManifestHash != hash || facts.status.Boot.ManifestSource != machine.ManifestSourceProven {
		t.Errorf("the boot record should carry the applied spec: %+v", facts.status.Boot)
	}
	if len(facts.status.Boot.Modules) != 1 || facts.status.Boot.Modules[0] != "loop" {
		t.Errorf("Boot.Modules = %v", facts.status.Boot.Modules)
	}
	if len(facts.status.Modules) != 1 || facts.status.Modules[0].State != machine.ModuleBuiltin {
		t.Errorf("status.Modules = %+v", facts.status.Modules)
	}
	if _, err := os.Stat(factsPath); err != nil {
		t.Error("the facts should have been republished")
	}
}

func TestLiveLoadRefusesAStorageChange(t *testing.T) {
	changed := bootStorageSpec()
	changed.MachineState.Size = "128Mi"
	store, base, facts, hash := liveLoadFixture(t, []string{"loop"}, nil, changed)

	applyModulesIntent(machine.ModulesIntent{ManifestHash: hash}, store, base, facts)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a storage-changing manifest must stay staged for its proving boot")
	}
	if facts.status.Boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", facts.status.Boot)
	}
}

func TestLiveLoadRefusesARetraction(t *testing.T) {
	store, base, facts, hash := liveLoadFixture(t, []string{"loop"}, []string{"loop", "zram"}, bootStorageSpec())

	applyModulesIntent(machine.ModulesIntent{ManifestHash: hash}, store, base, facts)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a retracting manifest must stay staged for its reboot")
	}
	if facts.status.Boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", facts.status.Boot)
	}
}

func TestLiveLoadToleratesAStaleIntent(t *testing.T) {
	store, base, facts, _ := liveLoadFixture(t, []string{"loop"}, nil, bootStorageSpec())
	if err := store.WithdrawStaged(); err != nil {
		t.Fatal(err)
	}

	applyModulesIntent(machine.ModulesIntent{ManifestHash: "whatever"}, store, base, facts)

	if facts.status.Boot.ManifestHash != "before" {
		t.Errorf("a stale intent must change nothing: %+v", facts.status.Boot)
	}
}

func TestMergeModuleStatusesUpsertsByName(t *testing.T) {
	merged := mergeModuleStatuses(
		[]machine.ModuleStatus{
			{Name: "zram", State: machine.ModuleLoaded},
			{Name: "loop", State: machine.ModuleFailed, Message: "an earlier failure"},
		},
		[]machine.ModuleStatus{{Name: "loop", State: machine.ModuleLoaded}},
	)
	if len(merged) != 2 {
		t.Fatalf("merged = %+v", merged)
	}
	byName := map[string]machine.ModuleStatus{}
	for _, s := range merged {
		byName[s.Name] = s
	}
	if byName["loop"].State != machine.ModuleLoaded || byName["loop"].Message != "" {
		t.Errorf("the new outcome should replace the old: %+v", byName["loop"])
	}
	if byName["zram"].State != machine.ModuleLoaded {
		t.Errorf("untouched outcomes should survive: %+v", byName["zram"])
	}
}
