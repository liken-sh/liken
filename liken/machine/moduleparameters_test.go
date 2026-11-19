package machine

import (
	"slices"
	"testing"
)

// parameterFixture declares three parameters for snd_hda_intel and one
// each for i915, drm, and comma. The comma value has commas in it, to
// check that a value passes through unchanged.
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
		t.Errorf("ModuleParameterString(snd_hda_intel) = %q, want %q", got, want)
	}
}

func TestModuleParameterStringTakesOnlyItsOwnModule(t *testing.T) {
	if got := ModuleParameterString("i915", parameterFixture); got != "enable_guc=3" {
		t.Errorf("ModuleParameterString(i915) = %q, want %q", got, "enable_guc=3")
	}
}

func TestModuleParameterStringKeepsACommaSeparatedArray(t *testing.T) {
	if got := ModuleParameterString("comma", parameterFixture); got != "array=1,2,3" {
		t.Errorf("ModuleParameterString(comma) = %q, want %q", got, "array=1,2,3")
	}
}

func TestModuleParameterStringIsEmptyForAnUndeclaredModule(t *testing.T) {
	if got := ModuleParameterString("e1000e", parameterFixture); got != "" {
		t.Errorf("ModuleParameterString(e1000e) = %q, want an empty string", got)
	}
}

func TestModuleParameterStringSpellsTheModuleExactly(t *testing.T) {
	parameters := map[string]string{"snd-hda-intel.power_save": "0"}
	if got := ModuleParameterString("snd_hda_intel", parameters); got != "" {
		t.Errorf("ModuleParameterString(snd_hda_intel) = %q, want an empty string, because a key spells the module as spec.modules does", got)
	}
}

func TestModuleParameterNamesListsTheFilesToReadBack(t *testing.T) {
	got := ModuleParameterNames("snd_hda_intel", parameterFixture)
	want := []string{"enable_msi", "power_save", "power_save_controller"}
	if !slices.Equal(got, want) {
		t.Errorf("ModuleParameterNames(snd_hda_intel) = %v, want %v", got, want)
	}
}

func TestModuleParameterModulesNamesEveryModuleHalf(t *testing.T) {
	got := ModuleParameterModules(parameterFixture)
	want := []string{"comma", "drm", "i915", "snd_hda_intel"}
	if !slices.Equal(got, want) {
		t.Errorf("ModuleParameterModules = %v, want %v", got, want)
	}
}

func TestModuleParameterKeyWithoutAModuleHalfHasNoModule(t *testing.T) {
	parameters := map[string]string{"power_save": "0", ".leading": "1", "trailing.": "1"}
	if got := ModuleParameterModules(parameters); len(got) != 0 {
		t.Errorf("ModuleParameterModules = %v, want no modules", got)
	}
}
