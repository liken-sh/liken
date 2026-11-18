package main

// The operator does not read a Horizon's contents. A scheduler reads
// them. The HorizonFound condition tells a person that a Telescope's
// spec.horizon refers to a Horizon that does not exist, before a
// scheduler plans a night without it. A missing Horizon does not stop
// the Telescope, so the condition does not change its phase.

import "github.com/liken-sh/liken/observatory-operator/observatory"

const reasonHorizonMissing = "HorizonMissing"

func (t *tree) horizonFound(name string) observatory.Condition {
	if _, ok := t.horizons[name]; !ok {
		return condition(observatory.ConditionHorizonFound, observatory.ConditionFalse, reasonHorizonMissing, "Missing Horizon "+name)
	}
	return condition(observatory.ConditionHorizonFound, observatory.ConditionTrue, "Found", "Found Horizon "+name)
}
