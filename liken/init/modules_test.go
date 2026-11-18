package main

// These tests load no module into a real kernel. A QEMU guest tests
// that.

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the test file: %v", err)
	}
	return path
}

func writeBytes(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing the test file: %v", err)
	}
	return path
}

func TestReadModuleListSkipsCommentsAndBlanks(t *testing.T) {
	path := writeFile(t, "modules.list", "# storage\nvirtio_blk\n\n  ext4  \n")
	names, err := readModuleList(path)
	if err != nil {
		t.Fatalf("reading the module list: %v", err)
	}
	if !slices.Equal(names, []string{"virtio_blk", "ext4"}) {
		t.Errorf("the names are %v, want virtio_blk and ext4", names)
	}
}

func TestReadModuleListToleratesAMissingFile(t *testing.T) {
	names, err := readModuleList(filepath.Join(t.TempDir(), "absent"))
	if names != nil || err != nil {
		t.Errorf("a missing list returned %v and error %v, want no names and no error", names, err)
	}
}

func TestReadModulesDepParsesTheIndex(t *testing.T) {
	path := writeFile(t, "modules.dep",
		"kernel/fs/overlayfs/overlay.ko.zst: kernel/a.ko.zst kernel/b.ko.zst\n"+
			"kernel/drivers/block/virtio-blk.ko.zst:\n"+
			"not an entry\n")
	deps, err := readModulesDep(path)
	if err != nil {
		t.Fatalf("reading modules.dep: %v", err)
	}
	overlay := deps["overlay"]
	if !slices.Equal(overlay, []string{"kernel/fs/overlayfs/overlay.ko.zst", "kernel/a.ko.zst", "kernel/b.ko.zst"}) {
		t.Errorf("the entry of overlay is %v, want its file then a.ko.zst and b.ko.zst", overlay)
	}
	if _, ok := deps["virtio_blk"]; !ok {
		t.Errorf("the index is %v, want a virtio_blk key for virtio-blk", deps)
	}
}

func TestModuleNameStripsExtensionsAndNormalizes(t *testing.T) {
	if got := moduleName("kernel/drivers/block/virtio-blk.ko.zst"); got != "virtio_blk" {
		t.Errorf("the name is %q, want virtio_blk", got)
	}
	if got := moduleName("kernel/fs/ext4.ko"); got != "ext4" {
		t.Errorf("the name is %q, want ext4", got)
	}
}

func TestReadModulesBuiltinKeysLikeTheDepIndex(t *testing.T) {
	path := writeFile(t, "modules.builtin",
		"kernel/fs/binfmt_misc.ko\nkernel/drivers/char/hw_random/rng-core.ko\n\n")
	builtin, err := readModulesBuiltin(path)
	if err != nil {
		t.Fatalf("reading modules.builtin: %v", err)
	}
	if !builtin["binfmt_misc"] {
		t.Errorf("the builtin set is %v, want binfmt_misc in it", builtin)
	}
	if !builtin["rng_core"] {
		t.Errorf("the builtin set is %v, want rng_core for rng-core", builtin)
	}
}

// declaredFixture returns a modules.dep with nvidia, a
// modules.builtin with loop, and a declaredPass whose load fails for
// the module named by failing. The last return value records the
// parameter string that each load passed.
func declaredFixture(failing string) (map[string][]string, map[string]bool, declaredPass, map[string]string) {
	deps := map[string][]string{"nvidia": {"kernel/nvidia.ko.zst"}}
	builtin := map[string]bool{"loop": true}
	passed := map[string]string{}
	pass := declaredPass{
		resident: func(string) bool { return false },
		load: func(name, parameters string) error {
			passed[name] = parameters
			if name == failing {
				return errors.New("finit_module: no such device")
			}
			return nil
		},
		readParameters: func(string) map[string]string { return nil },
	}
	return deps, builtin, pass, passed
}

func TestDeclaredModuleOutcomes(t *testing.T) {
	deps, builtin, pass, _ := declaredFixture("")
	statuses := declaredModuleOutcomes([]string{"nvidia", "loop", "nbd"}, deps, builtin, nil, pass)
	states := []machine.ModuleState{machine.ModuleLoaded, machine.ModuleBuiltin, machine.ModuleMissing}
	for i, want := range states {
		if statuses[i].State != want {
			t.Errorf("%s is %s, want %s", statuses[i].Name, statuses[i].State, want)
		}
	}
	if statuses[2].Message == "" {
		t.Error("the message of a missing module is empty, want a message that names the fix")
	}
}

func TestDeclaredModuleOutcomesReportsKernelRefusals(t *testing.T) {
	deps, builtin, pass, _ := declaredFixture("nvidia")
	statuses := declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, nil, pass)
	if statuses[0].State != machine.ModuleFailed {
		t.Errorf("the state is %s, want Failed", statuses[0].State)
	}
	if statuses[0].Message != "finit_module: no such device" {
		t.Errorf("the message is %q, want the error of the load", statuses[0].Message)
	}
}

