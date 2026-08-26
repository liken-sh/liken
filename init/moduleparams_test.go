package main

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// fakeSysModules builds the part of /sys/module a readback reads: one
// directory for each resident module, each holding a file for each
// parameter the kernel exposes. The tree replaces sysModuleDir for the
// test, so nothing reads the machine's real /sys.
func fakeSysModules(t *testing.T, modules map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, parameters := range modules {
		dir := filepath.Join(root, name, "parameters")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for parameter, value := range parameters {
			if err := os.WriteFile(filepath.Join(dir, parameter), []byte(value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	old := sysModuleDir
	sysModuleDir = root
	t.Cleanup(func() { sysModuleDir = old })
	return root
}

func TestModuleIsResidentSeesADirectoryUnderSysModule(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{"snd_hda_intel": {"power_save": "1"}})
	if !moduleIsResident(root, "snd_hda_intel") {
		t.Error("a module with a /sys/module directory is resident")
	}
	if moduleIsResident(root, "i915") {
		t.Error("a module with no directory is not resident")
	}
}

func TestModuleIsResidentNormalizesDashes(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{"snd_hda_intel": {}})
	if !moduleIsResident(root, "snd-hda-intel") {
		t.Error("sysfs spells a module name with underscores")
	}
}

func TestReadModuleParametersReadsTheDeclaredKeys(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{
		"snd_hda_intel": {"power_save": "0", "enable_msi": "Y", "id": "unused"},
	})
	declared := map[string]string{
		"snd_hda_intel.power_save": "0",
		"snd_hda_intel.enable_msi": "1",
		"i915.enable_guc":          "3",
	}
	got := readModuleParameters(root, "snd_hda_intel", declared)
	want := map[string]string{"power_save": "0", "enable_msi": "Y"}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReadModuleParametersOmitsAParameterWithNoFile(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{"snd_hda_intel": {"power_save": "0"}})
	declared := map[string]string{
		"snd_hda_intel.power_save": "0",
		"snd_hda_intel.powersave":  "0",
	}
	got := readModuleParameters(root, "snd_hda_intel", declared)
	if _, present := got["powersave"]; present {
		t.Errorf("a wrong parameter name has no file and no key: %v", got)
	}
	if got["power_save"] != "0" {
		t.Errorf("got %v", got)
	}
}

// /sys/module/acpi/parameters/debug_level reads back 1313 bytes on an
// ordinary machine, past the 1024 the CRD allows a status value. One
// value the API server refuses makes the whole status update fail, so
// the readback is cut to what the schema takes.
func TestReadModuleParametersClampsAnOversizeValue(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{
		"acpi": {"debug_level": strings.Repeat("a", 1313)},
	})
	got := readModuleParameters(root, "acpi", map[string]string{"acpi.debug_level": "0"})
	if len(got["debug_level"]) != moduleParameterValueMax {
		t.Errorf("read back %d bytes, want %d", len(got["debug_level"]), moduleParameterValueMax)
	}
}

func TestUsableModuleParametersDropsAnEmptyValue(t *testing.T) {
	got := usableModuleParameters(map[string]string{
		"snd_hda_intel.power_save": "0",
		"i915.enable_guc":          "",
	})
	want := map[string]string{"snd_hda_intel.power_save": "0"}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUsableModuleParametersDropsAnOverLongKey(t *testing.T) {
	long := strings.Repeat("a", 65) + "." + strings.Repeat("b", 65)
	got := usableModuleParameters(map[string]string{
		"snd_hda_intel.power_save": "0",
		long:                       "1",
	})
	want := map[string]string{"snd_hda_intel.power_save": "0"}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUsableModuleParametersKeepsADeclarationTheKernelCanTake(t *testing.T) {
	declared := map[string]string{
		"snd_hda_intel.power_save": "0",
		"i915.enable_guc":          "3",
	}
	if got := usableModuleParameters(declared); !maps.Equal(got, declared) {
		t.Errorf("got %v, want %v", got, declared)
	}
}

func TestUsableModuleParametersKeepsNothingWhenNothingIsDeclared(t *testing.T) {
	if got := usableModuleParameters(nil); got != nil {
		t.Errorf("got %v", got)
	}
}

func TestReadModuleParametersReportsNothingForAnUndeclaredModule(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{"snd_hda_intel": {"power_save": "0"}})
	if got := readModuleParameters(root, "i915", map[string]string{"snd_hda_intel.power_save": "0"}); got != nil {
		t.Errorf("got %v", got)
	}
}

// declaredParameters is the spec half of the parameter tests: two
// parameters on one module, and one on a module that this kernel
// builds in.
var declaredParameters = map[string]string{
	"nvidia.mode": "3",
	"nvidia.tune": "on",
	"loop.max_id": "8",
}

func TestDeclaredModuleOutcomesPassesTheSortedString(t *testing.T) {
	deps, builtin, pass, passed := declaredFixture("")
	declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, declaredParameters, pass)
	if passed["nvidia"] != "mode=3 tune=on" {
		t.Errorf("got %q", passed["nvidia"])
	}
}

