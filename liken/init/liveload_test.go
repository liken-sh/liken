package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// liveLoadFixture stages the given spec in a temporary manifest store
// and returns the store, a module tree where "loop" is builtin so that
// no test needs a real kernel, a loader that writes a temporary facts
// tree, and the staged document's hash. The loader boots with
// bootStorageSpec and bootNetworkSpec, so a spec drifts only where a
// test changes it. boot/manifest starts as Proven with the hash
// "before", so a test can check that a refused load left it alone.
func liveLoadFixture(t *testing.T, staged machine.MachineSpec, bootModules []string) (machine.ManifestStore, string, *moduleLoader, string) {
	t.Helper()
	// A resident module takes no parameters, so the fixture fakes
	// /sys/module. Otherwise a module that is loaded on the machine
	// running the tests would change the outcome.
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

// liveLoadable is a spec that a live load accepts: the boot's storage
// and network, and one added module.
func liveLoadable() machine.MachineSpec {
	return machine.MachineSpec{
		Modules: []string{"loop"},
		Storage: bootStorageSpec(),
		Network: bootNetworkSpec(),
	}
}

// bootManifestRecord reads back the boot status that the loader wrote.
func bootManifestRecord(t *testing.T, loader *moduleLoader) machine.BootStatus {
	t.Helper()
	facts, err := loader.tree.Read()
	if err != nil {
		t.Fatal(err)
	}
	return facts.Boot
}

// bootStorageSpec is the storage that the fixture's machine booted with.
func bootStorageSpec() machine.StorageSpec {
	return machine.StorageSpec{
		MachineState: &machine.StorageRole{Device: "/dev/vda", Size: "64Mi"},
	}
}

// bootNetworkSpec is the network that the fixture's machine booted
// with: one interface on DHCP and one with a static address.
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
		t.Error("the staged manifest is still staged, want it promoted")
	}
	if proven, _ := store.LoadProven(); machine.ManifestHash(proven) != hash {
		t.Error("the proven manifest hash differs from the staged manifest hash, want them equal")
	}
	boot := bootManifestRecord(t, loader)
	if boot.ManifestHash != hash || boot.ManifestSource != machine.ManifestSourceProven {
		t.Errorf("the boot record is %+v, want the hash %s and source Proven", boot, hash)
	}
	if len(boot.Modules) != 1 || boot.Modules[0] != "loop" {
		t.Errorf("boot/modules is %v, want [loop]", boot.Modules)
	}
	// The facts are plain files: boot/manifest holds the source and
	// hash, and each module's outcome is a file of its own.
	record, err := os.ReadFile(filepath.Join(loader.tree.Dir, "boot", "manifest"))
	if err != nil || string(record) != "source=Proven\nhash="+hash+"\n" {
		t.Errorf("boot/manifest is %q (read error %v), want the source Proven and the hash", record, err)
	}
	state, err := os.ReadFile(filepath.Join(loader.tree.Dir, "modules", "loop", "state"))
	if err != nil || string(state) != "Builtin\n" {
		t.Errorf("modules/loop/state is %q (read error %v), want Builtin", state, err)
	}
}

func TestLiveLoadRefusesAStorageChange(t *testing.T) {
	staged := liveLoadable()
	staged.Storage.MachineState.Size = "128Mi"
	store, base, loader, hash := liveLoadFixture(t, staged, nil)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a manifest that changes storage was promoted, want it staged until a boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record is %+v, want it unchanged", boot)
	}
}

func TestLiveLoadRefusesANetworkChange(t *testing.T) {
	// init applies the network only at boot. A live load that went
	// ahead would promote a manifest whose network the machine never
	// applied, and the boot record would report a spec the machine is
	// not running.
	staged := liveLoadable()
	staged.Network.Interfaces[1].Address = "10.10.0.9/24"
	store, base, loader, hash := liveLoadFixture(t, staged, nil)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a manifest that changes the network was promoted, want it staged until a boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record is %+v, want it unchanged", boot)
	}
}

func TestLiveLoadRefusesARetraction(t *testing.T) {
	store, base, loader, hash := liveLoadFixture(t, liveLoadable(), []string{"loop", "zram"})

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a manifest that retracts a module was promoted, want it staged until a boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record is %+v, want it unchanged", boot)
	}
}