func TestDeclaredModuleOutcomesNormalizesDashes(t *testing.T) {
	_, builtin, pass, _ := declaredFixture("")
	deps := map[string][]string{"rng_core": {"kernel/rng-core.ko.zst"}}
	statuses := declaredModuleOutcomes([]string{"rng-core"}, deps, builtin, nil, pass)
	if statuses[0].State != machine.ModuleLoaded {
		t.Errorf("the state is %s, want Loaded", statuses[0].State)
	}
}

func TestLoadDeclaredModulesFromAFabricatedTree(t *testing.T) {
	base := t.TempDir()
	deps := "kernel/drivers/gpu/nvidia.ko.zst:\n"
	if err := os.WriteFile(filepath.Join(base, "modules.dep"), []byte(deps), 0o644); err != nil {
		t.Fatalf("writing modules.dep: %v", err)
	}
	builtin := "kernel/block/loop.ko\n"
	if err := os.WriteFile(filepath.Join(base, "modules.builtin"), []byte(builtin), 0o644); err != nil {
		t.Fatalf("writing modules.builtin: %v", err)
	}
	// modules.dep lists nvidia, but the tree has no file for it, so
	// the load fails without a kernel.
	statuses := loadDeclaredModulesFrom(base, []string{"nvidia", "loop", "nbd"}, nil)
	states := []machine.ModuleState{machine.ModuleFailed, machine.ModuleBuiltin, machine.ModuleMissing}
	for i, want := range states {
		if statuses[i].State != want {
			t.Errorf("%s is %s, want %s", statuses[i].Name, statuses[i].State, want)
		}
	}
}

func TestLoadDeclaredModulesWithNothingDeclared(t *testing.T) {
	if statuses := loadDeclaredModules(nil, nil); statuses != nil {
		t.Errorf("with no declared modules the statuses are %v, want nil", statuses)
	}
}

func TestLoadDeclaredModulesFromAMissingTree(t *testing.T) {
	statuses := loadDeclaredModulesFrom(filepath.Join(t.TempDir(), "absent"), []string{"nvidia"}, nil)
	if len(statuses) != 1 || statuses[0].State != machine.ModuleMissing {
		t.Errorf("with no modules.dep the statuses are %v, want one Missing status", statuses)
	}
}

func TestKernelReleaseIsNotEmpty(t *testing.T) {
	if release := kernelRelease(); release == "" {
		t.Error("the kernel release is empty, want the uname release string")
	}
}

// fakeModulesConf makes loadModules read the list at path, and
// restores modulesConf when the test ends.
func fakeModulesConf(t *testing.T, path string) {
	t.Helper()
	old := modulesConf
	modulesConf = path
	t.Cleanup(func() { modulesConf = old })
}

func TestLoadModulesWithNoListLoadsNothing(t *testing.T) {
	fakeModulesConf(t, filepath.Join(t.TempDir(), "absent.conf"))
	loadModules()
}

func TestLoadModulesWithAnEmptyListLoadsNothing(t *testing.T) {
	fakeModulesConf(t, writeFile(t, "modules.conf", "# nothing but commentary\n"))
	loadModules()
}

func TestLoadModulesReportsAnUnreadableList(t *testing.T) {
	path := writeFile(t, "modules.conf", "overlay\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("making the list unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	fakeModulesConf(t, path)
	loadModules()
}

func TestLoadModuleReportsAMissingModuleFile(t *testing.T) {
	base := t.TempDir()
	deps := map[string][]string{"overlay": {"kernel/fs/overlayfs/overlay.ko.zst"}}
	n, err := loadModule(base, "overlay", "", deps, map[string]bool{})
	if err == nil || n != 0 {
		t.Errorf("loading a module with no file loaded %d files with error %v, want 0 files and an error", n, err)
	}
}

func TestLoadModuleReportsAKernelRefusal(t *testing.T) {
	// finit_module fails, because the test runs unprivileged or
	// because the file is not a module.
	base := t.TempDir()
	rel := "kernel/fake.ko.zst"
	if err := os.MkdirAll(filepath.Join(base, "kernel"), 0o755); err != nil {
		t.Fatalf("creating the kernel directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, rel), []byte("not a module"), 0o644); err != nil {
		t.Fatalf("writing the fake module: %v", err)
	}
	deps := map[string][]string{"fake": {rel}}
	n, err := loadModule(base, "fake", "", deps, map[string]bool{})
	if err == nil || !strings.Contains(err.Error(), "finit_module") || n != 0 {
		t.Errorf("loading a file that is not a module loaded %d files with error %v, want 0 files and a finit_module error", n, err)
	}
}

func TestLoadModuleSkipsFilesAlreadyLoaded(t *testing.T) {
	// The file does not exist, so an open would fail.
	deps := map[string][]string{"overlay": {"kernel/fs/overlayfs/overlay.ko.zst"}}
	loaded := map[string]bool{"kernel/fs/overlayfs/overlay.ko.zst": true}
	n, err := loadModule(t.TempDir(), "overlay", "", deps, loaded)
	if err != nil || n != 0 {
		t.Errorf("loading a chain that is already loaded loaded %d files with error %v, want 0 files and no error", n, err)
	}
}
