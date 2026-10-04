package main

// The session's line for each command it sends a receiver: the power
// and the input a flag or a power ask asks for, and an ensure. The
// harness is in session_test.go.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/denon"
)

func TestAPlayThatStartsIsALineForThePowerAndOneForTheInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.soundModes = map[string]string{"GAME": "STEREO"}

		h.begin(t, "GAME")

		mustDeepEqual(t, waitForLines(t, h.log, "a Play started", 2), []string{
			"Receiver theater: a Play started on Player theater; sent power On; the receiver reported power On after <time>",
			"Receiver theater: a Play started on Player theater; sent input GAME and sound mode STEREO; the receiver reported input GAME after <time>",
		})
	})
}

func TestAToggleOnAReceiverThatIsOnIsOneLine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)
		held := h.beginIdle(t, "GAME")

		powerAsk(held, "toggle")

		mustDeepEqual(t, waitForLines(t, h.log, "toggle", 1), []string{
			"Receiver theater: status.session.powerAsk asks toggle, and the receiver reports power On; sent power Standby; the receiver reported power Standby after <time>",
		})
	})
}

func TestAnEnsureIsOneLine(t *testing.T) {
	cases := []struct {
		name  string
		start func(*testing.T) (*sessionHarness, *session)
		want  string
	}{
		{
			"after a drift",
			driftedWhileListening,
			"Receiver theater: status.session.inputAsk at 2026-10-04T12:15:25.001Z asks ensure; sent input GAME; the receiver reported input GAME after <time>",
		},
		{
			"in standby",
			func(t *testing.T) (*sessionHarness, *session) {
				return idleListening(t)
			},
			"Receiver theater: status.session.inputAsk at 2026-10-04T12:15:25.001Z asks ensure; sent nothing, because the receiver reports power Standby",
		},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, held := one.start(t)

				held.inputAsk(ensureAsk)

				mustDeepEqual(t, waitForLines(t, h.log, "asks ensure", 1), []string{one.want})
			})
		})
	}
}

func TestAnEnsureForASessionWithNoInputSaysSo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		held := h.beginIdle(t, "")

		held.inputAsk(ensureAsk)

		mustDeepEqual(t, linesWith(h.log, "asks ensure"), []string{
			"Receiver theater: status.session.inputAsk at 2026-10-04T12:15:25.001Z asks ensure; sent nothing, because Player theater's session names no input",
		})
		h.refuseCommands(t, quietPeriod, denon.PowerOnCommand)
	})
}

// Each flag that runs the one-shots names itself in their lines.
func TestTheOneShotsNameTheFlagThatRanThem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cases := []struct {
			played, woke bool
			want         string
		}{
			{true, false, "a Play started on Player den"},
			{false, true, "the screen of Player den woke"},
			{true, true, "a Play started on Player den and its screen woke"},
		}
		held := &session{spec: ReceiverSession{Player: "den"}}
		for _, one := range cases {
			mustMatch(t, held.flagWords(one.played, one.woke), one.want)
		}
	})
}
