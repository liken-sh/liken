package main

import (
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// observedGenerations maps each condition to the generation it carries.
func observedGenerations(conditions []api.Condition) map[string]int64 {
	stamps := map[string]int64{}
	for _, condition := range conditions {
		stamps[condition.Type] = condition.ObservedGeneration
	}
	return stamps
}

// A pass stamps the generation it judged on the conditions it set. A
// pass that could not read the Node set none of the conditions that
// depend on the Node, so each of those keeps the generation of the pass
// that set it, and a reader does not take an old verdict for a verdict
// on the spec as it is now.
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
					t.Errorf("%s carries generation %d, want %d", name, got[name], want)
				}
			}
		})
	}
}
