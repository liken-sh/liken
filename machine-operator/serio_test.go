package main

import (
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

func TestSerioConditionNamesTheFirstEntryNotAttached(t *testing.T) {
	attached := machine.SerioStatus{Protocol: "pulse8-cec", USB: pulse8Serio.USB, TTY: "ttyACM0", State: machine.SerioAttached}
	missing := machine.SerioStatus{Protocol: "pulse8-cec", USB: pulse8Serio.USB, State: machine.SerioMissing,
		Message: "no USB device 2548:1002 is plugged in"}
	refused := machine.SerioStatus{Protocol: "pulse8-cec", USB: pulse8Serio.USB, TTY: "ttyACM1", State: machine.SerioRefused,
		Message: "declare serport in spec.modules"}
	tests := []struct {
		name     string
		observed []machine.SerioStatus
		want     api.Condition
	}{
		{"nothing declared", nil, api.Condition{Type: "SerioAttached", Status: api.ConditionTrue,
			Reason: "NothingDeclared", Message: "no serio entries declared"}},
		{"every entry attached", []machine.SerioStatus{attached}, api.Condition{Type: "SerioAttached",
			Status: api.ConditionTrue, Reason: "AllAttached", Message: "all 1 serio attachments hold"}},
		{"an adapter unplugged", []machine.SerioStatus{attached, missing}, api.Condition{Type: "SerioAttached",
			Status: api.ConditionFalse, Reason: "Missing",
			Message: "pulse8-cec 2548:1002: no USB device 2548:1002 is plugged in"}},
		{"a refusal first", []machine.SerioStatus{refused, missing}, api.Condition{Type: "SerioAttached",
			Status: api.ConditionFalse, Reason: "Refused",
			Message: "pulse8-cec 2548:1002 on ttyACM1: declare serport in spec.modules"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := serioCondition(test.observed); got != test.want {
				t.Errorf("got %+v", got)
			}
		})
	}
}

// A machine with an unplugged adapter works, and its workloads that do
// not use the adapter run as before, so the condition changes neither
// the phase nor the Ready roll-up.
func TestSerioAttachedLeavesTheMachineReady(t *testing.T) {
	conditions := []api.Condition{
		condition("SpecConverged", "True", "Converged"),
		condition("SerioAttached", "False", "Missing"),
		condition("SerioAttached", "False", "Refused"),
	}
	if phase := decidePhase(conditions); phase != api.PhaseReady {
		t.Errorf("phase = %s", phase)
	}
	if ready := readyCondition(conditions); ready.Status != api.ConditionTrue {
		t.Errorf("Ready = %+v", ready)
	}
}
