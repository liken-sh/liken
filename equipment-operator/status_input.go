package main

// The InputSelected condition: whether the receiver reports the input
// of the session that stands. The media operator reads it to decide
// whether a press needs an inputAsk of ensure, so a press on a room
// that already shows the Player costs no API write.

import (
	"fmt"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

const (
	inputSelectedConditionType = "InputSelected"
	reasonSessionInput         = "SessionInput"
	reasonOtherInput           = "OtherInput"
	reasonNoInputReported      = "NoInputReported"
)

// onSessionInput answers whether a zone reports the session's input.
// The ensure and the condition both use it, so the condition is True
// exactly when an ensure finds no input to send.
func onSessionInput(zone equipment.ZoneState, input string) bool {
	return zone.Input == input
}

// sessionInput answers the session that stands, without its flags and
// asks, and nil when none stands.
func (u *receiverUnit) sessionInput() *ReceiverSession {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.session == nil {
		return nil
	}
	spec := u.session.spec
	return &spec
}

// inputSelected builds the condition while a session stands. It is
// Unknown while the operator cannot reach the receiver or the receiver
// has reported no input, because the last input it reported can be
// stale. It keeps the moment the verdict last changed.
func inputSelected(session *ReceiverSession, state equipment.State, generation int64, previous []Condition, now time.Time) (Condition, bool) {
	if session == nil {
		return Condition{}, false
	}
	zone := mainZone(state)
	condition := Condition{
		Type:               inputSelectedConditionType,
		ObservedGeneration: generation,
		LastTransitionTime: timestamp(now),
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
	for _, held := range previous {
		if held.Type == inputSelectedConditionType && held.Status == condition.Status && held.LastTransitionTime != "" {
			condition.LastTransitionTime = held.LastTransitionTime
		}
	}
	return condition, true
}
