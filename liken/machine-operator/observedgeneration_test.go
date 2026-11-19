package main

import (
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// observedGenerations maps each condition type to its
// observedGeneration.
func observedGenerations(conditions []api.Condition) map[string]int64 {
	stamps := map[string]int64{}
	for _, condition := range conditions {
		stamps[condition.Type] = condition.ObservedGeneration
	}
	return stamps
}

// A pass sets the current generation on the conditions it set, and a
// pass that could not read the Node leaves the generation of the Node
// conditions as it was.
func TestAPassStampsOnlyTheConditionsItSet(t *testing.T) {
	cases := []struct {
		name     string
		nodeRead bool
		want     map[string]int64
	}{
		{name: "the pass read the Node", nodeRead: true, want: map[string]int64{
			"SysctlsApplied": 4, "NodeHealthy": 4, "NodeTaintsApplied": 4, "NodeLabelsApplied": 4, "NodeCurrent": 4,
			machine.RebootApprovedCondition: 2,
		}},
		{name: "the read of the Node failed", nodeRead: false, want: map[string]int64{
			"SysctlsApplied": 4, "NodeHealthy": 3, "NodeTaintsApplied": 3, "NodeLabelsApplied": 3, "NodeCurrent": 3,
			machine.RebootApprovedCondition: 2,
		}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			conditions := []api.Condition{
				{Type: "SysctlsApplied", ObservedGeneration: 3},
				{Type: "NodeHealthy", ObservedGeneration: 3},
				{Type: "NodeTaintsApplied", ObservedGeneration: 3},
				{Type: "NodeLabelsApplied", ObservedGeneration: 3},
				{Type: "NodeCurrent", ObservedGeneration: 3},
				{Type: machine.RebootApprovedCondition, ObservedGeneration: 2},
			}

			stampObservedGeneration(conditions, 4, each.nodeRead)

			got := observedGenerations(conditions)
			for name, want := range each.want {
				if got[name] != want {
					t.Errorf("%s has observedGeneration %d, want %d", name, got[name], want)
				}
			}
		})
	}
}
