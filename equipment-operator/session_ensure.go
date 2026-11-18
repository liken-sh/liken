package main

import "github.com/liken-sh/liken/equipment-operator/equipment"

// show handles a home press. Selecting the receiver's input is not
// enough, because a TV that is showing its own apps stays on its own
// input. So show also asks the room's TV to show the session's Display,
// then selects the input as ensure does. Neither step turns on a TV or a
// receiver that is off. trigger is the reason in the log lines.
func (s *session) show(trigger string) {
	if s.room != nil {
		s.room.show(trigger)
	}
	s.ensure(trigger)
}

// ensure selects the session's input on the receiver, for any
// controller press. It sends nothing when the receiver is already on
// that input or is not on, because a press of another key does not
// turn the equipment on. trigger is the reason in the log lines.
func (s *session) ensure(trigger string) {
	if s.spec.Input == "" {
		s.log.printf("%s; sent nothing, because Player %s's session names no input", trigger, s.spec.Player)
		return
	}
	goWork(s.ctx, func() { s.ensureInputOnce(trigger) })
}

// ensureInputOnce holds oneShot, so its commands never interleave with
// a power toggle or a change of the session's flags.
func (s *session) ensureInputOnce(trigger string) {
	s.oneShot.Lock()
	defer s.oneShot.Unlock()
	if !s.waitForFirstRead(s.ctx) {
		return
	}
	state := mainZone(s.driver.State())
	if state.Power != equipment.PowerOn {
		s.log.printf("%s; sent nothing, because the receiver reports %s", trigger, powerWords(state, 0))
		return
	}
	if onSessionInput(state, s.spec.Input) {
		s.log.printf("%s; sent nothing, because the receiver reports %s", trigger, inputWords(state, 0))
		return
	}
	s.selectTheInput(trigger)
}