func TestLiveLoadToleratesAStaleIntent(t *testing.T) {
	store, base, loader, _ := liveLoadFixture(t, liveLoadable(), nil)
	if err := store.WithdrawStaged(); err != nil {
		t.Fatal(err)
	}

	loader.apply(machine.ModulesIntent{ManifestHash: "whatever"}, store, base)

	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record is %+v after a stale intent, want it unchanged", boot)
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
		t.Fatalf("merged statuses are %+v, want 2", merged)
	}
	byName := map[string]machine.ModuleStatus{}
	for _, s := range merged {
		byName[s.Name] = s
	}
	if byName["loop"].State != machine.ModuleLoaded || byName["loop"].Message != "" {
		t.Errorf("the status of loop is %+v, want the new Loaded outcome with no message", byName["loop"])
	}
	if byName["zram"].State != machine.ModuleLoaded {
		t.Errorf("the status of zram is %+v, want Loaded", byName["zram"])
	}
}

func TestLiveLoadRefusesAParameterChangeOnALoadedModule(t *testing.T) {
	// A module reads its parameters only when it loads, so a new
	// parameter for a module the boot already loaded needs a reboot.
	// The operator stages it for a reboot, and init refuses it on its
	// own check.
	staged := liveLoadable()
	staged.ModuleParameters = map[string]string{"loop.max_part": "8"}
	store, base, loader, hash := liveLoadFixture(t, staged, []string{"loop"})

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a parameter change on a loaded module was promoted, want it staged until a boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record is %+v, want it unchanged", boot)
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

// A live load loads its additions in spec.modules order, as a boot
// does. ModuleSetDiff returns them sorted, so a spec that lists them
// in reverse alphabetical order shows which order the load used.
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
		t.Errorf("modules loaded in the order %v, want %v", order, want)
	}
}

// boot/modules records the order this machine loaded the modules in,
// not spec.modules order. A live load appends what it loaded after
// the boot's modules, and a reorder waits for the next boot.
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
		t.Errorf("boot/modules is %v, want %v", boot.Modules, want)
	}
}

// The kernel ignores a resident module's parameters, so they stay out
// of the boot record. The rest of the spec still loads and promotes.
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
		t.Errorf("finit_module calls are %v, want %v", *calls, want)
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the manifest is still staged, want it promoted")
	}
	boot := bootManifestRecord(t, loader)
	if boot.ManifestHash != hash {
		t.Fatalf("the boot record is %+v, want the hash of the applied spec", boot)
	}
	if _, ok := boot.ModuleParameters["aardvark.mode"]; ok {
		t.Errorf("boot/moduleParameters is %v, want no aardvark.mode, which no load delivered", boot.ModuleParameters)
	}
	if boot.ModuleParameters["zebra.speed"] != "9" {
		t.Errorf("boot/moduleParameters is %v, want zebra.speed=9", boot.ModuleParameters)
	}
}

// The parameter that did not reach the kernel must show as drift,
// because the drift makes the operator stage the reboot that
// delivers it.
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
		t.Errorf("parameter drift is %v, want %v", diffs, want)
	}
}

// A module that loads for the first time receives its parameters, so
// the boot record keeps them.
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
		t.Errorf("finit_module calls are %v, want %v", *calls, want)
	}
	boot := bootManifestRecord(t, loader)
	if boot.ModuleParameters["aardvark.mode"] != "3" {
		t.Errorf("boot/moduleParameters is %v, want aardvark.mode=3", boot.ModuleParameters)
	}
}

func TestLiveLoadAppliesAModuleAddedWithItsParameters(t *testing.T) {
	staged := liveLoadable()
	staged.ModuleParameters = map[string]string{"loop.max_part": "8"}
	store, base, loader, hash := liveLoadFixture(t, staged, nil)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	boot := bootManifestRecord(t, loader)
	if boot.ManifestHash != hash {
		t.Fatalf("the boot record is %+v, want the hash of the applied spec", boot)
	}
	if boot.ModuleParameters["loop.max_part"] != "8" {
		t.Errorf("boot/moduleParameters is %v, want loop.max_part=8", boot.ModuleParameters)
	}
}
