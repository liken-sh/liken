package main

// The AttentionNeeded condition. A notify action asks a person to do
// what no driver can, such as covering a portable telescope at the end
// of a night. The action is done once its run records it, and the
// status writer reads the records of the resource's runs and sets
// AttentionNeeded, whose transition to True posts a Warning Event, so
// one Event reaches the person, and an alert on the condition can
// reach their phone. Plan 21 gives the design.
//
// The condition clears at the start of the resource's next activation,
// because the equipment is in use again, and when a person writes the
// resource's SetUp after the request, because writing it is the
// person's answer: take-down after covering the telescope, or set-up
// after carrying it out again. An expiry at dawn is the operator's
// write, not the person's, so it clears nothing.

import (
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// notifyPrefix starts the record of a notify action, as its YAML reads.
const notifyPrefix = "notify: "

// attention answers the AttentionNeeded condition of a resource from
// the records of its runs and its stored conditions, and false for a
// resource with no notify action that is done.
func attention(runs []observatory.ProcedureRun, stored []observatory.Condition) (observatory.Condition, bool) {
	var activated, answered time.Time
	for _, run := range runs {
		if run.Trigger == observatory.TriggerActivation && run.StartTime != nil {
			activated = *run.StartTime
		}
	}
	if c, ok := conditions.Find(stored, observatory.ConditionSetUp); ok && c.Reason != observatory.ReasonExpired {
		answered = c.LastTransitionTime
	}
	asked := false
	var open []string
	for _, run := range runs {
		for _, a := range run.Actions {
			text, isNotify := strings.CutPrefix(a.Action, notifyPrefix)
			if !isNotify || a.State != observatory.StepDone || a.StopTime == nil {
				continue
			}
			asked = true
			// A request that an activation run made itself is open
			// while that run is the last activation.
			if !a.StopTime.Before(activated) && a.StopTime.After(answered) {
				open = append(open, sentence(strings.TrimSuffix(text, ".")))
			}
		}
	}
	switch {
	case !asked:
		return observatory.Condition{}, false
	case len(open) > 0:
		return condition(observatory.ConditionAttentionNeeded, observatory.ConditionTrue, observatory.ReasonNotified,
			strings.Join(open, ". ")), true
	case answered.After(activated):
		return condition(observatory.ConditionAttentionNeeded, observatory.ConditionFalse, observatory.ReasonAcknowledged,
			"A person wrote SetUp after the request"), true
	}
	return condition(observatory.ConditionAttentionNeeded, observatory.ConditionFalse, observatory.ReasonActivated,
		"The activation began after the request"), true
}

// withAttention adds a resource's AttentionNeeded to its composed
// conditions, when it has one.
func withAttention(composed []observatory.Condition, runs []observatory.ProcedureRun, stored []observatory.Condition) []observatory.Condition {
	if c, ok := attention(runs, stored); ok {
		return append(composed, c)
	}
	return composed
}
