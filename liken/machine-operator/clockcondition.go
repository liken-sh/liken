package main

import (
	"fmt"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// clockSynchronizedCondition is the type of the condition that
// clockCondition builds. The phase and the Ready roll-up skip it by
// this name (phase.go): an upstream outage makes every leader
// Unsynchronized at the same moment, and a fleet that turns Degraded
// together stalls every reboot turn and hides the machine with a real
// fault.
const clockSynchronizedCondition = "ClockSynchronized"

// clockCondition reports status.time as a condition, so a lost clock
// posts an Event and gives `kubectl wait` something to wait on. It is
// True while the clock follows a source within the step threshold, and
// True by design on a cluster that declares no upstreams. It is False
// when the sources stopped answering, or when the bound on the offset
// is past the step threshold, where the clock's error grows faster
// than a slew removes it. A clock with no bound yet, after a boot step
// and before the first poll, follows its source and is True.
func clockCondition(t machine.TimeStatus) api.Condition {
	c := api.Condition{Type: clockSynchronizedCondition}
	switch t.State {
	case machine.TimeSynchronized:
		if bound, ok := machine.ParseOffsetBound(t.Offset); ok && bound.OutrunsSlew() {
			c.Status, c.Reason = api.ConditionFalse, "OffsetTooLarge"
			c.Message = fmt.Sprintf("the clock is within %s of %s, past the 128ms that a slew can correct", t.Offset, t.Source)
			return c
		}
		c.Status, c.Reason = api.ConditionTrue, "Synchronized"
		c.Message = fmt.Sprintf("the clock follows %s at stratum %d", t.Source, t.Stratum)
		if t.Offset != "" {
			c.Message += ", within " + t.Offset
		}
	case machine.TimeFreeRunning:
		c.Status, c.Reason = api.ConditionTrue, "FreeRunning"
		c.Message = "the cluster declares no time upstreams, so the clock runs on its own hardware clock"
	case machine.TimeUnsynchronized:
		c.Status, c.Reason = api.ConditionFalse, "Unsynchronized"
		c.Message = "no time source answered in the last three polls"
	default:
		c.Status, c.Reason = api.ConditionUnknown, "NoClockFacts"
		c.Message = "init published no time facts"
	}
	return c
}