func TestDeclaredModuleOutcomesPassesNothingForABuiltin(t *testing.T) {
	deps, builtin, pass, passed := declaredFixture("")
	statuses := declaredModuleOutcomes([]string{"loop"}, deps, builtin, declaredParameters, pass)
	if statuses[0].State != machine.ModuleBuiltin {
		t.Fatalf("got %s", statuses[0].State)
	}
	if _, tried := passed["loop"]; tried {
		t.Error("a builtin takes its parameters from the kernel command line, not from a load")
	}
}

func TestDeclaredModuleOutcomesPassesNothingForAResidentModule(t *testing.T) {
	deps, builtin, pass, passed := declaredFixture("")
	pass.resident = func(name string) bool { return name == "nvidia" }
	statuses := declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, declaredParameters, pass)
	if !statuses[0].AlreadyResident {
		t.Error("the status must carry the residency the loader read before the load")
	}
	if statuses[0].State != machine.ModuleLoaded {
		t.Errorf("a resident module is still loaded: %s", statuses[0].State)
	}
	if passed["nvidia"] != "" {
		t.Errorf("finit_module ignores the string for a resident module: %q", passed["nvidia"])
	}
}

func TestDeclaredModuleOutcomesNamesTheStringARefusedLoadGot(t *testing.T) {
	deps, builtin, pass, _ := declaredFixture("nvidia")
	pass.load = func(name, parameters string) error {
		return errors.New("finit_module kernel/nvidia.ko.zst: invalid argument")
	}
	statuses := declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, declaredParameters, pass)
	if statuses[0].State != machine.ModuleFailed {
		t.Fatalf("got %s", statuses[0].State)
	}
	if !strings.Contains(statuses[0].Message, "invalid argument") ||
		!strings.Contains(statuses[0].Message, "mode=3 tune=on") {
		t.Errorf("the message names the errno and the string: %q", statuses[0].Message)
	}
}

func TestDeclaredModuleOutcomesReadsTheParametersBack(t *testing.T) {
	deps, builtin, pass, _ := declaredFixture("")
	pass.readback = func(name string) map[string]string {
		return map[string]string{"mode": "3", "tune": "Y"}
	}
	statuses := declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, declaredParameters, pass)
	if statuses[0].Parameters["tune"] != "Y" {
		t.Errorf("the readback reports the kernel's own rendering: %v", statuses[0].Parameters)
	}
}

func TestDeclaredModuleOutcomesReadsNothingBackForAMissingModule(t *testing.T) {
	deps, builtin, pass, _ := declaredFixture("")
	pass.readback = func(name string) map[string]string { return map[string]string{"mode": "3"} }
	statuses := declaredModuleOutcomes([]string{"nbd"}, deps, builtin, declaredParameters, pass)
	if statuses[0].State != machine.ModuleMissing || statuses[0].Parameters != nil {
		t.Errorf("a module that is not in the kernel has nothing to read back: %+v", statuses[0])
	}
}

