package main

// The conditions of every kind, in the shape of metav1.Condition. A
// condition's lastTransitionTime is when its status last changed, so a
// write that keeps the status keeps the time, and a status that holds
// the same facts as the stored one compares equal and is not written.

import (
	"reflect"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func condition(kind string, status observatory.ConditionStatus, reason, message string) observatory.Condition {
	return observatory.Condition{
		Type: kind, Status: status, Reason: reason, Message: message,
		LastTransitionTime: stamp(),
	}
}

// mergeConditions answers the next conditions, with the transition time
// of each stored condition whose status did not change.
func mergeConditions(stored, next []observatory.Condition) []observatory.Condition {
	out := make([]observatory.Condition, len(next))
	for i, c := range next {
		for _, old := range stored {
			if old.Type == c.Type && old.Status == c.Status {
				c.LastTransitionTime = old.LastTransitionTime
			}
		}
		out[i] = c
	}
	return out
}

// equalJSON reports whether two values are the same JSON document
// (asJSON in tree.go).
func equalJSON(a, b any) bool {
	return reflect.DeepEqual(asJSON(a), asJSON(b))
}

// stamp answers the time a record holds: UTC, to the second, which is
// what the API server keeps of a date-time.
func stamp() time.Time { return time.Now().UTC().Truncate(time.Second) }
