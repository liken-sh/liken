package main

import (
	"strings"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

func TestModuleParametersConditionNothingDeclared(t *testing.T) {
	c := moduleParametersCondition(nil, []machine.ModuleStatus{
		{Name: "snd_hda_intel", State: machine.ModuleLoaded},
	})
	if c.Type != "ModuleParametersApplied" || c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("got %+v", c)
	}
}

func TestModuleParametersConditionPassesAnOrdinaryLoad(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"snd_hda_intel.power_save": "0"},
		[]machine.ModuleStatus{
			{Name: "snd_hda_intel", State: machine.ModuleLoaded, Parameters: map[string]string{"power_save": "0"}},
		})
	if c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Errorf("got %+v", c)
	}
}

// A parameter name the module does not have still loads the module,
// and the kernel says so only in its own log. The condition has no
// way to see that, and it stays True on purpose.
func TestModuleParametersConditionStaysTrueForAMissingReadback(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"snd_hda_intel.powersave": "0"},
		[]machine.ModuleStatus{{Name: "snd_hda_intel", State: machine.ModuleLoaded}})
	if c.Status != api.ConditionTrue {
		t.Errorf("got %+v", c)
	}
}

// A parameter declared for a module this boot never loaded is not a
// parameter that reached the kernel, so it counts toward nothing. The
// drift machinery already carries that module to its next boot.
func TestModuleParametersConditionIgnoresAModuleTheBootNeverLoaded(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"i915.enable_guc": "3"},
		[]machine.ModuleStatus{{Name: "loop", State: machine.ModuleBuiltin}})
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("got %+v", c)
	}
}

func TestModuleParametersConditionCountsOnlyWhatTheBootLoaded(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"snd_hda_intel.power_save": "0", "i915.enable_guc": "3"},
		[]machine.ModuleStatus{
			{Name: "snd_hda_intel", State: machine.ModuleLoaded, Parameters: map[string]string{"power_save": "0"}},
		})
	if c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Message, "for 1 module") {
		t.Errorf("the count must leave out the module that never loaded: %q", c.Message)
	}
}

// A load that failed carried nothing, so it cannot count toward a
// message that says every declared parameter reached the kernel. The
// failure itself is ModulesLoaded's report, not this one's.
func TestModuleParametersConditionCountsOnlyLoadsThatSucceeded(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"snd_hda_intel.power_save": "0", "i915.enable_guc": "3"},
		[]machine.ModuleStatus{
			{Name: "snd_hda_intel", State: machine.ModuleLoaded, Parameters: map[string]string{"power_save": "0"}},
			{Name: "i915", State: machine.ModuleFailed, Message: "invalid argument (parameters enable_guc=3)"},
		})
	if c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Message, "for 1 module") {
		t.Errorf("the count must leave out the load that failed: %q", c.Message)
	}
}

// Every declared module failed its load, so no load carried a
// parameter. The message must not claim that none were declared.
func TestModuleParametersConditionSaysNoLoadCarriedAnything(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"i915.enable_guc": "3"},
		[]machine.ModuleStatus{{Name: "i915", State: machine.ModuleFailed, Message: "invalid argument"}})
	if c.Status != api.ConditionTrue {
		t.Fatalf("got %+v", c)
	}
	if strings.Contains(c.Message, "no module parameters declared") {
		t.Errorf("a parameter was declared: %q", c.Message)
	}
}

func TestModuleParametersConditionReportsABuiltin(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"drm.debug": "0x1e"},
		[]machine.ModuleStatus{{Name: "drm", State: machine.ModuleBuiltin}})
	if c.Status != api.ConditionFalse || c.Reason != "ParametersNotApplied" {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Message, "kernel command line") || !strings.Contains(c.Message, "debug=0x1e") {
		t.Errorf("the message names the fix and the string: %q", c.Message)
	}
}

