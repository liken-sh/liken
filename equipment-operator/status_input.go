package main

// The InputSelected condition says whether the receiver reports the
// input of the running session. media-operator writes an ensure
// request to status.session.inputAsk on a press only while the
// condition is not True, so most presses cost no API write.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// onSessionInput reports whether the zone's input is the session's
// input. The ensure and the condition both use it, so the condition is
// True exactly when an ensure finds the input already selected.
func onSessionInput(zone equipment.ZoneState, input string) bool {
	return zone.Input == input
}

// sessionInput returns a copy of the running session's spec, without
// Active, Awake, and the requests, or nil when no session is running.
func (u *receiverUnit) sessionInput() *ReceiverSession {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.session == nil {
		return nil
	}
	spec := u.session.spec
	return &spec
}

// inputSelected builds the InputSelected condition and returns false
// when no session is running. The condition is Unknown while the
// operator cannot reach the receiver or the receiver has reported no
// input, because the last input it reported can be stale. The
// condition keeps its previous transition time while its status does
// not change.
func inputSelected(session *ReceiverSession, state equipment.State, generation int64, previous []Condition, now time.Time) (Condition, bool) {
	if session == nil {
		return Condition{}, false
	}
	zone := mainZone(state)
	condition := Condition{
		Type:               inputSelectedConditionType,
		ObservedGeneration: generation,
	}
	switch {
	case state.Reachable != ConditionTrue:
		condition.Status, condition.Reason = ConditionUnknown, reasonUnreachable
		condition.Message = "the operator cannot reach the receiver to read its input"
	case zone.Input == "":
		condition.Status, condition.Reason = ConditionUnknown, reasonNoInputReported
		condition.Message = "the receiver has reported no input"
	case onSessionInput(zone, session.Input):
		condition.Status, condition.Reason = ConditionTrue, reasonSessionInput
		condition.Message = fmt.Sprintf("the receiver reports input %s, the input of Player %s's session", zone.Input, session.Player)
	default:
		condition.Status, condition.Reason = ConditionFalse, reasonOtherInput
		condition.Message = fmt.Sprintf("the receiver reports input %s, and Player %s's session uses input %s", zone.Input, session.Player, session.Input)
	}
	return withTransitionTime(previous, condition, now), true
}
