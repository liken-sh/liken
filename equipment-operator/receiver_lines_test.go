package main

// The Deployment's line for each command it sends a receiver: a spec
// change it acts on, a message on the receiver's own topics, and a
// session that starts, changes, or ends. A pass that sends nothing
// adds no line.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// loggedController is a controller with its log in a buffer, on a
// broker at brokerAddress.
func loggedController(t *testing.T, api *fakeAPI, brokerAddress string) (*controller, *logBuffer) {
	t.Helper()
	operator := newController(api.client, brokerAddress, testMetrics(t))
	operator.dial = testNetwork.dial
	operator.now = func() time.Time { return statusNow }
	log := &logBuffer{}
	operator.log = log
	return operator, log
}

// reachedController runs the first passes of a controller for one
// receiver: the pass that connects, and the pass once the receiver has
// answered its survey.
func reachedController(t *testing.T, receiver Receiver, brokerAddress string) (*fakeAPI, *controller, *logBuffer) {
	t.Helper()
	api := startFakeAPI(t)
	api.setReceivers(receiver)
	operator, log := loggedController(t, api, brokerAddress)
	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	waitForSurvey(t, operator)
	return api, operator, log
}

// Each declared change is one line when the operator sends it, and a
// later pass with the same spec adds none.
func TestADeclaredChangeIsOneLine(t *testing.T) {
	drc, lfe, volume, last := "low", 0, 40.0, "last"
	cases := []struct {
		name    string
		declare func(*Receiver)
		// prepare has the receiver report the values the spec differs
		// from, and report each declared value once it is sent.
		prepare func(*testing.T, *fakeDenon, *controller)
		command string
		want    string
	}{
		{
			"settings",
			func(receiver *Receiver) {
				receiver.Spec.Denon.Settings = denon.Settings{Audio: denon.AudioSettings{DRC: &drc, LFE: &lfe}}
			},
			func(t *testing.T, fake *fakeDenon, operator *controller) {
				fake.holdSetting("PSDRC LOW", "PSDRC LOW")
				waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool {
					return s.Audio.DRC != nil && s.Audio.LFE != nil
				})
			},
			"PSDRC LOW",
			`Receiver theater: generation 4 declares spec.denon.settings {"audio":{"drc":"low"}}; sent it; the receiver reported no value that differs after <time>`,
		},
		{
			"hdmi settings",
			func(receiver *Receiver) {
				receiver.Spec.Denon.Settings = denon.Settings{HDMI: denon.HDMISettings{PassThroughSource: &last}}
			},
			func(t *testing.T, fake *fakeDenon, operator *controller) {
				fake.holdSetting("SSHOSCONSTS LAS", "SSHOSCONSTS LAS")
				fake.volunteer("SSHOSCONSTS HD2")
				waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool {
					return s.HDMI.PassThroughSource != nil
				})
			},
			"SSHOSCONSTS LAS",
			`Receiver theater: generation 4 declares spec.denon.settings {"hdmi":{"passThroughSource":"last"}}; sent it; the receiver reported no value that differs after <time>`,
		},
		{
			"zone",
			func(receiver *Receiver) {
				receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Input: "CD", Volume: &volume}}
			},
			func(t *testing.T, fake *fakeDenon, operator *controller) {
				fake.holdSetting("Z2CD", "Z2CD")
				fake.holdSetting("Z2MV40", "Z240")
				fake.volunteer("Z2PHONO", "Z220")
				waitForObservedZone(t, operator, "theater", "zone2", 40)
			},
			"Z2MV40",
			`Receiver theater: generation 4 declares zone zone2 {"input":"CD","volume":40}; sent it; the receiver reported no value that differs after <time>`,
		},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := startFakeDenon(t)
				receiver := testReceiver("theater", fake.address())
				one.declare(&receiver)
				_, operator, log := reachedController(t, receiver, "127.0.0.1:1")
				one.prepare(t, fake, operator)

				mustSucceed(t, operator.pass(t.Context()))
				fake.waitForCommands(t, one.command)
				got := waitForLines(t, log, "generation 4", 1)
				mustSucceed(t, operator.pass(t.Context()))
				mustSucceed(t, operator.pass(t.Context()))
				time.Sleep(quietPeriod)

				mustDeepEqual(t, got, []string{one.want})
				mustMatch(t, len(linesWith(log, "generation 4")), 1)
			})
		})
	}
}

// A restart against a receiver that already holds every declared value
// sends nothing, so it writes no line.
func TestARestartAgainstASettledReceiverWritesNoLine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		drc := "off"
		receiver.Spec.Denon.Settings = denon.Settings{Audio: denon.AudioSettings{DRC: &drc}}
		_, operator, log := reachedController(t, receiver, "127.0.0.1:1")
		waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool { return s.Audio.DRC != nil })

		mustSucceed(t, operator.pass(t.Context()))
		mustSucceed(t, operator.pass(t.Context()))
		time.Sleep(quietPeriod)

		mustMatch(t, len(linesWith(log, "generation 4")), 0)
	})
}