func TestModuleParametersConditionReportsAResidentModule(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"loop.max_part": "8"},
		[]machine.ModuleStatus{{Name: "loop", State: machine.ModuleLoaded, AlreadyResident: true}})
	if c.Status != api.ConditionFalse || c.Reason != "ParametersNotApplied" {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Message, "already in the kernel") || !strings.Contains(c.Message, "fixed list") {
		t.Errorf("the message names the fix: %q", c.Message)
	}
}

// The two structural faults have two different fixes, so a machine
// carrying both reports both.
func TestModuleParametersConditionReportsBothFaultsAtOnce(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"drm.debug": "0x1e", "loop.max_part": "8"},
		[]machine.ModuleStatus{
			{Name: "drm", State: machine.ModuleBuiltin},
			{Name: "loop", State: machine.ModuleLoaded, AlreadyResident: true},
		})
	if c.Status != api.ConditionFalse {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Message, "kernel command line") || !strings.Contains(c.Message, "already in the kernel") {
		t.Errorf("both fixes must appear: %q", c.Message)
	}
}

// A reason absent from the phase table reads as Degraded, which is
// what a declaration that never reached the kernel deserves: nothing
// but an edit will fix it.
func TestModuleParametersFaultReadsAsDegraded(t *testing.T) {
	c := moduleParametersCondition(
		map[string]string{"drm.debug": "0x1e"},
		[]machine.ModuleStatus{{Name: "drm", State: machine.ModuleBuiltin}})
	if phase := conditionPhase(c); phase != api.PhaseDegraded {
		t.Errorf("got %s", phase)
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
		t.Errorf("an added module with its parameters loads live: %+v", c.condition)
	}
}

// A live load records only the parameters it delivered, so a module
// that was already resident when the load reached it leaves its
// parameter undelivered while the manifest promotes. The facts then
// carry the spec's own hash beside that one drift line, and the
// designed answer is the reboot that delivers it.
func TestDecideConvergenceTakesTheRebootPathForAnUndeliveredParameter(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"sunrpc"}
	m.Spec.ModuleParameters = map[string]string{"sunrpc.tcp_slot_table_entries": "4"}
	m.Spec.RebootPolicy = machine.RebootAuto
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	facts := labFacts()
	facts.Boot.ManifestHash = hash
	facts.Boot.Modules = []string{"sunrpc"}

	c := decideConvergence(m, facts, nil, "", turnAwaiting)

	if c.condition.Reason != "AwaitingTurn" {
		t.Fatalf("got %+v", c.condition)
	}
	if !c.stage || c.requestReboot || c.requestLoad {
		t.Errorf("the manifest stages and the machine waits for its turn: %+v", c)
	}
	if !strings.Contains(c.condition.Message, "sunrpc.tcp_slot_table_entries: 4 declared but not actuated") {
		t.Errorf("the message carries the drift: %q", c.condition.Message)
	}
}

// Only parameter drift has a designed path out of a matching hash.
// Any other shape is still the contradiction the guard was written
// for.
func TestDecideConvergenceStillBlocksOnAModuleSetMismatch(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"sunrpc", "loop"}
	m.Spec.ModuleParameters = map[string]string{"sunrpc.tcp_slot_table_entries": "4"}
	m.Spec.RebootPolicy = machine.RebootAuto
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	facts := labFacts()
	facts.Boot.ManifestHash = hash
	facts.Boot.Modules = []string{"sunrpc"}
	facts.Boot.ModuleParameters = map[string]string{"sunrpc.tcp_slot_table_entries": "4"}

	c := decideConvergence(m, facts, nil, "", turnAwaiting)

	if c.condition.Reason != "BootMismatch" {
		t.Fatalf("got %+v", c.condition)
	}
	if c.stage || c.requestReboot || c.requestLoad {
		t.Errorf("a contradiction must stay Blocked: %+v", c)
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
		t.Fatalf("a loaded module never reads its parameters again: %+v", c.condition)
	}
	if !strings.Contains(c.condition.Message, "loop.max_part") {
		t.Errorf("the drift must name the parameter: %q", c.condition.Message)
	}
}
