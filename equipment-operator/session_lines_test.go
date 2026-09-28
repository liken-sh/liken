package main

// The session's line for each command it sends a receiver: the power
// and the input a flag or a toggle asks for, each press on the volume
// topic, and an ensure. The session's own echo and a knob turn add no
// line. The harness is in session_test.go.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

func TestAPlayThatStartsIsALineForThePowerAndOneForTheInput(t *testing.T) {
	h := newSessionHarness(t)
	h.soundModes = map[string]string{"GAME": "STEREO"}

	h.begin(t, "GAME")

	mustDeepEqual(t, waitForLines(t, h.log, "a Play started", 2), []string{
		"Receiver theater: a Play started on Player theater; sent power On; the receiver reported power On after <time>",
		"Receiver theater: a Play started on Player theater; sent input GAME and sound mode STEREO; the receiver reported input GAME after <time>",
	})
}

// Each press is a line with what it asked, what the session sent, and
// what the receiver reported. The position the session puts back on
// the topic and a knob turn add no line.
func TestEachPressIsOneLine(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 1}, 72)

	first := pressFrom(t, h, broker, 72, 5, "MV51")
	pressFrom(t, h, broker, first.Level, 5, "MV52")
	handOnTheRemote(t, h.equipment, "MV45")
	h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Volume == 90 })
	time.Sleep(quietPeriod)

	mustDeepEqual(t, waitForLines(t, h.log, "volume topic", 2), []string{
		"Receiver theater: the volume topic went from level 72, mute off to level 77, mute off; sent volume 51; the receiver reported volume 51 after <time>",
		"Receiver theater: the volume topic went from level 73, mute off to level 78, mute off; sent volume 52; the receiver reported volume 52 after <time>",
	})
}

// A press up on a receiver at its ceiling moves nothing. The topic
// then reads the top of the scale, so the press reaches the session
// only when the topic lags the receiver, and the test hands it over.
func TestAPressThatMovesNothingSaysWhy(t *testing.T) {
	h, _, held := listeningWith(t, ReceiverVolume{Max: 50}, 100)

	held.press(volumeState{Level: 99}, volumeState{Level: 100})

	mustDeepEqual(t, linesWith(h.log, "volume topic"), []string{
		"Receiver theater: the volume topic went from level 99, mute off to level 100, mute off; sent nothing, because the receiver reports volume 50 and mute off, and the ceiling is volume 50",
	})
}

func TestAToggleOnAReceiverThatIsOnIsOneLine(t *testing.T) {
	h := newSessionHarness(t)
	h.powerTopic = testPowerTopic
	h.powerOn(t)
	h.beginIdle(t, "GAME")
	broker := h.brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))

	broker.push(testPowerTopic, []byte(`{"action":"toggle"}`))

	mustDeepEqual(t, waitForLines(t, h.log, "toggle", 1), []string{
		"Receiver theater: the power topic asks toggle, and the receiver reports power On; sent power Standby; the receiver reported power Standby after <time>",
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
			"Receiver theater: the commands topic asks input.ensure; sent input GAME; the receiver reported input GAME after <time>",
		},
		{
			"in standby",
			func(t *testing.T) (*sessionHarness, *session) {
				h, _, held := idleListening(t)
				return h, held
			},
			"Receiver theater: the commands topic asks input.ensure; sent nothing, because the receiver reports power Standby",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			h, held := one.start(t)

			held.ensureInput()

			mustDeepEqual(t, waitForLines(t, h.log, "input.ensure", 1), []string{one.want})
		})
	}
}

func TestAnEnsureForASessionWithNoInputSaysSo(t *testing.T) {
	h := newSessionHarness(t)
	held := h.beginIdle(t, "")

	held.ensureInput()

	mustDeepEqual(t, linesWith(h.log, "input.ensure"), []string{
		"Receiver theater: the commands topic asks input.ensure; sent nothing, because Player theater's session names no input",
	})
	h.refuseCommands(t, quietPeriod, denon.PowerOnCommand)
}

// Each flag that runs the one-shots names itself in their lines.
func TestTheOneShotsNameTheFlagThatRanThem(t *testing.T) {
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
}
