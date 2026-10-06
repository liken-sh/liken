// Package conditions holds the condition type that the resources of
// every liken component report in their status, and the setter that
// changes a list of them.
//
// A condition answers "what is true now" for automation:
// `kubectl wait --for=condition=Ready` and a controller read it. An
// Event answers "what just happened" for a person, and the events
// package posts one Event for each condition transition
// (events.Recorder.SetCondition). The type is in its own package so
// that an API type package can declare it in its resources and link
// only the time package, not the HTTP client that the events package
// needs.
//
// A component adopts the type with an alias, so its CRDs and its Go
// code keep their names:
//
//	type Condition = conditions.Condition
//	type ConditionStatus = conditions.Status
package conditions

import "time"

// Status is a condition's verdict. Unknown is a third state: the
// component cannot tell yet.
type Status string

const (
	True    Status = "True"
	False   Status = "False"
	Unknown Status = "Unknown"
)

// Condition has the JSON shape of metav1.Condition, so
// `kubectl describe` and `kubectl wait` read it the way they read a
// Pod's conditions. ObservedGeneration records which
// metadata.generation the condition judged. LastTransitionTime is when
// the status last changed, not when the condition was last written.
type Condition struct {
	Type               string    `json:"type"`
	Status             Status    `json:"status"`
	ObservedGeneration int64     `json:"observedGeneration,omitempty"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// Set puts next in the list in place of the condition of its type, or
// appends it when the list holds none.
//
// Set keeps the Kubernetes rule that makes lastTransitionTime
// meaningful: the time moves only when the status changes. So
// `kubectl get` answers "how long has this been Ready?" and not "when
// did the operator last write?". When the status changes, the
// condition takes next's LastTransitionTime, or the present time when
// next states none. The present time is cut to the second, because the
// API server keeps a date-time to the second, and a status composed
// again must compare equal to the stored one.
//
// Set answers whether the condition transitioned: it is new, or its
// status or its reason changed. A new message alone is no transition,
// because a message often carries a value that changes on each pass,
// such as a count or a temperature. The list still takes the message.
func Set(list *[]Condition, next Condition) (transitioned bool) {
	for i := range *list {
		held := &(*list)[i]
		if held.Type != next.Type {
			continue
		}
		statusChanged := held.Status != next.Status
		transitioned = statusChanged || held.Reason != next.Reason
		if statusChanged {
			next.LastTransitionTime = transitionTime(next)
		} else {
			next.LastTransitionTime = held.LastTransitionTime
		}
		*held = next
		return transitioned
	}
	next.LastTransitionTime = transitionTime(next)
	*list = append(*list, next)
	return true
}

// transitionTime answers the time a condition that changed its status
// records.
func transitionTime(next Condition) time.Time {
	if !next.LastTransitionTime.IsZero() {
		return next.LastTransitionTime
	}
	return time.Now().UTC().Truncate(time.Second)
}

// Find answers the condition of one type, and false when the list
// holds none.
func Find(list []Condition, conditionType string) (Condition, bool) {
	for _, c := range list {
		if c.Type == conditionType {
			return c, true
		}
	}
	return Condition{}, false
}
