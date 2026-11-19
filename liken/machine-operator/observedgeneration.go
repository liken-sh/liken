package main

import (
	"slices"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// nodeConditions are the conditions that a pass sets only from what it
// read of the machine's Node. When the pass could not read the Node, it
// sets none of them, and the status keeps the values that the last
// successful read left.
var nodeConditions = []string{"NodeHealthy", "NodeTaintsApplied", "NodeLabelsApplied", "NodeCurrent"}

// stampObservedGeneration sets observedGeneration to generation on each
// condition that this pass set. A reader takes observedGeneration as
// the generation of the spec that a condition was evaluated against, so
// a condition kept from an earlier pass must keep its old value. When
// the pass could not read the Node, that is each of nodeConditions.
//
// RebootApprovedCondition always keeps its observedGeneration, because
// cluster-operator sets it and not this operator.
func stampObservedGeneration(conditions []api.Condition, generation int64, nodeRead bool) {
	for i := range conditions {
		if conditions[i].Type == machine.RebootApprovedCondition {
			continue
		}
		if !nodeRead && slices.Contains(nodeConditions, conditions[i].Type) {
			continue
		}
		conditions[i].ObservedGeneration = generation
	}
}
