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

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// The condition type is the one every liken component declares: the
// shared module's conditions.Condition, which has the JSON shape of
// metav1.Condition. The aliases keep the names that the Machine and
// Cluster packages, the operators, and the CLI already use. The
// shared package imports only time, so init, which reads these types
// and must link no HTTP client, links nothing more.
//
// ConditionStatus is a condition's verdict. It is a string rather
// than a bool because there is a third state: a controller must be
// able to say when it currently cannot tell.
type ConditionStatus = conditions.Status

const (
	ConditionTrue    = conditions.True
	ConditionFalse   = conditions.False
	ConditionUnknown = conditions.Unknown
)

// Condition mirrors metav1.Condition. ObservedGeneration records
// which metadata.generation the condition judged. Generation counts
// spec edits, so a reader can tell "Ready, for the spec as it stands"
// apart from "Ready, but for a spec two edits ago". That difference
// matters in liken, where edits wait for a reboot to take effect. (The
// convergence conditions make the stronger, content-hashed version of
// this claim. The generation is for tooling that reads the
// convention.)
//
// The CRDs require a reason of at least one character, and accept an
// empty message, so a condition encodes the same way with or without
// omitempty on those two fields.
type Condition = conditions.Condition

// SetCondition adds or updates a condition by type, through
// conditions.Set, at the time now. It preserves the Kubernetes rule
// that makes lastTransitionTime meaningful: the time moves only when
// Status flips, not on every write. This is what lets `kubectl get`
// answer "how long has this machine been Ready?" instead of only
// "when did the operator last say so?".
func SetCondition(list []Condition, c Condition, now time.Time) []Condition {
	c.LastTransitionTime = now
	conditions.Set(&list, c)
	return list
}

// Transitions answers each condition in after that transitioned from
// before, by the rule of conditions.Set: the condition is new, or its
// status or its reason changed. An operator composes a whole status,
// writes it, and then posts one Event for each transition. Comparing
// the written list with the stored one, after the write lands, means
// a refused write posts nothing, and the next pass finds the same
// transition again.
func Transitions(before, after []Condition) []Condition {
	var out []Condition
	for _, c := range after {
		held := FindCondition(before, c.Type)
		if held == nil || held.Status != c.Status || held.Reason != c.Reason {
			out = append(out, c)
		}
	}
	return out
}

// FindCondition returns the condition of the named type, or nil when
// the list holds none.
func FindCondition(list []Condition, conditionType string) *Condition {
	for i := range list {
		if list[i].Type == conditionType {
			return &list[i]
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
func RemoveCondition(list []Condition, conditionType string) []Condition {
	for i := range list {
		if list[i].Type == conditionType {
			return slices.Delete(list, i, i+1)
		}
	}
	return list
}
