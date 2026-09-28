package main

// The remote's power button in a room with a TV, against the fake API
// server, a fake Denon, and a fake WiiM: the TV's reported power
// decides whether the press turns the room on or off, and the press
// asks the TV for standby or a wake through its status.session. A
// receiver with no standby stays on and says so. A sleep of the session
// asks the TV for nothing. cecnode_standby_test.go tests what the node
// workload sends the TV.

import (
	"fmt"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// televisionRoom is the lounge Television, which shows Display
// acm-0001-receiver and reports a power, and the link a session of
// Player theater on input GAME uses to reach it.
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

// loungeSession waits until the lounge Television's session satisfies
// a check, and answers it.
func loungeSession(t *testing.T, api *cecAPI, ready func(TelevisionSession) bool) TelevisionSession {
	t.Helper()
	television := api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.Session != nil && ready(*television.Status.Session)
	})
	return *television.Status.Session
}

// pressPower starts an idle session in the harness's room and presses
// the remote's power button once.
func pressPower(t *testing.T, h *sessionHarness) {
	t.Helper()
	h.beginIdle(t, "GAME")
	broker := h.brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	broker.push(testPowerTopic, []byte(`{"action":"toggle"}`))
}

// A TV that is on means the room is on, whatever the receiver reports,
// so the press asks the TV for standby and puts the receiver in standby
// when it is on.
func TestAPowerPressTurnsARoomWithTheTVOnOff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		on       bool
		receiver string
	}{
		{"a receiver that is on", true, "sent power Standby; the receiver reported power Standby after <time>"},
		{"a receiver in standby", false, "sent the receiver nothing, because it reports power Standby"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSessionHarness(t)
			h.powerTopic = testPowerTopic
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
			press := "Receiver theater: the power topic asks toggle, and Television lounge reports power On; "
			mustDeepEqual(t, waitForLines(t, h.log, "asks toggle", 2), []string{
				press + "asked Television lounge to go to standby",
				press + c.receiver,
			})
		})
	}
}

// A TV in standby means the room is off, whatever the receiver reports,
// so the press wakes the TV and turns the receiver on when it is not.
func TestAPowerPressTurnsARoomWithTheTVInStandbyOn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		on    bool
		power []string
	}{
		{"a receiver that is on", true, nil},
		{"a receiver in standby", false, []string{denon.PowerOnCommand}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newSessionHarness(t)
			h.powerTopic = testPowerTopic
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
	}
}

// A TV that does not answer has no power to decide with, so the
// receiver decides, and the press still reaches the TV.
func TestAPowerPressWithATVThatDoesNotAnswerFollowsTheReceiver(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	h.powerTopic = testPowerTopic
	h.powerOn(t)
	api, room := televisionRoom(t, h.lines, "")
	h.room = room

	pressPower(t, h)

	loungeSession(t, api, func(session TelevisionSession) bool { return session.StandbyAt != "" })
	mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
	mustMatch(t, waitForLines(t, h.log, "asks toggle", 1)[0],
		"Receiver theater: the power topic asks toggle, and the receiver reports power On; asked Television lounge to go to standby")
}

// A session that sleeps asks the TV for nothing: the TV of a living
// room shows other inputs while the room's player is idle.
func TestASessionThatSleepsAsksTheTVForNothing(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	api, room := televisionRoom(t, h.lines, "On")
	h.room = room
	started := h.beginSession(t, "GAME", true, true)
	loungeSession(t, api, func(session TelevisionSession) bool { return session.Awake })

	started.setFlags(false, false)

	session := loungeSession(t, api, func(session TelevisionSession) bool { return !session.Awake })
	mustMatch(t, session.StandbyAt, "")
}

