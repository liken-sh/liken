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
				t.Errorf("clockCondition(%+v) = %+v, want status %s and reason %s", tc.time, c, tc.status, tc.reason)
			}
		})
	}
}

func TestAnUnsynchronizedClockLeavesTheMachineReady(t *testing.T) {
	conditions := []api.Condition{
		condition("HostEntriesApplied", "True", "Applied"),
		clockCondition(machine.TimeStatus{State: machine.TimeUnsynchronized, Stratum: 16}),
	}
	if c := readyCondition(conditions); c.Status != api.ConditionTrue {
		t.Errorf("readyCondition = %+v, want status True", c)
	}
	if phase := decidePhase(conditions); phase != api.PhaseReady {
		t.Errorf("decidePhase = %s, want %s", phase, api.PhaseReady)
	}
}
