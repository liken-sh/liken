package main

// The two session flags against the fake receiver and the fake broker:
// the session the media operator holds at the idle screen, and the
// flips a Play and a waking screen make on it. The harness these run on
// is in session_test.go.

import "testing"

// idleListening is a harness whose session stands with both flags
// false. It has adopted the level and has sent the equipment nothing.
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

	held.setFlags(true, false)

	h.equipment.waitForCommands(t, denonPowerOnCommand)
	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state denonState) bool { return state.Power == powerOn })
}

// A Play that ends returns the Player to its idle screen. The room may
// still be listening to something else, so nothing is sent.
func TestAFlipOutOfActiveSendsNothing(t *testing.T) {
	h, _, held := idleListening(t)
	held.setFlags(true, false)
	h.equipment.waitForCommands(t, "SIGAME")

	held.setFlags(false, false)

	h.refuseCommands(t, quietPeriod, denonPowerOnCommand, "PWSTANDBY", "SIGAME")
}

// A person can select another input between two Plays, so the next Play
// selects the input again.
func TestASecondFlipToActiveSelectsTheInputAgain(t *testing.T) {
	h, _, held := idleListening(t)
	held.setFlags(true, false)
	h.equipment.waitForCommands(t, "SIGAME")
	held.setFlags(false, false)
	handOnTheRemote(t, h.equipment, "SIDVD")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "DVD" })

	held.setFlags(true, false)

	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "GAME" })
}

// A flip is not a new session. One broker connection stands across the
// flips, the owner mark is never published again, and the level the
// session adopted is still the level it holds.
func TestTheFlipsKeepOneBrokerConnection(t *testing.T) {
	h, broker, held := idleListening(t)

	held.setFlags(true, false)
	h.equipment.waitForCommands(t, "SIGAME")
	held.setFlags(false, false)
	held.setFlags(true, false)
	h.equipment.waitForCommands(t, "SIGAME")

	broker.refuseTopic(t, ownerTopic(testVolumeTopic), quietPeriod)
	mustMatch(t, len(h.brokers.sessions), 0)
	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))
	h.equipment.waitForCommands(t, "MV51")
}

// The screen waking is the second trigger for the same one-shots, so it
// powers a dark room on and selects the input by itself, with no Play
// standing.
func TestAFlipToAwakePowersOnThenSelectsTheInput(t *testing.T) {
	h, _, held := idleListening(t)

	held.setFlags(false, true)

	h.equipment.waitForCommands(t, denonPowerOnCommand)
	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state denonState) bool { return state.Power == powerOn })
}

// A person can select another input while the screen sleeps, so the
// next waking selects the input again.
func TestASecondFlipToAwakeSelectsTheInputAgain(t *testing.T) {
	h, _, held := idleListening(t)
	held.setFlags(false, true)
	h.equipment.waitForCommands(t, "SIGAME")
	held.setFlags(false, false)
	handOnTheRemote(t, h.equipment, "SIDVD")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "DVD" })

	held.setFlags(false, true)

	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "GAME" })
}

// The flags are independent, so a screen that wakes under a standing
// Play selects the input again. A person pressed the receiver's own
// power button in the middle of a film, and the re-select is what puts
// the film back on the screen.
func TestAFlipToAwakeSelectsAgainUnderAStandingPlay(t *testing.T) {
	h, _, held := idleListening(t)
	held.setFlags(true, false)
	h.equipment.waitForCommands(t, "SIGAME")
	handOnTheRemote(t, h.equipment, "SIDVD")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "DVD" })

	held.setFlags(true, true)

	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "GAME" })
}

// playingOnAWokenScreen is a harness whose session starts with both
// flags on, which is the media operator applying a Play and a waking
// screen in one write. The receiver is already on, so the input
// selection is the whole of what the session sends, and the count of
// them is what the test reads.
func playingOnAWokenScreen(t *testing.T, input string) *sessionHarness {
	t.Helper()
	h := newSessionHarnessWith(t, ReceiverVolume{Max: 69.5, Step: 1})
	h.powerOn(t)
	h.beginSession(t, input, true, true)
	return h
}

// Two flags on at the start are one start and not two, so the input is
// selected once.
func TestASessionThatStartsActiveAndAwakeSelectsOnce(t *testing.T) {
	h := playingOnAWokenScreen(t, "GAME")

	h.equipment.waitForCommands(t, "SIGAME")

	h.refuseCommands(t, quietPeriod, "SIGAME")
}

// A waking screen is not a new session either. One broker connection
// stands across the awake flips, the owner mark is never published
// again, and the level the session adopted is still the level it holds.
func TestTheAwakeFlipsKeepOneBrokerConnection(t *testing.T) {
	h, broker, held := idleListening(t)

	held.setFlags(false, true)
	h.equipment.waitForCommands(t, "SIGAME")
	held.setFlags(false, false)
	held.setFlags(false, true)
	h.equipment.waitForCommands(t, "SIGAME")

	broker.refuseTopic(t, ownerTopic(testVolumeTopic), quietPeriod)
	mustMatch(t, len(h.brokers.sessions), 0)
	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))
	h.equipment.waitForCommands(t, "MV51")
}
