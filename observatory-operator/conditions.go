package main

// The conditions of every kind, in the shape of metav1.Condition. A
// condition's lastTransitionTime is when its status last changed, so a
// write that keeps the status keeps the time, and a status that holds
// the same facts as the stored one compares equal and is not written.

import (
	"reflect"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func condition(kind string, status observatory.ConditionStatus, reason, message string) observatory.Condition {
	return observatory.Condition{
		Type: kind, Status: status, Reason: reason, Message: message,
		LastTransitionTime: stamp(),
	}
}

// mergeConditions answers the next conditions, with the transition time
// of each stored condition whose status did not change, and each next
// condition that transitioned from the stored one (conditions.Set). A
// stored condition of a type that next leaves out is dropped.
func mergeConditions(stored, next []observatory.Condition) (merged, transitions []observatory.Condition) {
	merged = make([]observatory.Condition, len(next))
	for i, c := range next {
		var held []observatory.Condition
		if old, ok := conditions.Find(stored, c.Type); ok {
			held = []observatory.Condition{old}
		}
		if conditions.Set(&held, c) {
			transitions = append(transitions, held[0])
		}
		merged[i] = held[0]
	}
	return merged, transitions
}

// equalJSON reports whether two values are the same JSON document
// (asJSON in tree.go).
func equalJSON(a, b any) bool {
	return reflect.DeepEqual(asJSON(a), asJSON(b))
}

// stamp answers the time a record holds: UTC, to the second, which is
// what the API server keeps of a date-time.
func stamp() time.Time { return time.Now().UTC().Truncate(time.Second) }

// conditionOf answers the condition of one type, or the zero condition.
func conditionOf(list []observatory.Condition, kind string) observatory.Condition {
	c, _ := conditions.Find(list, kind)
	return c
}
