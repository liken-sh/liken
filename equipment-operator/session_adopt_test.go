package main

// The session's power and input step against a restart and a live
// flip. An operator that starts finds each session's flags as the last
// operator left them, and a flag it merely finds is no change a person
// made, so it adopts the flags and sends nothing. A flip it sees
// happen runs the step after the survey, and sends only the power, the
// input, and the sound mode that the receiver reports at another value.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// refuseEveryCommand fails the test if the client sends any command in
// the window. The queries and the heartbeat end in ? and pass.
func (f *fakeDenon) refuseEveryCommand(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case command := <-f.commands:
			if !strings.HasSuffix(command, "?") {
				t.Fatalf("the client sent %q", command)
			}
		case <-deadline:
			return
		}
	}
}

// A restart finds a room that plays and is awake. The receiver is in
// standby, because a person turned it off with its own remote, and the
// operator leaves it there.
func TestARestartAdoptsTheSessionsFlags(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }
	log := &logBuffer{}
	operator.log = log
	t.Cleanup(operator.stopAll)
	receiver := playingReceiver(fake.address(), ReceiverVolume{Max: 69.5})
	receiver.Spec.Session.Awake = true
	api.setReceivers(receiver)

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	waitForSurvey(t, operator)

	fake.refuseEveryCommand(t, quietPeriod)
	mustDeepEqual(t, linesWith(log, "session for Player"), []string{
		"Receiver theater: a session for Player house/theater started: input GAME, volume topic liken/players/theater/volume, no power topic, active true, awake true; the operator found it when it started, so it sends nothing for these flags",
	})
}

// A flip that finds the receiver already on the session's input and
// sound mode sends nothing, and the line says what the receiver
// reports.
func TestAFlipToASettledReceiverSendsNothing(t *testing.T) {
	h := newSessionHarnessWith(t, ReceiverVolume{Max: 69.5, Step: 1})
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
}

// A flip that finds the receiver on the session's input in another
// sound mode sends the sound mode alone.
func TestAFlipSendsOnlyTheSoundModeThatDiffers(t *testing.T) {
	h := newSessionHarnessWith(t, ReceiverVolume{Max: 69.5, Step: 1})
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
}

// A Denon reports the mode it decodes, in other words than the command
// that selects its family. A flip that finds the receiver on the
// session's input, in a mode of the declared family, sends nothing.
func TestAFlipSendsNoSoundModeTheReceiverRunsInOtherWords(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		reported string
	}{
		{"a Dolby report", "dolby digital", "DOLBY AUDIO-DD"},
		{"a DTS report", "DTS SURROUND", "DTS HD MSTR"},
		{"a report with spaces at the end", "STEREO", "STEREO  "},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			h := newSessionHarnessWith(t, ReceiverVolume{Max: 69.5, Step: 1})
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
	}
}
