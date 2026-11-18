// Package conditions defines the condition that the resources of every
// liken component report in their status, and Set, which updates a list
// of conditions.
//
// A condition tells `kubectl wait` and controllers what is true now. An
// Event tells a person what just happened, and
// events.Recorder.SetCondition posts one Event for each transition.
//
// The type has its own package so that an API type package can use it
// without importing the HTTP client that the events package needs.
//
// A component uses the type through aliases, so its CRDs and its Go
// code keep their own type names:
//
//	type Condition = conditions.Condition
//	type ConditionStatus = conditions.Status
package conditions

import "time"

// Status is the value of a condition. Unknown means the component has
// not determined it yet.
type Status string

const (
	True    Status = "True"
	False   Status = "False"
	Unknown Status = "Unknown"
)

// Condition has the JSON shape of metav1.Condition, so `kubectl
// describe` and `kubectl wait` read it like a Pod's conditions.
// ObservedGeneration is the metadata.generation that the component
// evaluated when it set the condition. LastTransitionTime is when
// Status last changed, not when the condition was last written.
type Condition struct {
	Type               string    `json:"type"`
	Status             Status    `json:"status"`
	ObservedGeneration int64     `json:"observedGeneration,omitempty"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// Set replaces the condition in list that has next's Type with next,
// or appends next when the list has none.
//
// As Kubernetes requires, Set changes LastTransitionTime only when
// Status changes, so `kubectl get` shows how long a condition has had
// its status. A new transition time is next.LastTransitionTime, or the
// current time when that is zero. The current time is truncated to the
// second because the API server stores a date-time to the second, and a
// condition that the operator builds again must compare equal to the
// stored one.
//
// Set returns true when the condition is new or its Status or Reason
// changed. A changed Message alone is not a transition, because a
// message can include a value that changes on every pass, such as a
// count or a temperature. Set still stores the new message.
func Set(list *[]Condition, next Condition) (transitioned bool) {
	for i := range *list {
		existing := &(*list)[i]
		if existing.Type != next.Type {
			continue
		}
		statusChanged := existing.Status != next.Status
		transitioned = statusChanged || existing.Reason != next.Reason
		if statusChanged {
			next.LastTransitionTime = transitionTime(next)
		} else {
			next.LastTransitionTime = existing.LastTransitionTime
		}
		*existing = next
		return transitioned
	}
	next.LastTransitionTime = transitionTime(next)
	*list = append(*list, next)
	return true
}

func transitionTime(next Condition) time.Time {
	if !next.LastTransitionTime.IsZero() {
		return next.LastTransitionTime
	}
	return time.Now().UTC().Truncate(time.Second)
}

// Find returns the condition with this type, and false when the list
// has none.
func Find(list []Condition, conditionType string) (Condition, bool) {
	for _, c := range list {
		if c.Type == conditionType {
			return c, true
		}
	}
	return Condition{}, false
}
