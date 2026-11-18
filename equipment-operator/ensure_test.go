package main

// Tests of the ensure request. When a person presses a button on a
// controller that currently drives this Player, media-operator writes
// status.session.inputAsk with the action ensure. The operator then
// selects the Player's input only when the receiver is on and reports a
// different input.

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// ensureAsk is the inputAsk that media-operator writes for a press.
var ensureAsk = ReceiverInputAsk{Action: "ensure", At: askAt(1)}

// driftedWhileListening returns a harness whose session has selected
// GAME on a receiver that is on, after the receiver's own remote
// changed its input to DVD.
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

// A press after the receiver's input drifted away from the Player's
// selects the Player's input again.
func TestAnEnsureSelectsTheInputAfterADrift(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := driftedWhileListening(t)

		held.inputAsk(ensureAsk)

		h.equipment.waitForCommands(t, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })
	})
}

// A press while the receiver already shows the Player's input sends
// the receiver nothing.
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

// A press while the receiver is in standby sends nothing, because only
// the power key turns the receiver on.
func TestAnEnsureInStandbySendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := idleListening(t)

		held.inputAsk(ensureAsk)

		h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "SIGAME")
	})
}

// The ensure waits for the first read of the receiver's state, because
// it compares the receiver's input with the Player's. When the receiver
// never answers, the ensure returns when the session ends, and it sends
// no command and writes no log line.
func TestAnEnsureEndsWithItsSessionWhenTheReceiverNeverAnswers(t *testing.T) {
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
		mustMatch(t, len(linesWith(log, "requests ensure")), 0)
	})
}
