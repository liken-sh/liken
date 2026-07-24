package main

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// liveLoadFixture builds everything that a live load touches: a
// manifest store with a staged Machine document, a fabricated module
// tree where "loop" is builtin (so outcomes need no real kernel), and a
// module loader that publishes into a temporary facts tree. The tree is
// seeded with boot/manifest = Proven/before, so a refused load leaves a
// record to prove the boot state stayed untouched. It returns the
// store, the module tree, the loader, and the staged document's hash.
func liveLoadFixture(t *testing.T, stagedModules []string, bootModules []string, stagedStorage machine.StorageSpec) (machine.ManifestStore, string, *moduleLoader, string) {
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

	tree := machine.FactsTree{Dir: filepath.Join(t.TempDir(), "facts")}
	if err := tree.WriteBootManifest(machine.ManifestSourceProven, "before"); err != nil {
		t.Fatal(err)
	}
	loader := &moduleLoader{
		tree:        tree,
		bootStorage: bootStorage,
		bootModules: bootModules,
	}
	return store, base, loader, machine.ManifestHash(raw)
}

// bootManifestRecord reads the loader's boot/manifest record back as a
// status, so a test can assert the source and hash the load committed.
func bootManifestRecord(t *testing.T, loader *moduleLoader) machine.BootStatus {
	t.Helper()
	facts, err := loader.tree.Read()
	if err != nil {
		t.Fatal(err)
	}
	return facts.Boot
}

// bootStorageSpec is the storage that the fixture's boot record
// actuated. Staging the same spec means no storage drift.
func bootStorageSpec() machine.StorageSpec {
	return machine.StorageSpec{
		MachineState: &machine.StorageRole{Device: "/dev/vda", Size: "64Mi"},
	}
}

func TestLiveLoadAppliesAnAdditiveSpec(t *testing.T) {
	store, base, loader, hash := liveLoadFixture(t, []string{"loop"}, nil, bootStorageSpec())

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged manifest should have been promoted")
	}
	if proven, _ := store.LoadProven(); machine.ManifestHash(proven) != hash {
		t.Error("the proven manifest should be the staged one")
	}
	boot := bootManifestRecord(t, loader)
	if boot.ManifestHash != hash || boot.ManifestSource != machine.ManifestSourceProven {
		t.Errorf("the boot record should carry the applied spec: %+v", boot)
	}
	if len(boot.Modules) != 1 || boot.Modules[0] != "loop" {
		t.Errorf("boot/modules = %v", boot.Modules)
	}
	// cat parity: the applied manifest is one record file, and the
	// module's outcome is its own file. The write order pins these to
	// land before the manifest record the operator reads to converge.
	record, err := os.ReadFile(filepath.Join(loader.tree.Dir, "boot", "manifest"))
	if err != nil || string(record) != "source=Proven\nhash="+hash+"\n" {
		t.Errorf("boot/manifest record = %q, %v", record, err)
	}
	state, err := os.ReadFile(filepath.Join(loader.tree.Dir, "modules", "loop", "state"))
	if err != nil || string(state) != "Builtin\n" {
		t.Errorf("modules/loop/state = %q, %v", state, err)
	}
}

func TestLiveLoadRefusesAStorageChange(t *testing.T) {
	changed := bootStorageSpec()
	changed.MachineState.Size = "128Mi"
	store, base, loader, hash := liveLoadFixture(t, []string{"loop"}, nil, changed)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a storage-changing manifest must stay staged for its proving boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", boot)
	}
}

func TestLiveLoadRefusesARetraction(t *testing.T) {
	store, base, loader, hash := liveLoadFixture(t, []string{"loop"}, []string{"loop", "zram"}, bootStorageSpec())

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a retracting manifest must stay staged for its reboot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", boot)
	}
}

func TestLiveLoadToleratesAStaleIntent(t *testing.T) {
	store, base, loader, _ := liveLoadFixture(t, []string{"loop"}, nil, bootStorageSpec())
	if err := store.WithdrawStaged(); err != nil {
		t.Fatal(err)
	}

	loader.apply(machine.ModulesIntent{ManifestHash: "whatever"}, store, base)

	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("a stale intent must change nothing: %+v", boot)
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
