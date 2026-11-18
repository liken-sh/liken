package main

// The Deployment writes one log line for each action it takes on a
// receiver, and none for a pass that sends nothing.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

func loggedController(t *testing.T, api *fakeAPI) (*controller, *logBuffer) {
	t.Helper()
	operator := newController(api.client, testMetrics(t))
	operator.dial = testNetwork.dial
	operator.listen = testNetwork.bind
	operator.now = func() time.Time { return statusNow }
	log := &logBuffer{}
	operator.log = log
	return operator, log
}

// reachedController runs a controller until it has connected to the
// receiver and the first full read has finished.
func reachedController(t *testing.T, receiver Receiver) (*fakeAPI, *controller, *logBuffer) {
	t.Helper()
	api := startFakeAPI(t)
	api.setReceivers(receiver)
	operator, log := loggedController(t, api)
	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	waitForFirstRead(t, operator)
	return api, operator, log
}

func TestADeclaredChangeIsOneLine(t *testing.T) {
	drc, lfe, volume, last := "low", 0, 40.0, "last"
	cases := []struct {
		name    string
		declare func(*Receiver)
		// prepare makes the receiver report values that differ from the
		// spec, and report each declared value after it is sent.
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
				_, operator, log := reachedController(t, receiver)
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

func TestARestartAgainstASettledReceiverWritesNoLine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		drc := "off"
		receiver.Spec.Denon.Settings = denon.Settings{Audio: denon.AudioSettings{DRC: &drc}}
		_, operator, log := reachedController(t, receiver)
		waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool { return s.Audio.DRC != nil })

		mustSucceed(t, operator.pass(t.Context()))
		mustSucceed(t, operator.pass(t.Context()))
		time.Sleep(quietPeriod)

		mustMatch(t, len(linesWith(log, "generation 4")), 0)
	})
}

func TestARefusedSpecPowerLogsTheDriversError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeWiim(t)
		_, unit := waitingWiim(t, amp)
		log := &logBuffer{}
		unit.log = newReceiverLog(log, "studio")
		unit.generation.Store(3)

		unit.setPower(equipment.PowerStandby)

		mustDeepEqual(t, log.lines(), []string{
			"Receiver studio: generation 3 requests power Standby; sent power Standby; the command was not applied: a WiiM has no standby command, so it cannot power off",
		})
	})
}

func TestASessionsStartFlagsAndEndWriteOneLineEach(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		idle := idleReceiver(fake.address(), ReceiverVolume{Max: 69.5})
		api, operator, log := reachedController(t, idle)

		api.setReceivers(playingReceiver(fake.address(), ReceiverVolume{Max: 69.5}))
		mustSucceed(t, operator.pass(t.Context()))
		mustSucceed(t, operator.pass(t.Context()))
		api.setReceivers(testReceiver("theater", fake.address()))
		mustSucceed(t, operator.pass(t.Context()))

		mustDeepEqual(t, linesWith(log, "session for Player"), []string{
			"Receiver theater: a session for Player house/theater started: input GAME, active false, awake false; the operator found it when it started, so it sends nothing for these flags",
			"Receiver theater: the session for Player house/theater: active went from false to true",
			"Receiver theater: the session for Player house/theater ended",
		})
	})
}

// A WiiM has no standby, so it always reports power On.
func TestASpecPowerOnForAWiimLogsThatNothingWasSent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeWiim(t)
		_, unit := waitingWiim(t, amp)
		log := &logBuffer{}
		unit.log = newReceiverLog(log, "studio")
		unit.client = startFakeAPI(t).client
		unit.generation.Store(3)

		unit.setPower(equipment.PowerOn)

		mustDeepEqual(t, log.lines(), []string{
			"Receiver studio: generation 3 requests power On; sent nothing, because the receiver reports power On",
		})
		mustDeepEqual(t, amp.sent(), []string(nil))
	})
}
