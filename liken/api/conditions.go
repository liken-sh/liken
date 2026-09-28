package api

// Conditions are how a liken document's status carries observations:
// a set of typed, timestamped verdicts ("Ready", "SysctlsApplied")
// that controllers maintain and humans and tooling read. The shape
// and the rules here mirror metav1.Condition, the method Kubernetes
// uses everywhere (Pods, Nodes, and Deployments all carry these), so
// anyone who reads `kubectl describe` output already knows how to
// read a liken document.

import (
	"slices"
	"time"
)

// ConditionStatus is a condition's verdict. It is a string rather
// than a bool because there is a third state: a controller must be
// able to say when it currently cannot tell.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition mirrors metav1.Condition, the shape Kubernetes uses
// everywhere (Pods, Nodes, and Deployments all carry these).
// ObservedGeneration records which metadata.generation the condition
// judged. Generation counts spec edits, so a reader can tell "Ready,
// for the spec as it stands" apart from "Ready, but for a spec two
// edits ago". That difference matters in liken, where edits wait for
// a reboot to take effect. (The convergence conditions make the
// stronger, content-hashed version of this claim. The generation is
// for tooling that reads the convention.)
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Reason             string          `json:"reason,omitempty"`
	Message            string          `json:"message,omitempty"`
	LastTransitionTime time.Time       `json:"lastTransitionTime"`
}

// SetCondition adds or updates a condition by type. It preserves the
// Kubernetes rule that makes lastTransitionTime meaningful: the time
// moves only when Status flips, not on every write. This is what
// lets `kubectl get` answer "how long has this machine been Ready?"
// instead of only "when did the operator last say so?".
func SetCondition(conditions []Condition, c Condition, now time.Time) []Condition {
	c.LastTransitionTime = now
	for i, existing := range conditions {
		if existing.Type != c.Type {
			continue
		}
		if existing.Status == c.Status {
			c.LastTransitionTime = existing.LastTransitionTime
		}
		conditions[i] = c
		return conditions
	}
	return append(conditions, c)
}

// FindCondition returns the condition of the named type, or nil when
// the list holds none.
func FindCondition(conditions []Condition, conditionType string) *Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

// RemoveCondition drops the condition of the named type. Most
// conditions are observations: they stay in the list and flip
// between True and False. Removal exists for the conditions that are
// grants. A grant is present while it is extended and gone when it
// is revoked. Its absence carries the meaning, so there is no False
// state for other machinery to misread as trouble.
func RemoveCondition(conditions []Condition, conditionType string) []Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return slices.Delete(conditions, i, i+1)
		}
	}
	return conditions
}
