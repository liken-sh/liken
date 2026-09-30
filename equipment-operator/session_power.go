package main

// The room's power on the session's power topic: the toggle the remote's
// power button publishes, and the on and off a TV remote's deterministic
// power functions publish.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/equipment"
)

// togglePower turns the room on or off on an ask on the power topic.
// The remote's power button publishes {"action":"toggle"}, and the
// operator answers it the way a power button does: a room that is on
// goes to standby, and one that is not comes on and selects the
// session's input. A TV remote's Power Off Function and Power On
// Function publish {"action":"off"} and {"action":"on"}: HDMI-CEC 1.3a,
// CEC 13.13.3, says each puts the device in the state it names and
// keeps it there when repeated, so an ask for the state the room
// already holds sends nothing. The room's TV decides whether the room
// is on, when the room has one that reports its power, because the TV
// is what a person sees; otherwise the receiver decides. A room that
// goes off asks its TV for standby and puts the receiver in standby. A
// room that comes on wakes its TV and turns the receiver on. The spec's
// power field is updated to match what the receiver did, so the next
// reconcile sees no change to re-assert. A receiver the operator
// cannot reach gets nothing, and the TV, when it reports its power,
// still turns off or on. Anything else on the topic, such as the asks
// this operator publishes for the Player's screen
// (television_screen.go), a malformed body, or an empty payload, does
// nothing.
func (s *session) togglePower(payload []byte) {
	var event powerAsk
	if err := json.Unmarshal(payload, &event); err != nil || !powerActions[event.Action] {
		return
	}
	s.oneShot.Lock()
	defer s.oneShot.Unlock()
	// The power is read under the lock, so the decision is made against
	// the receiver as it stands after any in-flight one-shot settles, not
	// a snapshot taken a moment earlier.
	state := s.driver.State()
	receiver := mainZone(state)
	television, power := "", ""
	if s.room != nil {
		television, power = s.room.television()
	}
	// A receiver the operator cannot reach has no power to read, and
	// nothing to command. A TV that reports its power still decides the
	// room, so the press reaches the TV and skips only the receiver. With
	// no such TV, nothing decides the room, and the ask is dropped and
	// never queued.
	if state.Reachable != equipment.ConditionTrue {
		if television == "" || power == "" {
			return
		}
		s.toggleTelevisionOnly(event.Action, television, power)
		return
	}
	on, reports := roomIsOn(receiver, television, power)
	trigger := "the power topic asks " + event.Action + ", and " + reports
	switch {
	case !powerMoves(event.Action, on):
		s.log.printf("%s; sent nothing, because the room is already %s", trigger, onOff(on))
	case on:
		s.turnOff(trigger, receiver, television)
	default:
		if s.room != nil {
			s.room.woke(trigger)
		}
		s.selectInputLocked(s.ctx, trigger)
		if s.applyPower != nil {
			s.applyPower(equipment.PowerOn)
		}
	}
}

// powerActions are the asks on the power topic that move the room's
// power.
var powerActions = map[string]bool{"toggle": true, "on": true, "off": true}

// powerMoves answers whether an ask moves a room that is on or off: a
// toggle always does, and on and off only when the room is not already
// in the state they name.
func powerMoves(action string, on bool) bool {
	switch action {
	case "on":
		return !on
	case "off":
		return on
	}
	return true
}

// toggleTelevisionOnly turns the room's TV off or on for an ask that
// finds the receiver unreachable, and writes one line that says the
// receiver got nothing. The spec's power field stays as it is, because
// the receiver did nothing.
func (s *session) toggleTelevisionOnly(action, television, power string) {
	on, reports := roomIsOn(equipment.ZoneState{}, television, power)
	trigger := "the power topic asks " + action + ", and " + reports
	switch {
	case !powerMoves(action, on):
		s.log.printf("%s; sent nothing, because the room is already %s", trigger, onOff(on))
		return
	case on:
		s.room.standby(trigger)
	default:
		s.room.woke(trigger)
	}
	s.log.printf("%s; sent the receiver nothing, because the operator cannot reach it", trigger)
}

// roomIsOn answers whether a power press finds the room on, and the
// words that name what decided it. A TV that reports On, or ToOn on
// its way there, is a room that is on, and a TV in Standby or ToStandby
// is a room that is off. With no TV, or a TV that does not answer, the
// receiver's power decides.
func roomIsOn(receiver equipment.ZoneState, television, power string) (bool, string) {
	if television != "" && power != "" {
		on := strings.EqualFold(power, cec.PowerOn.String()) || strings.EqualFold(power, cec.PowerToOn.String())
		return on, fmt.Sprintf("Television %s reports power %s", television, power)
	}
	return receiver.Power == equipment.PowerOn, "the receiver reports " + powerWords(receiver, 0)
}

// turnOff puts the room in standby: its TV, when it has one, and the
// receiver, when the receiver has a standby. A receiver with no
// standby, such as a WiiM, stays on, and its line says so as the
// outcome of the press.
func (s *session) turnOff(trigger string, receiver equipment.ZoneState, television string) {
	switch {
	case s.room == nil:
	case television != "":
		s.room.standby(trigger)
	default:
		s.room.slept()
	}
	if !s.driver.HasStandby() {
		s.log.printf("%s; sent the receiver nothing, because it has no standby command, so it stays on", trigger)
		return
	}
	if receiver.Power != equipment.PowerOn {
		s.log.printf("%s; sent the receiver nothing, because it reports %s", trigger, powerWords(receiver, 0))
		if s.applyPower != nil {
			s.applyPower(equipment.PowerStandby)
		}
		return
	}
	line := trigger + "; sent power Standby"
	began := time.Now()
	if err := s.driver.SetPower(equipment.MainZone, false); err != nil {
		s.log.refused(line, err)
		return
	}
	s.log.confirm(line, began, mainZoneCheck(s.driver, "power Standby", powerWords))
	if s.applyPower != nil {
		s.applyPower(equipment.PowerStandby)
	}
}
