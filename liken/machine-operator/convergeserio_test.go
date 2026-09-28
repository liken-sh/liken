package main

import (
	"testing"

	"github.com/liken-sh/liken/machine"
)

var pulse8Serio = machine.SerioAttachment{
	Protocol: "pulse8-cec",
	USB:      machine.SerioUSB{Vendor: "2548", Product: "1002"},
}

// spec.serio converges on the terms spec.modules does: an addition
// loads live, alone or beside added modules, and a retraction waits
// for a boot. A serio line beside any reboot-class drift takes the
// reboot.
func TestSerioEditsConvergeLikeModules(t *testing.T) {
	tests := []struct {
		name       string
		spec       func(*machine.MachineSpec)
		boot       []machine.SerioAttachment
		wantReason string
		wantLoad   bool
	}{
		{"an added entry", func(s *machine.MachineSpec) {
			s.Serio = []machine.SerioAttachment{pulse8Serio}
		}, nil, "LoadRequested", true},
		{"an added entry with its modules", func(s *machine.MachineSpec) {
			s.Modules = []string{"cdc_acm", "serport", "pulse8_cec"}
			s.Serio = []machine.SerioAttachment{pulse8Serio}
		}, nil, "LoadRequested", true},
		{"a retracted entry", func(*machine.MachineSpec) {}, []machine.SerioAttachment{pulse8Serio}, "RebootPending", false},
		{"an added entry beside an rlimit edit", func(s *machine.MachineSpec) {
			s.Serio = []machine.SerioAttachment{pulse8Serio}
			s.Rlimits = map[string]string{"nofile": "524288"}
		}, nil, "RebootPending", false},
		{"a changed serial", func(s *machine.MachineSpec) {
			withSerial := pulse8Serio
			withSerial.USB.Serial = "A1"
			s.Serio = []machine.SerioAttachment{withSerial}
		}, []machine.SerioAttachment{pulse8Serio}, "RebootPending", false},
		{"no change", func(s *machine.MachineSpec) {
			s.Serio = []machine.SerioAttachment{pulse8Serio}
		}, []machine.SerioAttachment{pulse8Serio}, "Converged", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := labMachine()
			test.spec(&m.Spec)
			facts := labFacts()
			facts.Boot.Serio = test.boot

			conv := decideConvergence(m, facts, nil, "", turnStandalone)

			if conv.condition.Reason != test.wantReason || conv.requestLoad != test.wantLoad || conv.requestReboot {
				t.Errorf("got %+v, load %v", conv.condition, conv.requestLoad)
			}
		})
	}
}

// A manifest that reached the machine without admission can carry an
// entry that could never match, and staging it would cost a reboot to
// learn that.
func TestDecideConvergenceRefusesAnInvalidSerioEntry(t *testing.T) {
	m := labMachine()
	m.Spec.Serio = []machine.SerioAttachment{{Protocol: "pulse8-cec", USB: machine.SerioUSB{Vendor: "2548", Product: "10020"}}}

	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)

	if conv.condition.Reason != "StagingRejected" || conv.stage {
		t.Errorf("got %+v", conv.condition)
	}
}
