package main

// The AttentionNeeded condition. A notify action asks a person to do
// something that no driver can, such as cover a portable telescope at
// the end of a night. The action does not wait for the person. It
// finishes as soon as its run records it. The status writer then sets
// AttentionNeeded to True with the request as its message. The
// transition posts a Warning Event, and an alert on the condition can
// notify the person.
//
// AttentionNeeded turns False with the reason Activated when the
// resource's next activation starts, because the equipment is in use
// again. It turns False with the reason Acknowledged when a person
// writes the resource's SetUp after the request, such as a take-down
// after covering the telescope. A SetUp that expired at dawn was
// written by the operator, so it does not acknowledge anything.
//
// An activation run records its start only when the resource has
// activation actions, and a portable telescope often has only the
// notify of its deactivation. So the caller also passes activeSince,
// the time the resource's Telescope or Observatory turned Active.
// activeSince is zero again once the activity ends. A request is
// therefore also compared with the time the condition last turned
// False, or the old request would count as open again until the next
// deactivation's notify runs.

import (
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// notifyPrefix starts the record of a notify action, as its YAML reads.
const notifyPrefix = "notify: "

// attention returns the AttentionNeeded condition of a resource, or
// false when no notify action of the resource has finished.
func attention(runs []observatory.ProcedureRun, stored []observatory.Condition, activeSince time.Time) (observatory.Condition, bool) {
	activated := activeSince
	var acknowledgedAt, falseSince time.Time
	var acknowledgment string
	for _, run := range runs {
		if run.Trigger == observatory.TriggerActivation && run.StartTime != nil && run.StartTime.After(activated) {
			activated = *run.StartTime
		}
	}
	if c, ok := conditions.Find(stored, observatory.ConditionSetUp); ok && c.Reason != observatory.ReasonExpired {
		acknowledgedAt, acknowledgment = c.LastTransitionTime, acknowledgedMessage(c)
	}
	previous, wasFalse := conditions.Find(stored, observatory.ConditionAttentionNeeded)
	wasFalse = wasFalse && previous.Status == observatory.ConditionFalse
	if wasFalse {
		falseSince = previous.LastTransitionTime
	}
	hasNotify := false
	var requests []string
	for _, run := range runs {
		for _, a := range run.Actions {
			text, isNotify := strings.CutPrefix(a.Action, notifyPrefix)
			if !isNotify || a.State != observatory.StepDone || a.StopTime == nil {
				continue
			}
			hasNotify = true
			// A request is open when it finished at or after the latest
			// activation, and after both the person's acknowledgment and
			// the last time the condition turned False.
			if !a.StopTime.Before(activated) && a.StopTime.After(acknowledgedAt) && a.StopTime.After(falseSince) {
				requests = append(requests, sentence(strings.TrimSuffix(text, ".")))
			}
		}
	}
	switch {
	case !hasNotify:
		return observatory.Condition{}, false
	case len(requests) > 0:
		return condition(observatory.ConditionAttentionNeeded, observatory.ConditionTrue, observatory.ReasonNotified,
			strings.Join(requests, ". ")), true
	case wasFalse && !acknowledgedAt.After(falseSince) && !activated.After(falseSince):
		return previous, true
	// The condition turned False with Activated, and the activity has
	// since ended, so activated is zero. A SetUp written before that
	// activation can still be later than falseSince, because
	// LastTransitionTime changes only when the status changes. Keep the
	// stored condition, or the old SetUp would post a new Acknowledged
	// transition and Event at the end of every activity.
	case wasFalse && previous.Reason == observatory.ReasonActivated && activated.IsZero():
		return previous, true
	case acknowledgedAt.After(activated):
		return condition(observatory.ConditionAttentionNeeded, observatory.ConditionFalse, observatory.ReasonAcknowledged, acknowledgment), true
	}
	return condition(observatory.ConditionAttentionNeeded, observatory.ConditionFalse, observatory.ReasonActivated,
		"The activation began after the request"), true
}

// acknowledgedMessage returns the SetUp message, such as "Taken down by
// ada", which says what the person did and who did it. A SetUp written
// by hand can have no message, and then the result says only what was
// done.
func acknowledgedMessage(c observatory.Condition) string {
	if c.Message != "" {
		return sentence(c.Message)
	}
	if c.Status == observatory.ConditionTrue {
		return "Set up"
	}
	return "Taken down"
}

func withAttention(composed []observatory.Condition, runs []observatory.ProcedureRun, stored []observatory.Condition, activeSince time.Time) []observatory.Condition {
	if c, ok := attention(runs, stored, activeSince); ok {
		return append(composed, c)
	}
	return composed
}
