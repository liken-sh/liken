package events

import "github.com/liken-sh/liken/kubernetes/conditions"

// SetCondition sets next in the list with conditions.Set and posts one
// Event when the condition transitioned, meaning it is new or its
// status or reason changed. It returns whether it transitioned.
//
// A caller that writes the status later, and must post the Event only
// after the write succeeds, calls conditions.Set while it builds the
// status and Transition after the write.
func (r *Recorder) SetCondition(object ObjectReference, list *[]conditions.Condition, next conditions.Condition, bad conditions.Status) bool {
	transitioned := conditions.Set(list, next)
	if transitioned {
		r.Transition(object, next, bad)
	}
	return transitioned
}

// Transition posts the Event for one condition transition. The Event
// has the condition's reason and message, so `kubectl get -o yaml` and
// `kubectl describe` show the same text.
//
// bad is the status that needs a person to act. A transition to it
// posts a Warning, and any other transition posts a Normal Event. Pass
// conditions.False for a condition such as Ready, conditions.True for
// one such as Stalled, and "" for a condition that never needs a
// person. When only some reasons of a status need a person, such as a
// Ready that is False both while a device starts and when it fails,
// pass conditions.False only when the reason is a failure, and ""
// otherwise.
func (r *Recorder) Transition(object ObjectReference, c conditions.Condition, bad conditions.Status) {
	if bad != "" && c.Status == bad {
		r.Warning(object, c.Reason, c.Message)
		return
	}
	r.Normal(object, c.Reason, c.Message)
}
