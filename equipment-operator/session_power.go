package main

// A power request arrives in status.session.powerAsk, with the action
// toggle, on, or off. media-operator writes off when a Player's
// spec.power changes to Off. Other programs that write the session can
// write toggle and on.

import (
	"fmt"
	"strings"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// power carries out one power request. A toggle turns a room that is
// on to standby, and turns a room that is off on and selects the
// session's input. On and off send nothing when the room is already in
// that state, because HDMI-CEC 1.3a, CEC 13.13.3, defines the Power On
// and Power Off Functions to leave a device in that state when pressed
// again.
//
// When the room's TV reports its power, the TV's power decides whether
// the room is on, because the TV is what people see. Otherwise the
// receiver's power decides. After the receiver changes, `power` writes
// spec.power to match, so the next reconcile does not undo the change.
func (s *session) power(action, source string) {
	if !powerActions[action] {
		return
	}
	s.oneShot.Lock()
	defer s.oneShot.Unlock()
	// Read the state under the lock, so a power or input command that is
	// still running finishes before `power` chooses the command.
	state := s.driver.State()
	receiver := mainZone(state)
	television, power := "", ""
	if s.room != nil {
		television, power = s.room.television()
	}
	// When the receiver is unreachable, only the TV can act on the
	// request. With no TV that reports its power, `power` drops the
	// request.
	if state.Reachable != equipment.ConditionTrue {
		if television == "" || power == "" {
			return
		}
		s.toggleTelevisionOnly(source, action, television, power)
		return
	}
	on, reports := roomIsOn(receiver, television, power)
	trigger := source + " requests " + action + ", and " + reports
	switch {
	case !powerMoves(action, on):
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

var powerActions = map[string]bool{"toggle": true, "on": true, "off": true}

// powerMoves reports whether action changes the power of a room that
// is on or off.
func powerMoves(action string, on bool) bool {
	switch action {
	case "on":
		return !on
	case "off":
		return on
	}
	return true
}

// toggleTelevisionOnly turns the room's TV on or off while the receiver
// is unreachable. It does not write spec.power, because the receiver
// did not change.
func (s *session) toggleTelevisionOnly(source, action, television, power string) {
	on, reports := roomIsOn(equipment.ZoneState{}, television, power)
	trigger := source + " requests " + action + ", and " + reports
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

// roomIsOn reports whether the room is on, and returns a phrase for the
// log that says which device decided. A TV that reports On or ToOn is
// on, and any other power it reports is off.
func roomIsOn(receiver equipment.ZoneState, television, power string) (bool, string) {
	if television != "" && power != "" {
		on := strings.EqualFold(power, cec.PowerOn.String()) || strings.EqualFold(power, cec.PowerToOn.String())
		return on, fmt.Sprintf("Television %s reports power %s", television, power)
	}
	return receiver.Power == equipment.PowerOn, "the receiver reports " + powerWords(receiver, 0)
}

// turnOff puts the room's TV and the receiver in standby. A receiver
// with no standby command, such as a WiiM, stays on.
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
