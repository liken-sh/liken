package main

// Tests of the remote's power button in a room with a TV. A press
// writes the Television's status.session, and cecnode_standby_test.go
// tests what the node workload then sends the TV.

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// televisionRoom returns a fake API server with the lounge Television,
// which shows Display acm-0001-receiver and reports power, and the
// roomEvents through which a session of Player theater on input GAME
// reaches that Television.
func televisionRoom(t *testing.T, lines *receiverLog, power string) (*cecAPI, roomEvents) {
	t.Helper()
	api := startCECAPI(t)
	api.showing(lounge(""), "acm-0001-receiver")
	api.mutex.Lock()
	api.televisions["lounge"].Status.Power = power
	api.mutex.Unlock()
	sessions := newTelevisionSessions(api.client)
	t.Cleanup(sessions.stop)
	sessions.markLive()
	monitors := map[string]string{"GAME": "acm-0001-receiver"}
	return api, sessions.room(lines, "theater", "GAME", func(input string) string { return monitors[input] })
}

func loungeSession(t *testing.T, api *cecAPI, ready func(TelevisionSession) bool) TelevisionSession {
	t.Helper()
	television := api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.Session != nil && ready(*television.Status.Session)
	})
	return *television.Status.Session
}

func pressPower(t *testing.T, h *sessionHarness) {
	t.Helper()
	powerAsk(h.beginIdle(t, "GAME"), "toggle")
}

// When the TV is on, a press turns the room off, whatever the receiver
// reports.
func TestAPowerPressTurnsARoomWithTheTVOnOff(t *testing.T) {
	cases := []struct {
		name     string
		on       bool
		receiver string
	}{
		{"a receiver that is on", true, "sent power Standby; the receiver reported power Standby after <time>"},
		{"a receiver in standby", false, "sent the receiver nothing, because it reports power Standby"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newSessionHarness(t)
				if c.on {
					h.powerOn(t)
				}
				api, room := televisionRoom(t, h.lines, "On")
				h.room = room

				pressPower(t, h)

				session := loungeSession(t, api, func(session TelevisionSession) bool { return session.StandbyAt != "" })
				mustMatch(t, session.Awake, false)
				if c.on {
					mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
				}
				h.refuseCommands(t, quietPeriod, "PWSTANDBY", denon.PowerOnCommand)
				press := "Receiver theater: status.session.powerAsk requests toggle, and Television lounge reports power On; "
				mustDeepEqual(t, waitForLines(t, h.log, "requests toggle", 2), []string{
					press + "asked Television lounge to go to standby",
					press + c.receiver,
				})
			})
		})
	}
}

// When the TV is in standby, a press turns the room on, whatever the
// receiver reports.
func TestAPowerPressTurnsARoomWithTheTVInStandbyOn(t *testing.T) {
	cases := []struct {
		name  string
		on    bool
		power []string
	}{
		{"a receiver that is on", true, nil},
		{"a receiver in standby", false, []string{denon.PowerOnCommand}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newSessionHarness(t)
				if c.on {
					h.powerOn(t)
				}
				api, room := televisionRoom(t, h.lines, "Standby")
				h.room = room

				pressPower(t, h)

				session := loungeSession(t, api, func(session TelevisionSession) bool { return session.WokeAt != "" })
				mustMatch(t, session.Awake, true)
				mustMatch(t, session.StandbyAt, "")
				var sent []string
				for command := h.equipment.waitForCommand(t); command != "SIGAME"; command = h.equipment.waitForCommand(t) {
					sent = append(sent, command)
				}
				mustDeepEqual(t, sent, c.power)
				h.refuseCommands(t, quietPeriod, "PWSTANDBY")
			})
		})
	}
}

// When the TV reports no power, the receiver's power determines what the
// press does.
func TestAPowerPressWithATVThatDoesNotAnswerFollowsTheReceiver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)
		api, room := televisionRoom(t, h.lines, "")
		h.room = room

		pressPower(t, h)

		loungeSession(t, api, func(session TelevisionSession) bool { return session.StandbyAt != "" })
		mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
		mustMatch(t, waitForLines(t, h.log, "requests toggle", 1)[0],
			"Receiver theater: status.session.powerAsk requests toggle, and the receiver reports power On; asked Television lounge to go to standby")
	})
}

// When a session stops being awake, the TV is not put in standby,
// because it can be showing another input while the player is idle.
func TestASessionThatSleepsAsksTheTVForNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		api, room := televisionRoom(t, h.lines, "On")
		h.room = room
		started := h.beginSession(t, "GAME", true, true)
		loungeSession(t, api, func(session TelevisionSession) bool { return session.Awake })

		started.setFlags(false, false)

		session := loungeSession(t, api, func(session TelevisionSession) bool { return !session.Awake })
		mustMatch(t, session.StandbyAt, "")
	})
}

