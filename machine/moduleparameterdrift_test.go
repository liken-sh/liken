package machine

import (
	"strings"
	"testing"
)

func TestModuleParameterDriftSeesAChangeOnALoadedModule(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"snd_hda_intel"}, []string{"snd_hda_intel"},
		map[string]string{"snd_hda_intel.power_save": "0"},
		map[string]string{"snd_hda_intel.power_save": "1"})
	if len(diffs) != 1 || !strings.Contains(diffs[0], "snd_hda_intel.power_save: 0 declared, 1 actuated") {
		t.Errorf("got %v", diffs)
	}
}

func TestModuleParameterDriftSeesAParameterAddedToALoadedModule(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"snd_hda_intel"}, []string{"snd_hda_intel"},
		map[string]string{"snd_hda_intel.power_save": "0"}, nil)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "snd_hda_intel.power_save: 0 declared but not actuated") {
		t.Errorf("got %v", diffs)
	}
}

func TestModuleParameterDriftSeesARetractedParameter(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"snd_hda_intel"}, []string{"snd_hda_intel"},
		nil, map[string]string{"snd_hda_intel.power_save": "0"})
	if len(diffs) != 1 || !strings.Contains(diffs[0], "snd_hda_intel.power_save: 0 actuated but no longer declared") {
		t.Errorf("got %v", diffs)
	}
}

func TestModuleParameterDriftSaysNothingAboutAnAddedModule(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"snd_hda_intel", "i915"}, []string{"snd_hda_intel"},
		map[string]string{"i915.enable_guc": "3"}, nil)
	if len(diffs) != 0 {
		t.Errorf("a module the boot never loaded takes its parameters at the load: %v", diffs)
	}
}

func TestModuleParameterDriftSaysNothingAboutARetractedModule(t *testing.T) {
	diffs := ModuleParameterDrift(
		nil, []string{"i915"},
		nil, map[string]string{"i915.enable_guc": "3"})
	if len(diffs) != 0 {
		t.Errorf("the retracted module's own line is the whole report: %v", diffs)
	}
}

func TestModuleParameterDriftSortsItsLines(t *testing.T) {
	diffs := ModuleParameterDrift(
		[]string{"i915", "snd_hda_intel"}, []string{"i915", "snd_hda_intel"},
		map[string]string{"snd_hda_intel.power_save": "0", "i915.enable_guc": "3"}, nil)
	if len(diffs) != 2 {
		t.Fatalf("got %v", diffs)
	}
	if !strings.Contains(diffs[0], "i915.enable_guc") || !strings.Contains(diffs[1], "snd_hda_intel.power_save") {
		t.Errorf("the lines sort by key: %v", diffs)
	}
}

// A parameter change on a loaded module must make the drift longer
// than the list of added modules, because that count is what
// classifies a spec change as live-applicable.
func TestModulesDriftCountsAParameterChangeBesideTheAddedModules(t *testing.T) {
	desired := []string{"i915", "snd_hda_intel"}
	actuated := []string{"snd_hda_intel"}
	parameters := map[string]string{
		"i915.enable_guc":          "3",
		"snd_hda_intel.power_save": "0",
	}
	added, _ := ModuleSetDiff(desired, actuated)
	drift := ModulesDrift(desired, actuated, parameters, nil)
	if len(added) != 1 || len(drift) != 2 {
		t.Errorf("added %v, drift %v", added, drift)
	}
}

func TestModulesDriftKeepsAnAddedModuleWithParametersLiveClass(t *testing.T) {
	desired := []string{"i915", "snd_hda_intel"}
	actuated := []string{"snd_hda_intel"}
	parameters := map[string]string{"i915.enable_guc": "3"}
	added, retracted := ModuleSetDiff(desired, actuated)
	drift := ModulesDrift(desired, actuated, parameters, nil)
	if len(retracted) != 0 || len(drift) != len(added) {
		t.Errorf("added %v, drift %v", added, drift)
	}
}
