package main

// The generation stamp of each condition.

import (
	"slices"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// nodeConditions are the conditions that a pass sets only from its read
// of the machine's Node. A pass whose read of the Node failed sets none
// of them, and the status carries each one forward as the last pass
// that read the Node left it.
var nodeConditions = []string{"NodeHealthy", "NodeTaintsApplied", "NodeLabelsApplied", "NodeCurrent"}

// stampObservedGeneration records the generation of the spec that this
// pass judged on each condition that this pass set. A reader takes a
// condition's observedGeneration as "the operator judged this spec", so
// a stamp on a condition that the pass only carried forward would
// present the verdict of an earlier pass as a verdict on the current
// spec. So when the pass did not read the Node, the conditions that
// depend on the Node keep the stamp of the pass that set them.
//
// The conductor's grant keeps its own stamp on every pass, because it
// is the conductor's verdict.
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
