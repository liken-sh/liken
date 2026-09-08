package main

// The active flag against the fake receiver and the fake broker: the
// session the media operator holds at the idle screen, and the flips a
// Play makes on it. The harness these run on is in session_test.go.

import "testing"

// idleListening is a harness whose session stands with active false. It
// has adopted the level and has sent the equipment nothing.
func idleListening(t *testing.T) (*sessionHarness, *fakeBroker, *session) {
	t.Helper()
	h := newSessionHarnessWith(t, ReceiverVolume{Max: 69.5, Step: 1})
	held := h.beginIdle(t, "GAME")
	broker, _ := adoptTheLevel(t, h, held)
	return h, broker, held
}

// A Player at its idle screen holds a session on the receiver, so an
// idle session must leave a dark room dark.
func TestAnIdleSessionSendsNoPowerOrInput(t *testing.T) {
	h, _, _ := idleListening(t)

	h.refuseCommands(t, quietPeriod, denonPowerOnCommand, "SIGAME")

	mustMatch(t, h.denon.State().Power, powerStandby)
}

// The level is the idle session's whole job, and it does it with the
// receiver in standby.
func TestAnIdleSessionStillStepsTheVolume(t *testing.T) {
	h, broker, _ := idleListening(t)

	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))

	h.equipment.waitForCommands(t, "MV51")
	h.waitUntil(t, func(state denonState) bool { return state.Volume == 102 })
}

func TestAFlipToActivePowersOnThenSelectsTheInput(t *testing.T) {
	h, _, held := idleListening(t)

	held.setActive(true)

	h.equipment.waitForCommands(t, denonPowerOnCommand)
	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state denonState) bool { return state.Power == powerOn })
}

// A Play that ends returns the Player to its idle screen. The room may
// still be listening to something else, so nothing is sent.
func TestAFlipOutOfActiveSendsNothing(t *testing.T) {
	h, _, held := idleListening(t)
	held.setActive(true)
	h.equipment.waitForCommands(t, "SIGAME")

	held.setActive(false)

	h.refuseCommands(t, quietPeriod, denonPowerOnCommand, "PWSTANDBY", "SIGAME")
}

// A person can select another input between two Plays, so the next Play
// selects the input again.
func TestASecondFlipToActiveSelectsTheInputAgain(t *testing.T) {
	h, _, held := idleListening(t)
	held.setActive(true)
	h.equipment.waitForCommands(t, "SIGAME")
	held.setActive(false)
	handOnTheRemote(t, h.equipment, "SIDVD")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "DVD" })

	held.setActive(true)

	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "GAME" })
}

// A flip is not a new session. One broker connection stands across the
// flips, the owner mark is never published again, and the level the
// session adopted is still the level it holds.
func TestTheFlipsKeepOneBrokerConnection(t *testing.T) {
	h, broker, held := idleListening(t)

	held.setActive(true)
	h.equipment.waitForCommands(t, "SIGAME")
	held.setActive(false)
	held.setActive(true)
	h.equipment.waitForCommands(t, "SIGAME")

	broker.refuseTopic(t, ownerTopic(testVolumeTopic), quietPeriod)
	mustMatch(t, len(h.brokers.sessions), 0)
	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))
	h.equipment.waitForCommands(t, "MV51")
}
