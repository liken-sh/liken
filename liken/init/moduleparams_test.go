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

	"github.com/liken-sh/liken/liken/machine"
)

// fakeSysModules creates a stand-in for /sys/module with one
// directory for each loaded module and one file for each of its
// parameters, and points sysModuleDir at it for the test.
func fakeSysModules(t *testing.T, modules map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, parameters := range modules {
		dir := filepath.Join(root, name, "parameters")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the fake module directory: %v", err)
		}
		for parameter, value := range parameters {
			if err := os.WriteFile(filepath.Join(dir, parameter), []byte(value+"\n"), 0o644); err != nil {
				t.Fatalf("writing the fake parameter file: %v", err)
			}
		}
	}
	old := sysModuleDir
	sysModuleDir = root
	t.Cleanup(func() { sysModuleDir = old })
	return root
}

func TestModuleIsResidentFindsADirectoryUnderSysModule(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{"snd_hda_intel": {"power_save": "1"}})
	if !moduleIsResident(root, "snd_hda_intel") {
		t.Error("moduleIsResident is false for a module with a /sys/module directory, want true")
	}
	if moduleIsResident(root, "i915") {
		t.Error("moduleIsResident is true for a module with no /sys/module directory, want false")
	}
}

func TestModuleIsResidentNormalizesDashes(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{"snd_hda_intel": {}})
	if !moduleIsResident(root, "snd-hda-intel") {
		t.Error("moduleIsResident(\"snd-hda-intel\") is false, want true, because sysfs writes module names with underscores")
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
		t.Errorf("the result is %v, want %v", got, want)
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
		t.Errorf("the parameters are %v, want no powersave key, because a wrong parameter name has no file", got)
	}
	if got["power_save"] != "0" {
		t.Errorf("the parameters are %v, want power_save 0", got)
	}
}

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
		t.Errorf("the result is %v, want %v", got, want)
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
		t.Errorf("the result is %v, want %v", got, want)
	}
}

func TestUsableModuleParametersKeepsADeclarationTheKernelCanTake(t *testing.T) {
	declared := map[string]string{
		"snd_hda_intel.power_save": "0",
		"i915.enable_guc":          "3",
	}
	if got := usableModuleParameters(declared); !maps.Equal(got, declared) {
		t.Errorf("the result is %v, want %v", got, declared)
	}
}

func TestUsableModuleParametersKeepsNothingWhenNothingIsDeclared(t *testing.T) {
	if got := usableModuleParameters(nil); got != nil {
		t.Errorf("the result is %v, want nil", got)
	}
}

func TestReadModuleParametersReportsNothingForAnUndeclaredModule(t *testing.T) {
	root := fakeSysModules(t, map[string]map[string]string{"snd_hda_intel": {"power_save": "0"}})
	if got := readModuleParameters(root, "i915", map[string]string{"snd_hda_intel.power_save": "0"}); got != nil {
		t.Errorf("the parameters are %v, want nil", got)
	}
}

// declaredParameters declares two parameters of nvidia and one of
// loop, which declaredFixture marks as built in.
var declaredParameters = map[string]string{
	"nvidia.mode": "3",
	"nvidia.tune": "on",
	"loop.max_id": "8",
}

func TestDeclaredModuleOutcomesPassesTheSortedString(t *testing.T) {
	deps, builtin, pass, passed := declaredFixture("")
	declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, declaredParameters, pass)
	if passed["nvidia"] != "mode=3 tune=on" {
		t.Errorf("the string passed to nvidia is %q, want %q", passed["nvidia"], "mode=3 tune=on")
	}
}

func TestDeclaredModuleOutcomesPassesNothingForABuiltin(t *testing.T) {
	deps, builtin, pass, passed := declaredFixture("")
	statuses := declaredModuleOutcomes([]string{"loop"}, deps, builtin, declaredParameters, pass)
	if statuses[0].State != machine.ModuleBuiltin {
		t.Fatalf("the state is %s, want %s", statuses[0].State, machine.ModuleBuiltin)
	}
	if _, tried := passed["loop"]; tried {
		t.Errorf("the load received the parameters %q for a builtin, want none, because a builtin reads them from the kernel command line", passed["loop"])
	}
}

func TestDeclaredModuleOutcomesPassesNothingForAResidentModule(t *testing.T) {
	deps, builtin, pass, passed := declaredFixture("")
	pass.resident = func(name string) bool { return name == "nvidia" }
	statuses := declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, declaredParameters, pass)
	if !statuses[0].AlreadyResident {
		t.Error("AlreadyResident is false, want true because the module was resident before the load")
	}
	if statuses[0].State != machine.ModuleLoaded {
		t.Errorf("the state is %s, want Loaded for a module that is already resident", statuses[0].State)
	}
	if passed["nvidia"] != "" {
		t.Errorf("finit_module received %q for a resident module, want nothing, because it ignores the string", passed["nvidia"])
	}
}

func TestDeclaredModuleOutcomesNamesTheStringARefusedLoadGot(t *testing.T) {
	deps, builtin, pass, _ := declaredFixture("nvidia")
	pass.load = func(name, parameters string) error {
		return errors.New("finit_module kernel/nvidia.ko.zst: invalid argument")
	}
	statuses := declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, declaredParameters, pass)
	if statuses[0].State != machine.ModuleFailed {
		t.Fatalf("the state is %s, want %s", statuses[0].State, machine.ModuleFailed)
	}
	if !strings.Contains(statuses[0].Message, "invalid argument") ||
		!strings.Contains(statuses[0].Message, "mode=3 tune=on") {
		t.Errorf("the message is %q, want the errno and the string \"mode=3 tune=on\"", statuses[0].Message)
	}
}

