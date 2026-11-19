package machine

import (
	"strings"
	"testing"
)

func TestModuleParameterDriftReportsAChangeOnALoadedModule(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"snd_hda_intel"}, []string{"snd_hda_intel"},
		map[string]string{"snd_hda_intel.power_save": "0"},
		map[string]string{"snd_hda_intel.power_save": "1"})
	if len(diffs) != 1 || !strings.Contains(diffs[0], "snd_hda_intel.power_save: 0 declared, 1 in this boot") {
		t.Errorf("the drift is %v, want one line: snd_hda_intel.power_save: 0 declared, 1 in this boot", diffs)
	}
}

func TestModuleParameterDriftReportsAParameterAddedToALoadedModule(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"snd_hda_intel"}, []string{"snd_hda_intel"},
		map[string]string{"snd_hda_intel.power_save": "0"}, nil)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "snd_hda_intel.power_save: 0 declared but this boot ran without it") {
		t.Errorf("the drift is %v, want one line: snd_hda_intel.power_save: 0 declared but this boot ran without it", diffs)
	}
}

func TestModuleParameterDriftReportsARemovedParameter(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"snd_hda_intel"}, []string{"snd_hda_intel"},
		nil, map[string]string{"snd_hda_intel.power_save": "0"})
	if len(diffs) != 1 || !strings.Contains(diffs[0], "snd_hda_intel.power_save: 0 no longer declared but this boot ran with it") {
		t.Errorf("the drift is %v, want one line: snd_hda_intel.power_save: 0 no longer declared but this boot ran with it", diffs)
	}
}

func TestModuleParameterDriftIgnoresAnAddedModule(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"snd_hda_intel", "i915"}, []string{"snd_hda_intel"},
		map[string]string{"i915.enable_guc": "3"}, nil)
	if len(diffs) != 0 {
		t.Errorf("the drift is %v, want none, because a module that the boot did not load gets its parameters when it loads", diffs)
	}
}

func TestModuleParameterDriftIgnoresARemovedModule(t *testing.T) {
	diffs := ModuleParameterDrift(
		nil, []string{"i915"},
		nil, map[string]string{"i915.enable_guc": "3"})
	if len(diffs) != 0 {
		t.Errorf("the drift is %v, want none, because the removed module has its own drift line", diffs)
	}
}

func TestModuleParameterDriftSortsLinesByKey(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"i915", "snd_hda_intel"}, []string{"i915", "snd_hda_intel"},
		map[string]string{"snd_hda_intel.power_save": "0", "i915.enable_guc": "3"}, nil)
	if len(diffs) != 2 {
		t.Fatalf("the drift is %v, want two lines", diffs)
	}
	if !strings.Contains(diffs[0], "i915.enable_guc") || !strings.Contains(diffs[1], "snd_hda_intel.power_save") {
		t.Errorf("the drift is %v, want the i915 line before the snd_hda_intel line", diffs)
	}
}

// machine-operator loads a spec change without a reboot only when the
// drift has one line for each added module and no more, so a parameter
// change on a loaded module must add a line of its own.
func TestModulesDriftIncludesAParameterChangeBesideTheAddedModules(t *testing.T) {
	desired := []string{"i915", "snd_hda_intel"}
	booted := []string{"snd_hda_intel"}
	parameters := map[string]string{
		"i915.enable_guc":          "3",
		"snd_hda_intel.power_save": "0",
	}
	added, _ := ModuleSetDiff(desired, booted)
	drift := ModulesDrift(desired, booted, parameters, nil)
	if len(added) != 1 || len(drift) != 2 {
		t.Errorf("the added modules are %v and the drift is %v, want one added module and two drift lines", added, drift)
	}
}

func TestModulesDriftEqualsTheAddedModulesWhenOnlyAModuleIsAdded(t *testing.T) {
	desired := []string{"i915", "snd_hda_intel"}
	booted := []string{"snd_hda_intel"}
	parameters := map[string]string{"i915.enable_guc": "3"}
	added, retracted := ModuleSetDiff(desired, booted)
	drift := ModulesDrift(desired, booted, parameters, nil)
	if len(retracted) != 0 || len(drift) != len(added) {
		t.Errorf("the added modules are %v, the retracted modules are %v, and the drift is %v, want no retracted modules and one drift line for each added module", added, retracted, drift)
	}
}
