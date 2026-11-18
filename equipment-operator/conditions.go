package main

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// withTransitionTime returns next with the lastTransitionTime of the
// condition of the same type in previous when the status is the same,
// and now otherwise. now is truncated to the second because the API
// server stores times to the second, and an untruncated time would make
// a condition built again differ from the stored one and be written
// again. Every condition of the operator is built here, so this is
// also where its message becomes sentences.
func withTransitionTime(previous []Condition, next Condition, now time.Time) Condition {
	held := slices.Clone(previous)
	next.Message = sentences(next.Message)
	next.LastTransitionTime = now.UTC().Truncate(time.Second)
	conditions.Set(&held, next)
	stamped, _ := conditions.Find(held, next.Type)
	return stamped
}

// sentences rewrites a message built from log fragments as sentences.
// The operator logs each result after a prefix such as "Television
// lounge:", so a fragment starts in lowercase and "; " joins two
// fragments. A condition's message and the Event that repeats it are
// printed alone in a column, so there each fragment is a sentence that
// starts with a capital letter. A fragment whose first word holds a
// dot is a field path, such as spec.power, and keeps its case.
func sentences(message string) string {
	parts := strings.Split(message, "; ")
	for index, part := range parts {
		first, _, _ := strings.Cut(part, " ")
		if part == "" || strings.Contains(first, ".") {
			continue
		}
		letter, size := utf8.DecodeRuneInString(part)
		parts[index] = string(unicode.ToUpper(letter)) + part[size:]
	}
	return strings.Join(parts, ". ")
}

// reference returns the object reference for an Event about one of this
// operator's objects. These kinds are cluster-scoped, so the reference
// has no namespace, and the recorder posts the Event in `default`.
func reference(kind string, meta ObjectMeta) events.ObjectReference {
	return events.ObjectReference{APIVersion: equipmentAPIVersion, Kind: kind, Name: meta.Name, UID: meta.UID}
}

// postTransitions posts one Event for each condition in written that is
// new, or whose status or reason differs from the same condition in
// previous. Call it only after the API server accepts the write, so a
// refused write posts nothing and the next write posts the transition.
func postTransitions(recorder *events.Recorder, about events.ObjectReference, previous, written []Condition) {
	held := slices.Clone(previous)
	for _, condition := range written {
		if conditions.Set(&held, condition) {
			recorder.Transition(about, condition, badStatus(condition))
		}
	}
}

// badStatus returns the status that events.Recorder.Transition posts as
// a Warning: the condition's own status when its reason is in
// warningReasons, and empty otherwise. InputSelected is never a
// Warning, because it changes when a person picks another input, and
// its Unreachable reason repeats the Warning that Reachable posts.
// InCharge is never a Warning, because AnotherInCharge only means that
// another Television of the same bus applies spec.power.
func badStatus(condition Condition) conditions.Status {
	if condition.Type == inputSelectedConditionType || condition.Type == conditionInCharge {
		return ""
	}
	if warningReasons[condition.Reason] {
		return condition.Status
	}
	return ""
}
