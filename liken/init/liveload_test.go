package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// liveLoadFixture builds everything that a live load touches: a
// manifest store holding the given spec as its staged Machine
// document, a fabricated module tree where "loop" is builtin (so
// outcomes need no real kernel), and a module loader that publishes
// into a temporary facts tree. The loader's boot values are the ones
// bootStorageSpec and bootNetworkSpec describe, so a staged spec
// drifts from the boot only where a test makes it drift. The tree is
// seeded with boot/manifest = Proven/before, so a refused load leaves a
// record to prove the boot state stayed untouched. It returns the
// store, the module tree, the loader, and the staged document's hash.
func liveLoadFixture(t *testing.T, staged machine.MachineSpec, bootModules []string) (machine.ManifestStore, string, *moduleLoader, string) {
	t.Helper()
	// Residency decides whether a load can carry parameters, so the
	// fixture answers that question from a tree of its own. Without
	// this, a module the machine running the tests happens to hold
	// would change the outcome.
	fakeSysModules(t, nil)

	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "modules.dep"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "modules.builtin"), []byte("kernel/block/loop.ko\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	doc := machine.Machine{
		APIVersion: api.APIVersion,
		Kind:       "Machine",
		Metadata:   api.ObjectMeta{Name: "lab"},
		Spec:       staged,
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
		bootStorage: bootStorageSpec(),
		bootNetwork: bootNetworkSpec(),
		bootModules: bootModules,
	}
	return store, base, loader, machine.ManifestHash(raw)
}

// liveLoadable is the spec a live load accepts: the boot's own storage
// and network, plus one module to add.
func liveLoadable() machine.MachineSpec {
	return machine.MachineSpec{
		Modules: []string{"loop"},
		Storage: bootStorageSpec(),
		Network: bootNetworkSpec(),
	}
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

// bootStorageSpec is the storage that the fixture's boot actuated.
// Staging the same spec means no storage drift.
func bootStorageSpec() machine.StorageSpec {
	return machine.StorageSpec{
		MachineState: &machine.StorageRole{Device: "/dev/vda", Size: "64Mi"},
	}
}

// bootNetworkSpec is the network that the fixture's boot actuated: a
// DHCP uplink and a static cluster segment, the shape a lab machine
// has.
func bootNetworkSpec() machine.NetworkSpec {
	return machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth1", Address: "10.10.0.1/24"},
	}}
}

func TestLiveLoadAppliesAnAdditiveSpec(t *testing.T) {
	store, base, loader, hash := liveLoadFixture(t, liveLoadable(), nil)

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
	staged := liveLoadable()
	staged.Storage.MachineState.Size = "128Mi"
	store, base, loader, hash := liveLoadFixture(t, staged, nil)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a storage-changing manifest must stay staged for its proving boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", boot)
	}
}

func TestLiveLoadRefusesANetworkChange(t *testing.T) {
	// Nothing in the running kernel re-addresses an interface, and the
	// operator refuses the same combination for the same reason. If
	// this load went ahead, it would promote a manifest whose network
	// the machine never applied, and the boot record would then claim
	// a spec the machine is not running.
	staged := liveLoadable()
	staged.Network.Interfaces[1].Address = "10.10.0.9/24"
	store, base, loader, hash := liveLoadFixture(t, staged, nil)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a network-changing manifest must stay staged for its boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", boot)
	}
}

func TestLiveLoadRefusesARetraction(t *testing.T) {
	store, base, loader, hash := liveLoadFixture(t, liveLoadable(), []string{"loop", "zram"})

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a retracting manifest must stay staged for its reboot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", boot)
	}
}

func TestLiveLoadToleratesAStaleIntent(t *testing.T) {
	store, base, loader, _ := liveLoadFixture(t, liveLoadable(), nil)
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

func TestLiveLoadRefusesAParameterChangeOnALoadedModule(t *testing.T) {
	// A module reads its parameters once, when it loads, so a change
	// on a module the boot already loaded cannot apply in place. The
	// operator classifies it as reboot-class, and this refusal is
	// init's own derivation of the same judgment.
	staged := liveLoadable()
	staged.ModuleParameters = map[string]string{"loop.max_part": "8"}
	store, base, loader, hash := liveLoadFixture(t, staged, []string{"loop"})

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a parameter change on a loaded module must stay staged for its reboot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", boot)
	}
}

