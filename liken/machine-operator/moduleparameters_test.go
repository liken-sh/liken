package main

import (
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func TestModuleParametersConditionNothingDeclared(t *testing.T) {
	c := moduleParametersCondition(nil, []machine.ModuleStatus{
		{Name: "snd_hda_intel", State: machine.ModuleLoaded},
	})
	if c.Type != "ModuleParametersApplied" || c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True with reason NothingDeclared", c)
	}
}

func TestModuleParametersConditionPassesAnOrdinaryLoad(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"snd_hda_intel.power_save": "0"},
		[]machine.ModuleStatus{
			{Name: "snd_hda_intel", State: machine.ModuleLoaded, Parameters: map[string]string{"power_save": "0"}},
		})
	if c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Errorf("the condition is %+v, want True with reason Applied", c)
	}
}

// A module loads even when a parameter name is one it does not have,
// and the kernel reports the mistake only in its log. The condition
// cannot detect that, so it stays True.
func TestModuleParametersConditionStaysTrueForAMissingReadback(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"snd_hda_intel.powersave": "0"},
		[]machine.ModuleStatus{{Name: "snd_hda_intel", State: machine.ModuleLoaded}})
	if c.Status != api.ConditionTrue {
		t.Errorf("the condition is %+v, want True", c)
	}
}

// A parameter for a module that this boot did not load never reached
// the kernel, so the condition leaves it out. SpecConverged reports
// the missing module.
func TestModuleParametersConditionIgnoresAModuleTheBootNeverLoaded(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"i915.enable_guc": "3"},
		[]machine.ModuleStatus{{Name: "loop", State: machine.ModuleBuiltin}})
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True with reason NothingDeclared", c)
	}
}

func TestModuleParametersConditionCountsOnlyWhatTheBootLoaded(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"snd_hda_intel.power_save": "0", "i915.enable_guc": "3"},
		[]machine.ModuleStatus{
			{Name: "snd_hda_intel", State: machine.ModuleLoaded, Parameters: map[string]string{"power_save": "0"}},
		})
	if c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Fatalf("the condition is %+v, want True with reason Applied", c)
	}
	if !strings.Contains(c.Message, "Loaded modules: 1") {
		t.Errorf("the message is %q, want a count that leaves out the module that never loaded", c.Message)
	}
}

// A load that failed applied no parameters, so the message leaves it
// out. ModulesLoaded reports the failure.
func TestModuleParametersConditionCountsOnlyLoadsThatSucceeded(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"snd_hda_intel.power_save": "0", "i915.enable_guc": "3"},
		[]machine.ModuleStatus{
			{Name: "snd_hda_intel", State: machine.ModuleLoaded, Parameters: map[string]string{"power_save": "0"}},
			{Name: "i915", State: machine.ModuleFailed, Message: "invalid argument (parameters enable_guc=3)"},
		})
	if c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Fatalf("the condition is %+v, want True with reason Applied", c)
	}
	if !strings.Contains(c.Message, "Loaded modules: 1") {
		t.Errorf("the message is %q, want a count that leaves out the load that failed", c.Message)
	}
}

func TestModuleParametersConditionReportsThatNoLoadPassedAParameter(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"i915.enable_guc": "3"},
		[]machine.ModuleStatus{{Name: "i915", State: machine.ModuleFailed, Message: "invalid argument"}})
	if c.Status != api.ConditionTrue {
		t.Fatalf("the condition is %+v, want True", c)
	}
	if strings.Contains(c.Message, "no module parameters declared") {
		t.Errorf("the message is %q, want no claim that no parameters are declared, because one is", c.Message)
	}
}

func TestModuleParametersConditionReportsABuiltin(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"drm.debug": "0x1e"},
		[]machine.ModuleStatus{{Name: "drm", State: machine.ModuleBuiltin}})
	if c.Status != api.ConditionFalse || c.Reason != "ParametersNotApplied" {
		t.Fatalf("the condition is %+v, want False with reason ParametersNotApplied", c)
	}
	if !strings.Contains(c.Message, "kernel command line") || !strings.Contains(c.Message, "debug=0x1e") {
		t.Errorf("the message is %q, want the kernel command line and debug=0x1e", c.Message)
	}
}

func TestModuleParametersConditionReportsAResidentModule(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"loop.max_part": "8"},
		[]machine.ModuleStatus{{Name: "loop", State: machine.ModuleLoaded, AlreadyResident: true}})
	if c.Status != api.ConditionFalse || c.Reason != "ParametersNotApplied" {
		t.Fatalf("the condition is %+v, want False with reason ParametersNotApplied", c)
	}
	if !strings.Contains(c.Message, "already in the kernel") || !strings.Contains(c.Message, "fixed list") {
		t.Errorf("the message is %q, want \"already in the kernel\" and \"fixed list\"", c.Message)
	}
}

