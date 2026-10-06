package main

// The conditions of a Receiver, a CECBus, and a Television, and the
// Event each transition of one posts. A condition answers what is true
// now; the Event records when it changed, so `kubectl describe` shows
// the history of a receiver that dropped off the network or a TV that
// did not wake, for the hour the API server keeps an Event.

import (
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// withTransitionTime gives next its lastTransitionTime: the time held in previous
// when the status did not change, and now when it did or when previous
// holds no condition of its type. The API server keeps a date-time to
// the second, so now is cut to the second, and a condition composed
// again compares equal to the stored one.
func withTransitionTime(previous []Condition, next Condition, now time.Time) Condition {
	held := slices.Clone(previous)
	next.LastTransitionTime = now.UTC().Truncate(time.Second)
	conditions.Set(&held, next)
	stamped, _ := conditions.Find(held, next.Type)
	return stamped
}

// reference names one of this operator's objects in an Event. Each
// kind is cluster-scoped, so the recorder posts the Event in default.
func reference(kind string, meta ObjectMeta) events.ObjectReference {
	return events.ObjectReference{APIVersion: equipmentAPIVersion, Kind: kind, Name: meta.Name, UID: meta.UID}
}

// postTransitions posts one Event for each condition in written that
// transitioned from the conditions in previous: it is new, or its
// status or its reason changed. The caller calls it after the API
// server accepted the write of written, so a refused write posts
// nothing, and the next write finds the same transition again.
func postTransitions(recorder *events.Recorder, about events.ObjectReference, previous, written []Condition) {
	held := slices.Clone(previous)
	for _, condition := range written {
		if conditions.Set(&held, condition) {
			recorder.Transition(about, condition, badStatus(condition))
		}
	}
}

// badStatus answers the status that makes the condition's Event a
// Warning: its own status when its reason needs a person
// (warningReasons), and no status otherwise. InputSelected and
// InCharge follow a person's choice, so neither is ever a Warning, and
// InputSelected's Unreachable repeats the Warning that Reachable
// already posted.
func badStatus(condition Condition) conditions.Status {
	if condition.Type == inputSelectedConditionType || condition.Type == conditionInCharge {
		return ""
	}
	if warningReasons[condition.Reason] {
		return condition.Status
	}
	return ""
}