// fakeModuleTree writes a loadable module file for each name and a
// modules.dep entry with no dependencies, so that a declared load
// reaches finit_module instead of stopping at Missing.
func fakeModuleTree(t *testing.T, base string, names ...string) {
	t.Helper()
	var dep strings.Builder
	for _, name := range names {
		file := name + ".ko.zst"
		if err := os.WriteFile(filepath.Join(base, file), []byte("module"), 0o644); err != nil {
			t.Fatal(err)
		}
		dep.WriteString(file + ":\n")
	}
	if err := os.WriteFile(filepath.Join(base, "modules.dep"), []byte(dep.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A reboot loads spec.modules in the order the manifest lists, and a
// live load must load the same additions in the same order. The diff
// reports the added set sorted, so a manifest whose order runs against
// the alphabet proves the load follows the manifest.
func TestLiveLoadLoadsInTheOrderTheManifestLists(t *testing.T) {
	staged := liveLoadable()
	staged.Modules = []string{"zebra", "aardvark"}
	store, base, loader, hash := liveLoadFixture(t, staged, nil)
	fakeModuleTree(t, base, "zebra", "aardvark")
	calls := fakeFinitModule(t)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	var order []string
	for _, call := range *calls {
		order = append(order, call[0])
	}
	want := []string{"zebra.ko.zst", "aardvark.ko.zst"}
	if !slices.Equal(order, want) {
		t.Errorf("loaded %v, want %v", order, want)
	}
}

// boot/modules records the order this machine loaded, not the order
// the manifest lists. The modules the boot loaded keep their places
// in the running kernel, so a live load appends what it loaded and
// leaves a declared reorder to the next boot.
func TestLiveLoadRecordsTheOrderTheMachineLoaded(t *testing.T) {
	staged := liveLoadable()
	staged.Modules = []string{"aardvark", "zebra", "badger"}
	store, base, loader, hash := liveLoadFixture(t, staged, []string{"zebra"})
	fakeModuleTree(t, base, "aardvark", "badger")
	fakeFinitModule(t)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	boot := bootManifestRecord(t, loader)
	want := []string{"zebra", "aardvark", "badger"}
	if !slices.Equal(boot.Modules, want) {
		t.Errorf("boot/modules = %v, want %v", boot.Modules, want)
	}
}

// A resident module loads nothing and its parameter must stay out
// of the boot record, while the rest of the edit still applies and
// promotes.
func TestLiveLoadAppliesAroundAResidentModule(t *testing.T) {
	staged := liveLoadable()
	staged.Modules = []string{"aardvark", "zebra"}
	staged.ModuleParameters = map[string]string{"aardvark.mode": "3", "zebra.speed": "9"}
	store, base, loader, hash := liveLoadFixture(t, staged, nil)
	fakeModuleTree(t, base, "aardvark", "zebra")
	fakeSysModules(t, map[string]map[string]string{"aardvark": {}})
	calls := fakeFinitModule(t)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	want := [][2]string{{"aardvark.ko.zst", ""}, {"zebra.ko.zst", "speed=9"}}
	if !slices.Equal(*calls, want) {
		t.Errorf("loaded %v, want %v", *calls, want)
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the manifest should have been promoted")
	}
	boot := bootManifestRecord(t, loader)
	if boot.ManifestHash != hash {
		t.Fatalf("the spec should have applied in place: %+v", boot)
	}
	if _, ok := boot.ModuleParameters["aardvark.mode"]; ok {
		t.Errorf("an undelivered parameter must not be recorded: %v", boot.ModuleParameters)
	}
	if boot.ModuleParameters["zebra.speed"] != "9" {
		t.Errorf("boot/moduleParameters = %v", boot.ModuleParameters)
	}
}

// The record the load writes must leave exactly one drift line for
// the undelivered key, because that line is what makes the operator
// stage the reboot that can deliver it.
func TestLiveLoadLeavesAnUndeliveredParameterAsDrift(t *testing.T) {
	staged := liveLoadable()
	staged.Modules = []string{"aardvark"}
	staged.ModuleParameters = map[string]string{"aardvark.mode": "3"}
	store, base, loader, hash := liveLoadFixture(t, staged, nil)
	fakeModuleTree(t, base, "aardvark")
	fakeSysModules(t, map[string]map[string]string{"aardvark": {}})
	fakeFinitModule(t)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	boot := bootManifestRecord(t, loader)
	diffs := machine.ModuleParameterDrift(staged.Modules, boot.Modules,
		staged.ModuleParameters, boot.ModuleParameters)
	want := []string{"module parameter aardvark.mode: 3 declared but not actuated"}
	if !slices.Equal(diffs, want) {
		t.Errorf("drift = %v, want %v", diffs, want)
	}
}

// A module loading for the first time takes its parameters at the
// load, so the record keeps them and nothing drifts.
func TestLiveLoadRecordsAParameterItDelivered(t *testing.T) {
	staged := liveLoadable()
	staged.Modules = []string{"aardvark"}
	staged.ModuleParameters = map[string]string{"aardvark.mode": "3"}
	store, base, loader, hash := liveLoadFixture(t, staged, nil)
	fakeModuleTree(t, base, "aardvark")
	calls := fakeFinitModule(t)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	want := [][2]string{{"aardvark.ko.zst", "mode=3"}}
	if !slices.Equal(*calls, want) {
		t.Errorf("loaded %v, want %v", *calls, want)
	}
	boot := bootManifestRecord(t, loader)
	if boot.ModuleParameters["aardvark.mode"] != "3" {
		t.Errorf("boot/moduleParameters = %v", boot.ModuleParameters)
	}
}

func TestLiveLoadAppliesAModuleAddedWithItsParameters(t *testing.T) {
	staged := liveLoadable()
	staged.ModuleParameters = map[string]string{"loop.max_part": "8"}
	store, base, loader, hash := liveLoadFixture(t, staged, nil)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	boot := bootManifestRecord(t, loader)
	if boot.ManifestHash != hash {
		t.Fatalf("the spec should have applied in place: %+v", boot)
	}
	if boot.ModuleParameters["loop.max_part"] != "8" {
		t.Errorf("boot/moduleParameters = %v", boot.ModuleParameters)
	}
}
