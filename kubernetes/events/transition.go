package events

import "github.com/liken-sh/liken/kubernetes/conditions"

// SetCondition puts next in the list with conditions.Set, and posts one
// Event when the condition transitioned: it is new, or its status or
// its reason changed. It answers whether it transitioned.
//
// A caller that writes the status later, and must post only when the
// write lands, calls conditions.Set while it composes and Transition
// after the write.
func (r *Recorder) SetCondition(object ObjectReference, list *[]conditions.Condition, next conditions.Condition, bad conditions.Status) bool {
	transitioned := conditions.Set(list, next)
	if transitioned {
		r.Transition(object, next, bad)
	}
	return transitioned
}

// Transition posts the Event of one condition transition, with the
// condition's reason and message, so `kubectl get -o yaml` and
// `kubectl describe` show the same words.
//
// bad is the status that needs a person: a transition to it is a
// Warning, and any other is Normal. Pass conditions.False for a
// condition such as Ready, conditions.True for one such as Stalled,
// and "" for a condition no status of which needs a person. A caller
// whose condition is bad only for some reasons, such as a Ready that is
// False both while a device starts and when it fails, passes bad only
// with the failing reasons.
func (r *Recorder) Transition(object ObjectReference, c conditions.Condition, bad conditions.Status) {
	if bad != "" && c.Status == bad {
		r.Warning(object, c.Reason, c.Message)
		return
	}
	r.Normal(object, c.Reason, c.Message)
}
