package main

// These tests change a session's flags the way a Play and a waking
// screen do: active is true while a Play runs, and awake is true while
// the screen is awake. session_test.go has the harness.

import (
	"context"
	"sync"
	"time"

	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// idleListening returns a session whose flags are both false, which
// has sent the receiver nothing.
func idleListening(t *testing.T) (*sessionHarness, *session) {
	t.Helper()
	h := newSessionHarness(t)
	return h, h.beginIdle(t, "GAME")
}

// A Player at its idle screen has a session open on the receiver. It
// must not turn the receiver on or select an input, because nobody is
// using the room.
func TestAnIdleSessionSendsNoPowerOrInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, _ := idleListening(t)

		h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "SIGAME")

		mustMatch(t, mainZone(h.denon.State()).Power, equipment.PowerStandby)
	})
}

func TestAFlipToActivePowersOnThenSelectsTheInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := idleListening(t)

		held.setFlags(true, false)

		h.equipment.waitForCommands(t, denon.PowerOnCommand)
		h.equipment.waitForCommands(t, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerOn })
	})
}

// A Play that ends returns the Player to its idle screen. The receiver
// may be playing something else by then, so the session sends nothing.
func TestAFlipOutOfActiveSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := idleListening(t)
		held.setFlags(true, false)
		h.equipment.waitForCommands(t, "SIGAME")

		held.setFlags(false, false)

		h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "PWSTANDBY", "SIGAME")
	})
}

// A person can select another input between two Plays, so each Play
// selects the input again.
func TestASecondFlipToActiveSelectsTheInputAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := idleListening(t)
		held.setFlags(true, false)
		h.equipment.waitForCommands(t, "SIGAME")
		held.setFlags(false, false)
		handOnTheRemote(t, h.equipment, "SIDVD")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "DVD" })

		held.setFlags(true, false)

		h.equipment.waitForCommands(t, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })
	})
}

// A screen that wakes turns the receiver on and selects the input with
// no Play running.
func TestAFlipToAwakePowersOnThenSelectsTheInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := idleListening(t)

		held.setFlags(false, true)

		h.equipment.waitForCommands(t, denon.PowerOnCommand)
		h.equipment.waitForCommands(t, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerOn })
	})
}

// A person can select another input while the screen sleeps, so each
// waking selects the input again.
func TestASecondFlipToAwakeSelectsTheInputAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := idleListening(t)
		held.setFlags(false, true)
		h.equipment.waitForCommands(t, "SIGAME")
		held.setFlags(false, false)
		handOnTheRemote(t, h.equipment, "SIDVD")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "DVD" })

		held.setFlags(false, true)

		h.equipment.waitForCommands(t, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })
	})
}

// A screen that wakes during a Play selects the input again. When a
// person pressed the receiver's own power button during the Play, this
// restores the Player's picture.
func TestAFlipToAwakeDuringAPlaySelectsTheInputAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, held := idleListening(t)
		held.setFlags(true, false)
		h.equipment.waitForCommands(t, "SIGAME")
		handOnTheRemote(t, h.equipment, "SIDVD")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "DVD" })

		held.setFlags(true, true)

		h.equipment.waitForCommands(t, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })
	})
}

// playingOnAWokenScreen starts a session with both flags on, as when
// media-operator writes a Play and a waking screen in one write. The
// receiver is already on, so the session sends only the input.
func playingOnAWokenScreen(t *testing.T, input string) *sessionHarness {
	t.Helper()
	h := newSessionHarness(t)
	h.powerOn(t)
	h.beginSession(t, input, true, true)
	return h
}

func TestASessionThatStartsActiveAndAwakeSelectsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := playingOnAWokenScreen(t, "GAME")

		h.equipment.waitForCommands(t, "SIGAME")

		h.refuseCommands(t, quietPeriod, "SIGAME")
	})
}

// When the receiver never reports power On, the session sends the input
// after sessionPowerWait, and equipment_commands_total counts a timeout.
// The test sets the client's Dial and Reporter before Run starts,
// because the client reads those fields without a lock.
func TestAPowerOnThatNeverAnswersCountsATimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		receiver := startFakeDenon(t)
		receiver.ignorePowerOn()
		readings := testMetrics(t)
		log := &logBuffer{}
		lines := newReceiverLog(log, "theater")
		h := &sessionHarness{
			equipment: receiver,
			holder:    &sessionHolder{lines: lines},
			readings:  readings,
			log:       log,
			lines:     lines,
		}
		h.denon = denon.NewClient(receiver.address(), h.holder.observe)
		h.denon.Dial = testNetwork.dial
		h.denon.Reporter = readings.reportCommand
		go h.denon.Run(t.Context())
		h.waitUntil(t, func(state equipment.State) bool {
			return mainZone(state).Power != "" && mainZone(state).VolumeMax != equipment.Unknown
		})
		h.equipment.waitForCommands(t, denon.Queries[len(denon.Queries)-1])

		h.begin(t, "GAME")
		time.Sleep(sessionPowerWait)

		h.equipment.waitForCommands(t, "SIGAME")
		requireSeries(t, scrape(t, readings), `equipment_commands_total{status="timeout"} 1`)
		mustDeepEqual(t, linesWith(log, "sent power On"), []string{
			"Receiver theater: a Play started on Player theater; sent power On; the receiver did not report power On in 10 s, so the input goes out anyway",
		})
	})
}

func TestASessionThatEndsDuringThePowerWaitSendsNoInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.equipment.ignorePowerOn()
		held := h.begin(t, "GAME")
		h.equipment.waitForCommands(t, denon.PowerOnCommand)

		held.stop()
		time.Sleep(sessionPowerWait)

		mustDeepEqual(t, linesWith(h.log, "sent power On"), []string{
			"Receiver theater: a Play started on Player theater; sent power On; the session ended before the receiver reported power On",
		})
		h.equipment.refuseCommand(t, "SIGAME", quietPeriod)
	})
}

func TestAnInputsSoundModeIsSelectedWithTheInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)
		h.soundModes = map[string]string{"GAME": "STEREO"}

		h.begin(t, "GAME")

		sent := h.equipment.waitForCommands(t, "MSSTEREO")
		if !slices.Contains(sent, "SIGAME") {
			t.Fatalf("the receiver got %v, want SIGAME to select the input before MSSTEREO", sent)
		}
	})
}

// The session compares itself with the state that the receiver
// reported, so it sends nothing until the driver reports FirstReadDone.
func TestAnActiveSessionOnAReceiverThatNeverAnsweredEndsWithNothingSent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var group sync.WaitGroup
		ctx, cancel := context.WithCancel(withWork(t.Context(), &group))
		log := &logBuffer{}
		spec := ReceiverSession{Player: "theater", Input: "GAME", Active: true}
		startSession(ctx, "theater", spec, denon.NewClient("127.0.0.1:1", nil), newReceiverLog(log, "theater"), nil, nil)

		cancel()

		mustMatch(t, awaitWork(&group, testTimeout), true)
		mustMatch(t, len(linesWith(log, "sent")), 0)
	})
}
