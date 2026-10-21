package disruption

// A MachineDisruptionBudget is the PodDisruptionBudget of machines. It
// selects machines by their labels and limits how many of them may be
// out of service at once, planned and unplanned together.
//
// Several budgets can select one machine, unlike pod budgets, where
// the eviction API refuses a pod that two budgets select. A machine
// takes a turn only when every budget that selects it has room. One
// budget, named default, selects every machine, so a machine that no
// other budget selects is still limited. cluster-operator creates it
// when it is missing, from the Cluster's spec.disruption.maxUnavailable.
//
// The rule of one leader at a time is not a budget. It applies on top
// of every budget, because the datastore keeps quorum only while a
// majority of the leaders is up, and a Machine has no role in its spec
// for a selector to read.

import (
	"slices"

	"github.com/liken-sh/liken/liken/api"
)

// DefaultBudget is the name of the budget that selects every machine.
const DefaultBudget = "default"

// AllowedCondition reports whether a budget has room for one more
// machine.
const AllowedCondition = "DisruptionAllowed"

// Budget is a MachineDisruptionBudget.
type Budget struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   api.ObjectMeta `json:"metadata"`
	Spec       BudgetSpec     `json:"spec"`
	Status     BudgetStatus   `json:"status,omitzero"`
}

// GetObjectMeta answers the metadata that a watch's store and the
// memo of an operator's writes key the budget by.
func (b *Budget) GetObjectMeta() api.Meta { return b.Metadata }

// BudgetSpec is the limit and the machines it covers.
type BudgetSpec struct {
	Selector       Selector `json:"selector"`
	MaxUnavailable int      `json:"maxUnavailable"`
}

// Selector selects machines by their labels, with the grammar of a
// Kubernetes label selector. An empty selector selects every machine.
type Selector struct {
	MatchLabels      map[string]string `json:"matchLabels,omitempty"`
	MatchExpressions []Requirement     `json:"matchExpressions,omitempty"`
}

// Requirement is one expression of a selector.
type Requirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// BudgetStatus is what the budget counted on cluster-operator's last
// sweep: the machines it selects, the ones of them that are out of
// service, and how many more may go.
type BudgetStatus struct {
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Selected           int             `json:"selected"`
	Unavailable        int             `json:"unavailable"`
	Allowed            int             `json:"allowed"`
	Conditions         []api.Condition `json:"conditions,omitempty"`
}

// NewDefaultBudget answers the budget that selects every machine.
func NewDefaultBudget(maxUnavailable int) *Budget {
	return &Budget{
		APIVersion: api.APIVersion,
		Kind:       "MachineDisruptionBudget",
		Metadata:   api.ObjectMeta{Name: DefaultBudget},
		Spec:       BudgetSpec{MaxUnavailable: maxUnavailable},
	}
}

// Selects reports whether the budget covers a machine with these
// labels. The CRD admits only the four operators below, so an
// operator the code does not know selects nothing.
func (s Selector) Selects(labels map[string]string) bool {
	for key, value := range s.MatchLabels {
		if held, ok := labels[key]; !ok || held != value {
			return false
		}
	}
	for _, r := range s.MatchExpressions {
		held, ok := labels[r.Key]
		switch r.Operator {
		case "In":
			if !ok || !slices.Contains(r.Values, held) {
				return false
			}
		case "NotIn":
			if ok && slices.Contains(r.Values, held) {
				return false
			}
		case "Exists":
			if !ok {
				return false
			}
		case "DoesNotExist":
			if ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}
