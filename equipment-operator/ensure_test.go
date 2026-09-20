package main

// The any-press input ensure. A press on a controller whose mark names
// this Player asks the receiver for the player's input, and the
// operator sends the wire command only when the room has drifted off
// it. The harness is in session_test.go.

import (
	"testing"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// driftedWhileListening is a harness whose session stands on GAME with
// the room on, and a hand on the receiver that moved it to DVD.
func driftedWhileListening(t *testing.T) (*sessionHarness, *session) {
	t.Helper()
	h, _, held := idleListening(t)
	held.setFlags(false, true)
	h.equipment.waitForCommands(t, denon.PowerOnCommand)
	h.equipment.waitForCommands(t, "SIGAME")
	handOnTheRemote(t, h.equipment, "SIDVD")
	h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "DVD" })
	return h, held
}

// A press after the room drifted off the player's input brings it back.
func TestAnEnsureSelectsTheInputAfterADrift(t *testing.T) {
	h, held := driftedWhileListening(t)

	held.ensureInput()

	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })
}

// A press while the room already shows the player's input sends nothing
// to the receiver: the check is what keeps every press off the wire.
func TestAnEnsureOnTheRightInputSendsNothing(t *testing.T) {
	h, held := driftedWhileListening(t)
	held.ensureInput()
	h.equipment.waitForCommands(t, "SIGAME")
	h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })

	held.ensureInput()

	h.refuseCommands(t, quietPeriod, "SIGAME", "SIDVD", denon.PowerOnCommand)
}

// A press on a dark room turns nothing on. The power key is the one that
// wakes the equipment, so the input ensure never powers a room on.
func TestAnEnsureInStandbySendsNothing(t *testing.T) {
	h, _, held := idleListening(t)

	held.ensureInput()

	h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "SIGAME")
}

// The ask travels in player terms on the receiver's commands topic, and
// the receiver resolves the input from the session it already holds.
func TestTheEnsureCommandReachesTheStandingSession(t *testing.T) {
	h, held := driftedWhileListening(t)
	unit := &receiverUnit{session: held}

	unit.handleCommand([]byte(`{"command":"input.ensure"}`))

	h.equipment.waitForCommands(t, "SIGAME")
}

// A receiver with no session has no player listening, so the ask is
// dropped rather than sent to a room nobody asked for.
func TestTheEnsureCommandWithNoSessionSendsNothing(t *testing.T) {
	h := newSessionHarness(t)
	unit := &receiverUnit{}

	unit.handleCommand([]byte(`{"command":"input.ensure"}`))

	h.refuseCommands(t, quietPeriod, "SIGAME", denon.PowerOnCommand)
}