// A builtin module and a module already in the kernel need different
// fixes, so the message gives both.
func TestModuleParametersConditionReportsBothFaultsAtOnce(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"drm.debug": "0x1e", "loop.max_part": "8"},
		[]machine.ModuleStatus{
			{Name: "drm", State: machine.ModuleBuiltin},
			{Name: "loop", State: machine.ModuleLoaded, AlreadyResident: true},
		})
	if c.Status != api.ConditionFalse {
		t.Fatalf("the condition is %+v, want False", c)
	}
	if !strings.Contains(c.Message, "kernel command line") || !strings.Contains(c.Message, "already in the kernel") {
		t.Errorf("the message is %q, want both the kernel command line and \"already in the kernel\"", c.Message)
	}
}

// The phase table has no entry for ParametersNotApplied, so the
// machine is Degraded. Only an edit to the spec fixes it.
func TestModuleParametersFaultReadsAsDegraded(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"drm.debug": "0x1e"},
		[]machine.ModuleStatus{{Name: "drm", State: machine.ModuleBuiltin}})
	if phase := conditionPhase(c); phase != api.PhaseDegraded {
		t.Errorf("conditionPhase = %s, want %s", phase, api.PhaseDegraded)
	}
}

func TestDecideConvergenceLoadsAModuleAddedWithItsParameters(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"loop", "i915"}
	m.Spec.ModuleParameters = map[string]string{"i915.enable_guc": "3"}
	facts := labFacts()
	facts.Boot.Modules = []string{"loop"}

	c := decideConvergence(m, facts, nil, "", turnStandalone)

	if !c.requestLoad || c.condition.Reason != "LoadRequested" {
		t.Errorf("the convergence is %+v, want a live load of the added module with its parameters", c.condition)
	}
}

// A live load promotes the manifest even when it cannot apply a
// parameter because the module was already loaded. The boot then
// reports the spec's hash with that parameter as drift, and only a
// reboot applies it.
func TestDecideConvergenceTakesTheRebootPathForAnUndeliveredParameter(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"sunrpc"}
	m.Spec.ModuleParameters = map[string]string{"sunrpc.tcp_slot_table_entries": "4"}
	m.Spec.RebootPolicy = machine.RebootAuto
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatalf("rendering the manifest: %v", err)
	}
	facts := labFacts()
	facts.Boot.ManifestHash = hash
	facts.Boot.Modules = []string{"sunrpc"}

	c := decideConvergence(m, facts, nil, "", turnAwaiting)

	if c.condition.Reason != "AwaitingTurn" {
		t.Fatalf("the reason is %q, want AwaitingTurn: %+v", c.condition.Reason, c.condition)
	}
	if !c.stage || c.requestReboot || c.requestLoad {
		t.Errorf("the convergence is %+v, want only the manifest staged while the machine waits for its turn", c)
	}
	if !strings.Contains(c.condition.Message, "sunrpc.tcp_slot_table_entries: 4 declared") {
		t.Errorf("the message is %q, want the drifted parameter sunrpc.tcp_slot_table_entries", c.condition.Message)
	}
}

// When the boot reports the spec's hash, only drift in module
// parameters can lead to a reboot. Any other drift is BootMismatch.
func TestDecideConvergenceStillBlocksOnAModuleSetMismatch(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"sunrpc", "loop"}
	m.Spec.ModuleParameters = map[string]string{"sunrpc.tcp_slot_table_entries": "4"}
	m.Spec.RebootPolicy = machine.RebootAuto
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatalf("rendering the manifest: %v", err)
	}
	facts := labFacts()
	facts.Boot.ManifestHash = hash
	facts.Boot.Modules = []string{"sunrpc"}
	facts.Boot.ModuleParameters = map[string]string{"sunrpc.tcp_slot_table_entries": "4"}

	c := decideConvergence(m, facts, nil, "", turnAwaiting)

	if c.condition.Reason != "BootMismatch" {
		t.Fatalf("the reason is %q, want BootMismatch: %+v", c.condition.Reason, c.condition)
	}
	if c.stage || c.requestReboot || c.requestLoad {
		t.Errorf("the convergence is %+v, want nothing staged, rebooted, or loaded, so that the machine stays Blocked", c)
	}
}

func TestDecideConvergenceRebootsForAParameterOnALoadedModule(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"loop"}
	m.Spec.ModuleParameters = map[string]string{"loop.max_part": "8"}
	facts := labFacts()
	facts.Boot.Modules = []string{"loop"}

	c := decideConvergence(m, facts, nil, "", turnStandalone)

	if c.requestLoad {
		t.Fatalf("the convergence requests a live load, want none because a loaded module does not read its parameters again: %+v", c.condition)
	}
	if !strings.Contains(c.condition.Message, "loop.max_part") {
		t.Errorf("the message is %q, want the drifted parameter loop.max_part", c.condition.Message)
	}
}
