package main

import (
	"fmt"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// clockSynchronizedCondition is left out of the phase and the Ready
// condition (reportsOnly in phase.go). An outage of the upstream time
// sources makes every leader Unsynchronized at once. If that made them
// Degraded, no machine could take a reboot turn, and a machine with a
// real fault would be hidden among them.
const clockSynchronizedCondition = "ClockSynchronized"

// clockCondition reports status.time as a condition, so that a loss of
// synchronization posts an Event and `kubectl wait` can wait on it. It
// is False when the time sources stopped answering, or when the bound
// on the offset is past the step threshold, where the clock's error
// grows faster than a slew can remove it. It is True on a cluster that
// declares no upstreams, and after the step at boot before the first
// poll, when the clock has no bound yet.
func clockCondition(t machine.TimeStatus) api.Condition {
	c := api.Condition{Type: clockSynchronizedCondition}
	switch t.State {
	case machine.TimeSynchronized:
		if bound, ok := machine.ParseOffsetBound(t.Offset); ok && bound.OutrunsSlew() {
			c.Status, c.Reason = api.ConditionFalse, "OffsetTooLarge"
			c.Message = fmt.Sprintf("The clock is %s from %s, more than the 128ms that a slew can correct", t.Offset, t.Source)
			return c
		}
		c.Status, c.Reason = api.ConditionTrue, "Synchronized"
		c.Message = fmt.Sprintf("The clock follows %s at stratum %d", t.Source, t.Stratum)
		if t.Offset != "" {
			c.Message += ", within " + t.Offset
		}
	case machine.TimeFreeRunning:
		c.Status, c.Reason = api.ConditionTrue, "FreeRunning"
		c.Message = "The Cluster declares no time upstreams, so the clock runs on its own hardware clock"
	case machine.TimeUnsynchronized:
		c.Status, c.Reason = api.ConditionFalse, "Unsynchronized"
		c.Message = "No time source answered in the last three polls"
	default:
		c.Status, c.Reason = api.ConditionUnknown, "NoClockFacts"
		c.Message = "init published no time facts"
	}
	return c
}
