package main

import (
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func TestClockCondition(t *testing.T) {
	cases := map[string]struct {
		time   machine.TimeStatus
		status api.ConditionStatus
		reason string
	}{
		"a clock that follows its source": {
			machine.TimeStatus{State: machine.TimeSynchronized, Source: "10.0.0.210", Stratum: 4, Offset: "±5ms"},
			api.ConditionTrue, "Synchronized",
		},
		"a clock at the step threshold still follows": {
			machine.TimeStatus{State: machine.TimeSynchronized, Source: "10.0.0.210", Offset: "±128ms"},
			api.ConditionTrue, "Synchronized",
		},
		"a clock after a boot step, before its first poll": {
			machine.TimeStatus{State: machine.TimeSynchronized, Source: "10.0.0.210"},
			api.ConditionTrue, "Synchronized",
		},
		"a clock past the step threshold": {
			machine.TimeStatus{State: machine.TimeSynchronized, Source: "10.0.0.210", Offset: "±1s"},
			api.ConditionFalse, "OffsetTooLarge",
		},
		"a clock more than a second off": {
			machine.TimeStatus{State: machine.TimeSynchronized, Source: "10.0.0.210", Offset: ">1s"},
			api.ConditionFalse, "OffsetTooLarge",
		},
		"a cluster with no upstreams": {
			machine.TimeStatus{State: machine.TimeFreeRunning, Stratum: 10},
			api.ConditionTrue, "FreeRunning",
		},
		"sources that stopped answering": {
			machine.TimeStatus{State: machine.TimeUnsynchronized, Stratum: 16},
			api.ConditionFalse, "Unsynchronized",
		},
		"no time facts": {
			machine.TimeStatus{},
			api.ConditionUnknown, "NoClockFacts",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := clockCondition(tc.time)
			if c.Type != clockSynchronizedCondition || c.Status != tc.status || c.Reason != tc.reason {
				t.Errorf("got %+v", c)
			}
		})
	}
}

// A clock that loses its source leaves the machine Ready. An upstream
// outage reaches every leader at once, and a fleet that turns Degraded
// together stalls every reboot turn.
func TestAnUnsynchronizedClockLeavesTheMachineReady(t *testing.T) {
	conditions := []api.Condition{
		condition("HostEntriesApplied", "True", "Applied"),
		clockCondition(machine.TimeStatus{State: machine.TimeUnsynchronized, Stratum: 16}),
	}
	if c := readyCondition(conditions); c.Status != api.ConditionTrue {
		t.Errorf("got %+v", c)
	}
	if phase := decidePhase(conditions); phase != api.PhaseReady {
		t.Errorf("got %s", phase)
	}
}
