package main

// The two session flags against the fake receiver:
// the session the media operator holds at the idle screen, and the
// flips a Play and a waking screen make on it. The harness these run on
// is in session_test.go.

import (
	"context"
	"sync"
	"time"

	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// idleListening is a harness whose session stands with both flags
// false. It has sent the equipment nothing.
func idleListening(t *testing.T) (*sessionHarness, *session) {
	t.Helper()
	h := newSessionHarness(t)
	return h, h.beginIdle(t, "GAME")
}

// A Player at its idle screen holds a session on the receiver, so an
// idle session must leave a dark room dark.
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

// A Play that ends returns the Player to its idle screen. The room may
// still be listening to something else, so nothing is sent.
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

// A person can select another input between two Plays, so the next Play
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

// The screen waking is the second trigger for the same one-shots, so it
// powers a dark room on and selects the input by itself, with no Play
// standing.
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

// A person can select another input while the screen sleeps, so the
// next waking selects the input again.
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

// The flags are independent, so a screen that wakes under a standing
// Play selects the input again. A person pressed the receiver's own
// power button in the middle of a film, and the re-select is what puts
// the film back on the screen.
func TestAFlipToAwakeSelectsAgainUnderAStandingPlay(t *testing.T) {
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

// playingOnAWokenScreen is a harness whose session starts with both
// flags on, which is the media operator applying a Play and a waking
// screen in one write. The receiver is already on, so the input
// selection is the whole of what the session sends, and the count of
// them is what the test reads.
func playingOnAWokenScreen(t *testing.T, input string) *sessionHarness {
	t.Helper()
	h := newSessionHarness(t)
	h.powerOn(t)
	h.beginSession(t, input, true, true)
	return h
}

// Two flags on at the start are one start and not two, so the input is
// selected once.
func TestASessionThatStartsActiveAndAwakeSelectsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := playingOnAWokenScreen(t, "GAME")

		h.equipment.waitForCommands(t, "SIGAME")

		h.refuseCommands(t, quietPeriod, "SIGAME")
	})
}

// A power-on the receiver never confirms is the one wait selectInput
// gives up on, and giving up is what equipment_commands_total counts
// as a timeout. readings is wired before the client's Run goroutine
// starts, and never touched again from this goroutine, because the
// field carries no lock of its own.
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

// A session that ends while it waits for the receiver to report power
// On sends no input, and its line says the session ended.
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

// An input may name the sound mode it wants, and the session selects it
// in the same one-shot that selects the input.
func TestAnInputsSoundModeIsSelectedWithTheInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)
		h.soundModes = map[string]string{"GAME": "STEREO"}

		h.begin(t, "GAME")

		sent := h.equipment.waitForCommands(t, "MSSTEREO")
		if !slices.Contains(sent, "SIGAME") {
			t.Fatalf("the input was not selected before the mode: %v", sent)
		}
	})
}

// The power-and-input step compares the room with what the receiver
// reported, so it waits for the survey. A session that starts active on
// a receiver that never answers ends with nothing sent and no line
// written.
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