func TestDeclaredModuleLineNamesTheStringItPassed(t *testing.T) {
	s := machine.ModuleStatus{Name: "nvidia", State: machine.ModuleLoaded}
	if got := declaredModuleLine(s, declaredParameters); got != "liken: modules: nvidia: loaded (mode=3 tune=on)" {
		t.Errorf("got %q", got)
	}
}

func TestDeclaredModuleLineNamesNoStringForAResidentModule(t *testing.T) {
	s := machine.ModuleStatus{Name: "nvidia", State: machine.ModuleLoaded, AlreadyResident: true}
	if got := declaredModuleLine(s, declaredParameters); strings.Contains(got, "(mode=3 tune=on)") {
		t.Errorf("a resident module never received the string: %q", got)
	}
}

// A resident module's line read exactly like a clean load, which hid
// the one case the console could report at the moment it happened.
func TestDeclaredModuleLineSaysAResidentModuleMissedItsParameters(t *testing.T) {
	s := machine.ModuleStatus{Name: "nvidia", State: machine.ModuleLoaded, AlreadyResident: true}
	want := "liken: modules: nvidia: loaded: already in the kernel, so mode=3 tune=on did not reach it"
	if got := declaredModuleLine(s, declaredParameters); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A builtin's line read exactly like a clean load, the same way a
// resident module's line did, and the console is the live record on
// a machine with no shell.
func TestDeclaredModuleLineSaysABuiltinMissedItsParameters(t *testing.T) {
	s := machine.ModuleStatus{Name: "loop", State: machine.ModuleBuiltin}
	want := "liken: modules: loop: builtin: the kernel builds it in, so max_id=8 did not reach it"
	if got := declaredModuleLine(s, declaredParameters); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeclaredModuleLineSaysNothingExtraForABuiltinWithNoParameters(t *testing.T) {
	s := machine.ModuleStatus{Name: "binfmt_misc", State: machine.ModuleBuiltin}
	if got := declaredModuleLine(s, declaredParameters); got != "liken: modules: binfmt_misc: builtin" {
		t.Errorf("got %q", got)
	}
}

func TestDeclaredModuleLineSaysNothingExtraForAResidentModuleWithNoParameters(t *testing.T) {
	s := machine.ModuleStatus{Name: "nbd", State: machine.ModuleLoaded, AlreadyResident: true}
	if got := declaredModuleLine(s, declaredParameters); got != "liken: modules: nbd: loaded" {
		t.Errorf("got %q", got)
	}
}

func TestDeclaredModuleLineKeepsTheMessage(t *testing.T) {
	s := machine.ModuleStatus{Name: "nbd", State: machine.ModuleMissing, Message: "check the spelling"}
	if got := declaredModuleLine(s, nil); got != "liken: modules: nbd: missing: check the spelling" {
		t.Errorf("got %q", got)
	}
}

// fakeFinitModule replaces the load syscall with a recorder, so a test
// reads which file got which parameter string.
func fakeFinitModule(t *testing.T) *[][2]string {
	t.Helper()
	var calls [][2]string
	old := finitModule
	finitModule = func(fd int, params string, flags int) error {
		name, err := os.Readlink(filepath.Join("/proc/self/fd", strconv.Itoa(fd)))
		if err != nil {
			name = ""
		}
		calls = append(calls, [2]string{filepath.Base(name), params})
		return unix.EEXIST
	}
	t.Cleanup(func() { finitModule = old })
	return &calls
}

func TestLoadModuleGivesTheParametersToTheModuleItself(t *testing.T) {
	base := t.TempDir()
	files := []string{"nvidia.ko.zst", "nvidia_helper.ko.zst"}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(base, file), []byte("module"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	calls := fakeFinitModule(t)
	deps := map[string][]string{"nvidia": {"nvidia.ko.zst", "nvidia_helper.ko.zst"}}
	if _, err := loadModule(base, "nvidia", "mode=3", deps, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"nvidia_helper.ko.zst", ""}, {"nvidia.ko.zst", "mode=3"}}
	if !slices.Equal(*calls, want) {
		t.Errorf("got %v, want %v", *calls, want)
	}
}