// A WiiM has no standby. The press still turns the room off: the TV
// goes to standby, and the WiiM stays on, which its line states as the
// outcome and not as a failed command.
func TestAPowerPressInAWiimRoomTurnsTheTVOff(t *testing.T) {
	t.Parallel()
	amp := startFakeWiim(t)
	client, _ := waitingWiim(t, amp)
	brokers := startFakeBrokerServer(t)
	log := &logBuffer{}
	lines := newReceiverLog(log, "studio")
	api, room := televisionRoom(t, lines, "On")
	applied := make(chan equipment.Power, 4)
	spec := ReceiverSession{Player: "theater", Input: "GAME", VolumeTopic: testVolumeTopic, PowerTopic: testPowerTopic}
	startSession(t.Context(), "studio", spec, client, nil, lines, brokers.address(),
		func() ReceiverVolume { return ReceiverVolume{} }, nil, func(power equipment.Power) { applied <- power }, room)
	broker := brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))

	broker.push(testPowerTopic, []byte(`{"action":"toggle"}`))

	loungeSession(t, api, func(session TelevisionSession) bool { return session.StandbyAt != "" })
	press := "Receiver studio: the power topic asks toggle, and Television lounge reports power On; "
	mustDeepEqual(t, waitForLines(t, log, "asks toggle", 2), []string{
		press + "asked Television lounge to go to standby",
		press + "sent the receiver nothing, because it has no standby command, so it stays on",
	})
	time.Sleep(quietPeriod)
	mustMatch(t, len(applied), 0)
}

// unreachableDriver is a receiver the operator cannot reach. It
// records every power and input command, which a press must not send.
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

// A receiver the operator cannot reach gets nothing from a press, and
// the TV still decides the room and turns off or on. With no TV power
// to decide, the press is dropped.
func TestAPowerPressWithAnUnreachableReceiverStillTogglesTheTV(t *testing.T) {
	t.Parallel()
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			driver := &unreachableDriver{fixedDriver: fixedDriver{state: equipment.State{Reachable: equipment.ConditionFalse}}, sent: make(chan string, 4)}
			brokers := startFakeBrokerServer(t)
			log := &logBuffer{}
			lines := newReceiverLog(log, "studio")
			api, room := televisionRoom(t, lines, c.power)
			applied := make(chan equipment.Power, 4)
			spec := ReceiverSession{Player: "theater", Input: "GAME", VolumeTopic: testVolumeTopic, PowerTopic: testPowerTopic}
			startSession(t.Context(), "studio", spec, driver, nil, lines, brokers.address(),
				func() ReceiverVolume { return ReceiverVolume{} }, nil, func(power equipment.Power) { applied <- power }, room)
			broker := brokers.waitForSession(t)
			broker.waitForTopic(t, ownerTopic(testVolumeTopic))

			broker.push(testPowerTopic, []byte(`{"action":"toggle"}`))

			loungeSession(t, api, c.ready)
			press := "Receiver studio: the power topic asks toggle, and Television lounge reports power " + c.power + "; "
			mustDeepEqual(t, waitForLines(t, log, "asks toggle", 2), []string{
				press + c.line,
				press + "sent the receiver nothing, because the operator cannot reach it",
			})
			time.Sleep(quietPeriod)
			mustMatch(t, len(driver.sent), 0)
			mustMatch(t, len(applied), 0)
		})
	}
}

// With no TV power to decide the room, a press on an unreachable
// receiver is dropped: nothing reaches the TV or the receiver.
func TestAPowerPressWithAnUnreachableReceiverAndNoTVPowerIsDropped(t *testing.T) {
	t.Parallel()
	driver := &unreachableDriver{fixedDriver: fixedDriver{state: equipment.State{Reachable: equipment.ConditionFalse}}, sent: make(chan string, 4)}
	brokers := startFakeBrokerServer(t)
	log := &logBuffer{}
	lines := newReceiverLog(log, "studio")
	api, room := televisionRoom(t, lines, "")
	spec := ReceiverSession{Player: "theater", Input: "GAME", VolumeTopic: testVolumeTopic, PowerTopic: testPowerTopic}
	startSession(t.Context(), "studio", spec, driver, nil, lines, brokers.address(),
		func() ReceiverVolume { return ReceiverVolume{} }, nil, nil, room)
	broker := brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	written := api.sessionWriteCount()

	broker.push(testPowerTopic, []byte(`{"action":"toggle"}`))

	time.Sleep(quietPeriod)
	mustMatch(t, api.sessionWriteCount(), written)
	mustMatch(t, len(driver.sent), 0)
	mustDeepEqual(t, linesWith(log, "asks toggle"), []string(nil))
}
