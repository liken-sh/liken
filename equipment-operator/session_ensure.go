package main

// The asks a controller press makes in player terms: the ensure, which
// any press sends, and the show, which a home press sends. They arrive
// in status.session.inputAsk (session_asks.go).

import "github.com/liken-sh/equipment-operator/equipment"

// show asks the room's TV to show the session's Display, and then asks
// the receiver for the session's input the way the ensure does. A home
// press reaches here. A TV that shows its own apps is on another input,
// and the receiver's input alone does not bring the player back to the
// screen. The TV decides whether the room is on for its part: the node
// workload sends it Image View On and Active Source only when it
// reports On, so the ask wakes no TV that is off, and the receiver part
// turns on no receiver that is off. trigger names the ask in its lines.
func (s *session) show(trigger string) {
	if s.room != nil {
		s.room.show(trigger)
	}
	s.ensure(trigger)
}

// ensure asks the receiver for the session's input without touching
// the power. A controller press reaches here, and a room that already
// shows the player's input is left alone, so the press sends the
// receiver nothing. A dark room is left dark too: the power key is the
// one that wakes the equipment. trigger names the ask in its lines.
func (s *session) ensure(trigger string) {
	if s.spec.Input == "" {
		s.log.printf("%s; sent nothing, because Player %s's session names no input", trigger, s.spec.Player)
		return
	}
	goWork(s.ctx, func() { s.ensureInputOnce(trigger) })
}

// ensureInputOnce is the ensure under the one-shot lock, serialized
// against a toggle and a flag flip so the three never drive the
// receiver at the same moment. trigger names the ask that reached it,
// for its lines.
func (s *session) ensureInputOnce(trigger string) {
	s.oneShot.Lock()
	defer s.oneShot.Unlock()
	if !s.waitForSurvey(s.ctx) {
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