// A command the driver refuses states the driver's own error.
func TestARefusedSpecPowerStatesTheDriversError(t *testing.T) {
	t.Parallel()
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)
	log := &logBuffer{}
	unit.log = newReceiverLog(log, "studio")
	unit.generation.Store(3)

	unit.setPower(equipment.PowerStandby)

	mustDeepEqual(t, log.lines(), []string{
		"Receiver studio: generation 3 asks power Standby; sent power Standby; the command failed: a WiiM has no standby command; power off is not supported",
	})
}

// A message on the receiver's settings or commands topic is a person's
// command, and each one is a line.
func TestAMessageOnTheReceiversTopicsIsOneLine(t *testing.T) {
	cases := []struct {
		name    string
		topic   string
		payload string
		want    string
	}{
		{
			"setting",
			"liken/equipment/theater/settings",
			`{"setting":"tone.bass","value":3}`,
			"Receiver theater: the settings topic asks tone.bass 3; sent it; the receiver reported no value that differs after <time>",
		},
		{
			"refused setting",
			"liken/equipment/theater/settings",
			`{"setting":"tone.pitch","value":3}`,
			`Receiver theater: the settings topic asks tone.pitch 3; sent it; the command failed: unknown setting "tone.pitch"`,
		},
		{
			"refused command",
			"liken/equipment/theater/commands",
			`{"command":"quick.3","args":{"slot":1}}`,
			`Receiver theater: the commands topic asks quick.3 {"slot":1}; sent it; the command failed: no actions yet: quick.3`,
		},
		{
			"ensure with no session",
			"liken/equipment/theater/commands",
			`{"command":"input.ensure"}`,
			"Receiver theater: the commands topic asks input.ensure; sent nothing, because no session stands",
		},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := startFakeDenon(t)
				brokers := startFakeBrokerServer(t)
				receiver := testReceiver("theater", fake.address())
				receiver.Spec.SettingsTopic = "liken/equipment/theater/settings"
				receiver.Spec.CommandsTopic = "liken/equipment/theater/commands"
				_, _, log := reachedController(t, receiver, brokers.address())
				broker := brokers.waitForSession(t)
				waitForString(t, broker.subs)
				waitForString(t, broker.subs)

				broker.push(one.topic, []byte(one.payload))

				mustDeepEqual(t, waitForLines(t, log, "topic asks", 1), []string{one.want})
			})
		})
	}
}

// A WiiM answers each command over HTTP, and that answer is the report
// the line states.
func TestAWiimSettingsMessageStatesTheDevicesAnswer(t *testing.T) {
	t.Parallel()
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)
	log := &logBuffer{}
	unit.log = newReceiverLog(log, "studio")
	unit.client = startFakeAPI(t).client

	unit.handleSettings([]byte(`{"setting":"device.led","value":false}`))

	mustDeepEqual(t, timeless(log.lines()), []string{
		"Receiver studio: the settings topic asks device.led false; sent it; the receiver answered OK after <time>",
	})
}

// A session's start, each change of its flags, and its end are one
// line each, and a pass that changes nothing adds none.
func TestASessionsStartFlagsAndEndAreOneLineEach(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		brokers := startFakeBrokerServer(t)
		idle := idleReceiver(fake.address(), ReceiverVolume{Max: 69.5})
		api, operator, log := reachedController(t, idle, brokers.address())
		brokers.waitForSession(t).waitForTopic(t, ownerTopic(testVolumeTopic))

		api.setReceivers(playingReceiver(fake.address(), ReceiverVolume{Max: 69.5}))
		mustSucceed(t, operator.pass(t.Context()))
		mustSucceed(t, operator.pass(t.Context()))
		api.setReceivers(testReceiver("theater", fake.address()))
		mustSucceed(t, operator.pass(t.Context()))

		mustDeepEqual(t, linesWith(log, "session for Player"), []string{
			"Receiver theater: a session for Player house/theater started: input GAME, volume topic liken/players/theater/volume, no power topic, active false, awake false; the operator found it when it started, so it sends nothing for these flags",
			"Receiver theater: the session for Player house/theater: active went from false to true",
			"Receiver theater: the session for Player house/theater ended",
		})
		mustDeepEqual(t, linesWith(log, "owner mark"), []string{
			`Receiver theater: published the owner mark {"owner":"receiver/theater"} on liken/players/theater/volume/owner`,
			"Receiver theater: cleared the owner mark on liken/players/theater/volume/owner",
		})
	})
}

// A WiiM has no standby, so it always reports On, and a spec.power of
// On sends nothing. The line says why.
func TestASpecPowerOnForAWiimSaysNothingWasSent(t *testing.T) {
	t.Parallel()
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)
	log := &logBuffer{}
	unit.log = newReceiverLog(log, "studio")
	unit.client = startFakeAPI(t).client
	unit.generation.Store(3)

	unit.setPower(equipment.PowerOn)

	mustDeepEqual(t, log.lines(), []string{
		"Receiver studio: generation 3 asks power On; sent nothing, because the receiver reports power On",
	})
	mustDeepEqual(t, amp.sent(), []string(nil))
}
