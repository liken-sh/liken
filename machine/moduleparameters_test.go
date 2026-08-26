package machine

import (
	"slices"
	"testing"
)

// parameterFixture is the declaration two modules share in these
// tests: one module with two parameters, one with a single parameter,
// and one key whose module half names nothing that loads.
var parameterFixture = map[string]string{
	"snd_hda_intel.power_save":            "0",
	"snd_hda_intel.enable_msi":            "1",
	"i915.enable_guc":                     "3",
	"drm.debug":                           "0x1e",
	"comma.array":                         "1,2,3",
	"snd_hda_intel.power_save_controller": "N",
}

func TestModuleParameterStringSortsByParameterName(t *testing.T) {
	got := ModuleParameterString("snd_hda_intel", parameterFixture)
	want := "enable_msi=1 power_save=0 power_save_controller=N"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestModuleParameterStringTakesOnlyItsOwnModule(t *testing.T) {
	if got := ModuleParameterString("i915", parameterFixture); got != "enable_guc=3" {
		t.Errorf("got %q", got)
	}
}

func TestModuleParameterStringKeepsACommaSeparatedArray(t *testing.T) {
	if got := ModuleParameterString("comma", parameterFixture); got != "array=1,2,3" {
		t.Errorf("got %q", got)
	}
}

func TestModuleParameterStringIsEmptyForAnUndeclaredModule(t *testing.T) {
	if got := ModuleParameterString("e1000e", parameterFixture); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestModuleParameterStringSpellsTheModuleExactly(t *testing.T) {
	parameters := map[string]string{"snd-hda-intel.power_save": "0"}
	if got := ModuleParameterString("snd_hda_intel", parameters); got != "" {
		t.Errorf("a key must spell the module the way spec.modules does: %q", got)
	}
}

func TestModuleParameterNamesListsTheFilesToReadBack(t *testing.T) {
	got := ModuleParameterNames("snd_hda_intel", parameterFixture)
	want := []string{"enable_msi", "power_save", "power_save_controller"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestModuleParameterModulesNamesEveryModuleHalf(t *testing.T) {
	got := ModuleParameterModules(parameterFixture)
	want := []string{"comma", "drm", "i915", "snd_hda_intel"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestModuleParameterKeyWithoutADotNamesNothing(t *testing.T) {
	parameters := map[string]string{"power_save": "0", ".leading": "1", "trailing.": "1"}
	if got := ModuleParameterModules(parameters); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}