func TestDeclaredModuleOutcomesReadsTheParametersBack(t *testing.T) {
	deps, builtin, pass, _ := declaredFixture("")
	pass.readParameters = func(name string) map[string]string {
		return map[string]string{"mode": "3", "tune": "Y"}
	}
	statuses := declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, declaredParameters, pass)
	if statuses[0].Parameters["tune"] != "Y" {
		t.Errorf("the parameters are %v, want tune=Y, as the kernel writes it", statuses[0].Parameters)
	}
}

func TestDeclaredModuleOutcomesReadsNothingBackForAMissingModule(t *testing.T) {
	deps, builtin, pass, _ := declaredFixture("")
	pass.readParameters = func(name string) map[string]string { return map[string]string{"mode": "3"} }
	statuses := declaredModuleOutcomes([]string{"nbd"}, deps, builtin, declaredParameters, pass)
	if statuses[0].State != machine.ModuleMissing || statuses[0].Parameters != nil {
		t.Errorf("the status is %+v, want Missing and no parameters for a module that is not in the kernel", statuses[0])
	}
}

func TestDeclaredModuleLineNamesTheStringItPassed(t *testing.T) {
	s := machine.ModuleStatus{Name: "nvidia", State: machine.ModuleLoaded}
	if got := declaredModuleLine(s, declaredParameters); got != "liken: modules: nvidia: loaded (mode=3 tune=on)" {
		t.Errorf("the line is %q, want it to end with (mode=3 tune=on)", got)
	}
}

func TestDeclaredModuleLineNamesNoStringForAResidentModule(t *testing.T) {
	s := machine.ModuleStatus{Name: "nvidia", State: machine.ModuleLoaded, AlreadyResident: true}
	if got := declaredModuleLine(s, declaredParameters); strings.Contains(got, "(mode=3 tune=on)") {
		t.Errorf("the console line is %q, want no (mode=3 tune=on), because a resident module never received the string", got)
	}
}

// A module that was already loaded must not print the same line as a
// clean load, because its parameters were not applied.
func TestDeclaredModuleLineSaysAResidentModuleMissedItsParameters(t *testing.T) {
	s := machine.ModuleStatus{Name: "nvidia", State: machine.ModuleLoaded, AlreadyResident: true}
	want := "liken: modules: nvidia: loaded: already in the kernel, so mode=3 tune=on did not reach it"
	if got := declaredModuleLine(s, declaredParameters); got != want {
		t.Errorf("the line is %q, want %q", got, want)
	}
}

// A built-in module must not print the same line as a clean load,
// because its parameters were not applied.
func TestDeclaredModuleLineSaysABuiltinMissedItsParameters(t *testing.T) {
	s := machine.ModuleStatus{Name: "loop", State: machine.ModuleBuiltin}
	want := "liken: modules: loop: builtin: the kernel builds it in, so max_id=8 did not reach it"
	if got := declaredModuleLine(s, declaredParameters); got != want {
		t.Errorf("the line is %q, want %q", got, want)
	}
}

func TestDeclaredModuleLineSaysNothingExtraForABuiltinWithNoParameters(t *testing.T) {
	s := machine.ModuleStatus{Name: "binfmt_misc", State: machine.ModuleBuiltin}
	if got := declaredModuleLine(s, declaredParameters); got != "liken: modules: binfmt_misc: builtin" {
		t.Errorf("the line is %q, want it to add nothing for a builtin with no parameters", got)
	}
}

func TestDeclaredModuleLineSaysNothingExtraForAResidentModuleWithNoParameters(t *testing.T) {
	s := machine.ModuleStatus{Name: "nbd", State: machine.ModuleLoaded, AlreadyResident: true}
	if got := declaredModuleLine(s, declaredParameters); got != "liken: modules: nbd: loaded" {
		t.Errorf("the line is %q, want it to add nothing for a resident module with no parameters", got)
	}
}

func TestDeclaredModuleLineKeepsTheMessage(t *testing.T) {
	s := machine.ModuleStatus{Name: "nbd", State: machine.ModuleMissing, Message: "check the spelling"}
	if got := declaredModuleLine(s, nil); got != "liken: modules: nbd: missing: check the spelling" {
		t.Errorf("the line is %q, want it to keep the message", got)
	}
}

// fakeFinitModule replaces the finit_module call with a recorder of
// the file name and parameter string of each call.
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
			t.Fatalf("writing the fake module file: %v", err)
		}
	}
	calls := fakeFinitModule(t)
	deps := map[string][]string{"nvidia": {"nvidia.ko.zst", "nvidia_helper.ko.zst"}}
	if _, err := loadModule(base, "nvidia", "mode=3", deps, map[string]bool{}); err != nil {
		t.Fatalf("loading the module: %v", err)
	}
	want := [][2]string{{"nvidia_helper.ko.zst", ""}, {"nvidia.ko.zst", "mode=3"}}
	if !slices.Equal(*calls, want) {
		t.Errorf("the result is %v, want %v", *calls, want)
	}
}