// A WiiM has no standby command, so a press puts only the TV in
// standby, and the log reports that as the outcome, not as a failure.
func TestAPowerPressInAWiimRoomTurnsTheTVOff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeWiim(t)
		client, _ := waitingWiim(t, amp)
		log := &logBuffer{}
		lines := newReceiverLog(log, "studio")
		api, room := televisionRoom(t, lines, "On")
		applied := make(chan equipment.Power, 4)
		spec := ReceiverSession{Player: "theater", Input: "GAME"}
		held := startSession(t.Context(), "studio", spec, client, lines, func(power equipment.Power) { applied <- power }, room)

		powerAsk(held, "toggle")

		loungeSession(t, api, func(session TelevisionSession) bool { return session.StandbyAt != "" })
		press := "Receiver studio: status.session.powerAsk requests toggle, and Television lounge reports power On; "
		mustDeepEqual(t, waitForLines(t, log, "requests toggle", 2), []string{
			press + "asked Television lounge to go to standby",
			press + "sent the receiver nothing, because it has no standby command, so it stays on",
		})
		time.Sleep(quietPeriod)
		mustMatch(t, len(applied), 0)
	})
}

// fixedDriver reports one state and accepts every command.
type fixedDriver struct {
	state equipment.State
}

func (d *fixedDriver) Run(context.Context)               {}
func (d *fixedDriver) State() equipment.State            { return d.state }
func (d *fixedDriver) FirstReadDone() bool               { return true }
func (d *fixedDriver) Address() string                   { return "" }
func (d *fixedDriver) VolumeResolution() int             { return 1 }
func (d *fixedDriver) HasStandby() bool                  { return true }
func (d *fixedDriver) SetPower(string, bool) error       { return nil }
func (d *fixedDriver) SetInput(string, string) error     { return nil }
func (d *fixedDriver) SetVolume(string, int) error       { return nil }
func (d *fixedDriver) SetMute(string, bool) error        { return nil }
func (d *fixedDriver) SetSoundMode(string, string) error { return nil }
func (d *fixedDriver) SetSleep(string, int) error        { return nil }
func (d *fixedDriver) SameSoundMode(declared, reported string) bool {
	return declared == reported
}

// unreachableDriver reports a receiver that the operator cannot reach,
// and records every power and input command it is sent.
type unreachableDriver struct {
	fixedDriver
	sent chan string
}

func (d *unreachableDriver) SetPower(_ string, on bool) error {
	d.sent <- fmt.Sprintf("power %t", on)
	return nil
}

func (d *unreachableDriver) SetInput(_ string, input string) error {
	d.sent <- "input " + input
	return nil
}

// A press sends an unreachable receiver nothing, and the TV's power
// still determines whether the room turns on or off.
func TestAPowerPressWithAnUnreachableReceiverStillTogglesTheTV(t *testing.T) {
	cases := []struct {
		name  string
		power string
		ready func(TelevisionSession) bool
		line  string
	}{
		{"a TV that is on", "On", func(session TelevisionSession) bool { return session.StandbyAt != "" && !session.Awake },
			"asked Television lounge to go to standby"},
		{"a TV in standby", "Standby", func(session TelevisionSession) bool { return session.WokeAt != "" && session.Awake },
			"asked Television lounge to wake and show Display acm-0001-receiver"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				driver := &unreachableDriver{fixedDriver: fixedDriver{state: equipment.State{Reachable: equipment.ConditionFalse}}, sent: make(chan string, 4)}
				log := &logBuffer{}
				lines := newReceiverLog(log, "studio")
				api, room := televisionRoom(t, lines, c.power)
				applied := make(chan equipment.Power, 4)
				spec := ReceiverSession{Player: "theater", Input: "GAME"}
				held := startSession(t.Context(), "studio", spec, driver, lines, func(power equipment.Power) { applied <- power }, room)

				powerAsk(held, "toggle")

				loungeSession(t, api, c.ready)
				press := "Receiver studio: status.session.powerAsk requests toggle, and Television lounge reports power " + c.power + "; "
				mustDeepEqual(t, waitForLines(t, log, "requests toggle", 2), []string{
					press + c.line,
					press + "sent the receiver nothing, because the operator cannot reach it",
				})
				time.Sleep(quietPeriod)
				mustMatch(t, len(driver.sent), 0)
				mustMatch(t, len(applied), 0)
			})
		})
	}
}

// When the TV reports no power and the receiver is unreachable, no
// state determines what the press does, so the press is dropped.
func TestAPowerPressWithAnUnreachableReceiverAndNoTVPowerIsDropped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		driver := &unreachableDriver{fixedDriver: fixedDriver{state: equipment.State{Reachable: equipment.ConditionFalse}}, sent: make(chan string, 4)}
		log := &logBuffer{}
		lines := newReceiverLog(log, "studio")
		api, room := televisionRoom(t, lines, "")
		spec := ReceiverSession{Player: "theater", Input: "GAME"}
		held := startSession(t.Context(), "studio", spec, driver, lines, nil, room)
		written := api.sessionWriteCount()

		powerAsk(held, "toggle")

		time.Sleep(quietPeriod)
		mustMatch(t, api.sessionWriteCount(), written)
		mustMatch(t, len(driver.sent), 0)
		mustDeepEqual(t, linesWith(log, "requests toggle"), []string(nil))
	})
}
