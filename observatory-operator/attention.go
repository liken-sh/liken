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
//
// The activation begins when the resource's Telescope or Observatory
// turns Active, and a resource's own activation run records that start
// only when the resource has activation actions. A portable telescope
// often has none, only the notify of its deactivation, so the status
// writer passes the start of the activity too. That start is gone once
// the activity ends, so a request made before the condition last turned
// False stays closed. Without that rule, the deactivation that follows
// would open the old request again before its own notify asks.

import (
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// notifyPrefix starts the record of a notify action, as its YAML reads.
const notifyPrefix = "notify: "

// attention answers the AttentionNeeded condition of a resource from
// the records of its runs, its stored conditions, and the start of its
// activity, which is zero while it is not Active. It answers false for
// a resource with no notify action that is done.
func attention(runs []observatory.ProcedureRun, stored []observatory.Condition, activeSince time.Time) (observatory.Condition, bool) {
	activated := activeSince
	var answered, cleared time.Time
	var answer string
	for _, run := range runs {
		if run.Trigger == observatory.TriggerActivation && run.StartTime != nil && run.StartTime.After(activated) {
			activated = *run.StartTime
		}
	}
	if c, ok := conditions.Find(stored, observatory.ConditionSetUp); ok && c.Reason != observatory.ReasonExpired {
		answered, answer = c.LastTransitionTime, personsAnswer(c)
	}
	held, wasCleared := conditions.Find(stored, observatory.ConditionAttentionNeeded)
	wasCleared = wasCleared && held.Status == observatory.ConditionFalse
	if wasCleared {
		cleared = held.LastTransitionTime
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
			if !a.StopTime.Before(activated) && a.StopTime.After(answered) && a.StopTime.After(cleared) {
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
	case wasCleared && !answered.After(cleared) && !activated.After(cleared):
		return held, true
	// The time of the condition's last transition moves only when its
	// status changes, so a person's answer from before the activation
	// that cleared the request can be later than it. Once the activity
	// ends, the start of that activation is gone and cannot outrank the
	// answer, so the condition keeps the reason Activated. Without this
	// case, the end of every activity would turn the old answer into a
	// new transition to Acknowledged and post its Event.
	case wasCleared && held.Reason == observatory.ReasonActivated && activated.IsZero():
		return held, true
	case answered.After(activated):
		return condition(observatory.ConditionAttentionNeeded, observatory.ConditionFalse, observatory.ReasonAcknowledged, answer), true
	}
	return condition(observatory.ConditionAttentionNeeded, observatory.ConditionFalse, observatory.ReasonActivated,
		"The activation began after the request"), true
}

// personsAnswer answers the message of the Acknowledged condition: the
// message of the person's SetUp, such as "Taken down by ada", because
// it says what the person did and who did it. A SetUp written by hand
// can have no message, and then the message says only what was done.
func personsAnswer(c observatory.Condition) string {
	if c.Message != "" {
		return sentence(c.Message)
	}
	if c.Status == observatory.ConditionTrue {
		return "Set up"
	}
	return "Taken down"
}

// withAttention adds a resource's AttentionNeeded to its composed
// conditions, when it has one.
func withAttention(composed []observatory.Condition, runs []observatory.ProcedureRun, stored []observatory.Condition, activeSince time.Time) []observatory.Condition {
	if c, ok := attention(runs, stored, activeSince); ok {
		return append(composed, c)
	}
	return composed
}
