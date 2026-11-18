package main

// A restarted operator finds each session's flags as the previous
// operator left them. A flag that is already set is not a new change by
// a person, so the operator adopts it and sends nothing. A flag that
// changes while the operator runs makes it send, after the first full read, only
// the power, input, and sound mode that differ from what the receiver
// reports.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// refuseEveryCommand fails the test if the client sends any command
// within the given time. Queries and the heartbeat end in ? and are
// allowed.
func (f *fakeDenon) refuseEveryCommand(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case command := <-f.commands:
			if !strings.HasSuffix(command, "?") {
				t.Fatalf("the client sent the command %q, want only queries", command)
			}
		case <-deadline:
			return
		}
	}
}

// A person turned the receiver off with its own remote while the
// session was playing and awake. After a restart, the operator leaves
// the receiver in standby.
func TestARestartAdoptsTheSessionsFlags(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.listen = testNetwork.bind
		operator.now = func() time.Time { return statusNow }
		log := &logBuffer{}
		operator.log = log
		t.Cleanup(operator.stopAll)
		receiver := playingReceiver(fake.address(), ReceiverVolume{Max: 69.5})
		receiver.Spec.Session.Awake = true
		api.setReceivers(receiver)

		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)

		fake.refuseEveryCommand(t, quietPeriod)
		mustDeepEqual(t, linesWith(log, "session for Player"), []string{
			"Receiver theater: a session for Player house/theater started: input GAME, active true, awake true; the operator found it when it started, so it sends nothing for these flags",
		})
	})
}

func TestAFlipToASettledReceiverSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)
		h.soundModes = map[string]string{"GAME": "MULTI CH IN"}
		handOnTheRemote(t, h.equipment, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })
		held := h.beginIdle(t, "GAME")

		held.setFlags(true, false)

		mustDeepEqual(t, waitForLines(t, h.log, "a Play started", 1), []string{
			"Receiver theater: a Play started on Player theater; sent nothing, because the receiver reports power On, input GAME, and sound mode MULTI CH IN",
		})
		h.equipment.refuseEveryCommand(t, quietPeriod)
	})
}

func TestAFlipSendsOnlyTheSoundModeThatDiffers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)
		h.soundModes = map[string]string{"GAME": "STEREO"}
		handOnTheRemote(t, h.equipment, "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "GAME" })
		held := h.beginIdle(t, "GAME")
		h.drainCommands()

		held.setFlags(true, false)

		mustMatch(t, h.equipment.waitForCommand(t), denon.SoundModeCommand("STEREO"))
		mustDeepEqual(t, waitForLines(t, h.log, "a Play started", 1), []string{
			"Receiver theater: a Play started on Player theater; sent sound mode STEREO; the receiver reported sound mode STEREO after <time>",
		})
		h.equipment.refuseEveryCommand(t, quietPeriod)
	})
}

// A Denon reports the mode that it is decoding in different words than
// the command that selects the mode's family, so a reported mode of the
// declared family must count as a match.
func TestAFlipSendsNoSoundModeTheReceiverRunsInOtherWords(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		reported string
	}{
		{"a Dolby report", "dolby digital", "DOLBY AUDIO-DD"},
		{"a DTS report", "DTS SURROUND", "DTS HD MSTR"},
		{"a report with trailing spaces", "STEREO", "STEREO  "},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newSessionHarness(t)
				h.powerOn(t)
				h.soundModes = map[string]string{"GAME": one.declared}
				handOnTheRemote(t, h.equipment, "SIGAME")
				h.equipment.volunteer("MS" + one.reported)
				h.waitUntil(t, func(state equipment.State) bool {
					zone := mainZone(state)
					return zone.Input == "GAME" && zone.SoundMode == strings.TrimSpace(one.reported)
				})
				held := h.beginIdle(t, "GAME")
				h.drainCommands()

				held.setFlags(true, false)

				mustDeepEqual(t, waitForLines(t, h.log, "a Play started", 1), []string{
					"Receiver theater: a Play started on Player theater; sent nothing, because the receiver reports power On, input GAME, and sound mode " + strings.TrimSpace(one.reported),
				})
				h.equipment.refuseEveryCommand(t, quietPeriod)
			})
		})
	}
}
