package main

// The any-press input ensure. A press on a controller whose mark names
// this Player makes the media operator write an ensure in
// status.session.inputAsk, and the operator sends the wire command only
// when the room has drifted off the player's input. The harness is in
// session_test.go.

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// ensureAsk is the ask the media operator writes for any press.
var ensureAsk = ReceiverInputAsk{Action: "ensure", At: askAt(1)}

// driftedWhileListening is a harness whose session stands on GAME with
// the room on, and a hand on the receiver that moved it to DVD.
func driftedWhileListening(t *testing.T) (*sessionHarness, *session) {
	t.Helper()
	h, held := idleListening(t)
	held.setFlags(false, true)
	h.equipment.waitForCommands(t, denon.PowerOnCommand)
	h.equipment.waitForCommands(t, "SIGAME")
	handOnTheRemote(t, h.equipment, "SIDVD")
	h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "DVD" })
	return h, held
}

// A press after the room drifted off the player's input brings it back.
func TestAnEnsureSelectsTheInputAfterADrift(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := driftedWhileListening(t)

		held.inputAsk(ensureAsk)

		h.equipment.waitForCommands(t, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })
	})
}

// A press while the room already shows the player's input sends nothing
// to the receiver: the check is what keeps every press off the wire.
func TestAnEnsureOnTheRightInputSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := driftedWhileListening(t)
		held.inputAsk(ensureAsk)
		h.equipment.waitForCommands(t, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })

		held.inputAsk(ensureAsk)

		h.refuseCommands(t, quietPeriod, "SIGAME", "SIDVD", denon.PowerOnCommand)
	})
}

// A press on a dark room turns nothing on. The power key is the one that
// wakes the equipment, so the input ensure never powers a room on.
func TestAnEnsureInStandbySendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := idleListening(t)

		held.inputAsk(ensureAsk)

		h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "SIGAME")
	})
}

// The ensure compares the room with what the receiver reported, so it
// waits for the survey. A receiver that never answers gives it nothing
// to compare, and the ensure ends with its session, having sent and
// written nothing.
func TestAnEnsureTheReceiverNeverAnsweredEndsWithTheSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var group sync.WaitGroup
		ctx, cancel := context.WithCancel(withWork(t.Context(), &group))
		log := &logBuffer{}
		spec := ReceiverSession{Player: "theater", Input: "GAME"}
		held := startSession(ctx, "theater", spec, denon.NewClient("127.0.0.1:1", nil), newReceiverLog(log, "theater"), nil, nil)

		held.inputAsk(ensureAsk)
		cancel()

		mustMatch(t, awaitWork(&group, testTimeout), true)
		mustMatch(t, len(linesWith(log, "asks ensure")), 0)
	})
}
