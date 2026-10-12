package main

// The HorizonFound condition of a Telescope that names a Horizon in
// spec.horizon (plan 18). The operator reads nothing in a Horizon: a
// scheduler reads the profile. The condition and the Event of its
// transition tell a person that the telescope names a horizon that
// does not exist, before a scheduler plans a night with no horizon. A
// missing Horizon stops nothing, so the condition has no part in the
// telescope's phase. The status writer composes it on each change of
// the watches' stores, so a Horizon created or deleted moves it.

import "github.com/liken-sh/liken/observatory-operator/observatory"

// reasonHorizonMissing is the reason of a HorizonFound condition that
// is False.
const reasonHorizonMissing = "HorizonMissing"

// horizonFound answers the HorizonFound condition of a Telescope whose
// spec.horizon names a Horizon.
func (t *tree) horizonFound(name string) observatory.Condition {
	if _, ok := t.horizons[name]; !ok {
		return condition(observatory.ConditionHorizonFound, observatory.ConditionFalse, reasonHorizonMissing, "Missing Horizon "+name)
	}
	return condition(observatory.ConditionHorizonFound, observatory.ConditionTrue, "Found", "Found Horizon "+name)
}
