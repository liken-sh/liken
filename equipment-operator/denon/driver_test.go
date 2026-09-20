// The driver's half of the equipment.Driver contract: the state it
// reports, the snapshot it hands the status, and the commands it sends.

package denon

import (
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

// The setters reject a zone the receiver does not carry, and sound mode
// on a zone that carries none, so a caller learns before anything
// reaches the wire.
func TestSettersRejectAnUnknownZone(t *testing.T) {
	client := NewClient("192.0.2.1", nil)
	cases := []struct {
		name string
		send func() error
	}{
		{"power", func() error { return client.SetPower("zone9", true) }},
		{"input", func() error { return client.SetInput("zone9", "TV") }},
		{"volume", func() error { return client.SetVolume("zone9", 50) }},
		{"mute", func() error { return client.SetMute("zone9", true) }},
		{"sleep", func() error { return client.SetSleep("zone9", 30) }},
		{"sound mode on a zone", func() error { return client.SetSoundMode("zone2", "STEREO") }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if one.send() == nil {
				t.Fatalf("%s on an unknown zone did not error", one.name)
			}
		})
	}
}

// The zone-aware setters reach their own wire lines, so a second and
// third zone each carry its Z2 or Z3 prefix and never the main zone's
// command.
func TestTheZoneSettersReachTheirOwnWireLines(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	cases := []struct {
		name string
		send func() error
		want string
	}{
		{"zone2 power on", func() error { return harness.client.SetPower("zone2", true) }, "Z2ON"},
		{"zone2 power off", func() error { return harness.client.SetPower("zone2", false) }, "Z2OFF"},
		{"zone3 power on", func() error { return harness.client.SetPower("zone3", true) }, "Z3ON"},
		{"zone3 input", func() error { return harness.client.SetInput("zone3", "TV") }, "Z3TV"},
		{"zone2 volume", func() error { return harness.client.SetVolume("zone2", 101) }, "Z2MV505"},
		{"zone3 mute on", func() error { return harness.client.SetMute("zone3", true) }, "Z3MUON"},
		{"zone2 mute off", func() error { return harness.client.SetMute("zone2", false) }, "Z2MUOFF"},
		{"zone3 sleep", func() error { return harness.client.SetSleep("zone3", 30) }, "Z3SLP030"},
		{"zone2 sleep off", func() error { return harness.client.SetSleep("zone2", 0) }, "Z2SLPOFF"},
		{"main sleep", func() error { return harness.client.SetSleep(equipment.MainZone, 60) }, "SLP060"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustSucceed(t, one.send())
			mustMatch(t, harness.receiver.waitForCommand(t), one.want)
		})
	}
}

// SetSleep bounds the timer to the receiver's own 0 to 120 minute
// range.
func TestSetSleepRejectsAnOutOfRangeTimer(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	if err := client.SetSleep(equipment.MainZone, 121); err == nil {
		t.Fatal("a timer above the receiver's range did not error")
	}
	if err := client.SetSleep("zone2", -1); err == nil {
		t.Fatal("a negative timer did not error")
	}
}

func TestVolumeResolutionIsTwoStepsPerDisplayUnit(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	mustMatch(t, client.VolumeResolution(), 2)
}

// A disconnected client reports the state before the receiver answered:
// unreachable, one main zone, and no volume yet.
func TestStateBeforeTheReceiverAnswers(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	state := client.State()
	mustMatch(t, state.Reachable, equipment.ConditionUnknown)
	zone, held := state.Zone(equipment.MainZone)
	mustMatch(t, held, true)
	mustMatch(t, zone.Volume, equipment.Unknown)
	mustMatch(t, zone.VolumeMax, equipment.Unknown)
}

func TestStateCarriesTheMainZoneInHalfSteps(t *testing.T) {
	harness := startHarness(t)
	waitForField(t, harness.events, equipment.EventSoundMode)

	state := harness.client.State()
	mustMatch(t, state.Reachable, equipment.ConditionTrue)
	zone, _ := state.Zone(equipment.MainZone)
	mustMatch(t, zone.Power, equipment.PowerStandby)
	mustMatch(t, zone.Input, "MPLAY")
	mustMatch(t, zone.SoundMode, "MULTI CH IN")
	mustMatch(t, zone.Volume, 100)
	mustMatch(t, zone.VolumeMax, 139)
}

func TestSettingsCarriesTheParsedSnapshot(t *testing.T) {
	harness := startHarness(t)
	waitForField(t, harness.events, equipment.EventSoundMode)

	// The noise lines arrive behind the sound mode, so poll until the
	// folded snapshot holds them.
	got := waitForSettings(t, harness.client, func(s Settings) bool {
		return s.System.VideoSelect != nil && s.Audio.DRC != nil && s.Audio.LFE != nil
	})
	mustMatch(t, got.System.Power, "standby")
	mustMatch(t, *got.System.VideoSelect, "off")
	mustMatch(t, *got.Audio.DRC, "off")
	mustMatch(t, *got.Audio.LFE, 0)
}

// Every setter reaches the wire. The zone argument is main for now, and
// the receiver's own command is what the fake reads back.
func TestTheSettersSendEveryCommand(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	cases := []struct {
		name string
		send func()
		want string
	}{
		{"power on", func() { harness.client.SetPower(equipment.MainZone, true) }, PowerOnCommand},
		{"power to standby", func() { harness.client.SetPower(equipment.MainZone, false) }, powerStandbyCommand},
		{"select input", func() { harness.client.SetInput(equipment.MainZone, "GAME") }, "SIGAME"},
		{"mute on", func() { harness.client.SetMute(equipment.MainZone, true) }, MuteOnCommand},
		{"mute off", func() { harness.client.SetMute(equipment.MainZone, false) }, MuteOffCommand},
		{"sound mode", func() { harness.client.SetSoundMode(equipment.MainZone, "STEREO") }, "MSSTEREO"},
		{"volume", func() { harness.client.SetVolume(equipment.MainZone, 101) }, "MV505"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			one.send()
			mustMatch(t, harness.receiver.waitForCommand(t), one.want)
		})
	}
}

// A dropped connection is reported to the listener as a reachability
// change, which is how the Receiver status learns the receiver is gone.
func TestADroppedConnectionReportsUnreachable(t *testing.T) {
	harness := startHarness(t)
	waitForField(t, harness.events, equipment.EventSoundMode)

	harness.receiver.dropConnections()

	mustMatch(t, waitForField(t, harness.events, equipment.EventReachable).Reachable, equipment.ConditionFalse)
}

func TestACommandReportsItsOutcomeToTheWiredReporter(t *testing.T) {
	var statuses []string
	client := NewClient("192.0.2.1", nil)
	client.Reporter = func(status string) { statuses = append(statuses, status) }

	// A disconnected client drops the command and reports the failure.
	client.SetPower(equipment.MainZone, true)

	mustMatch(t, len(statuses), 1)
	mustMatch(t, statuses[0], CommandFailed)
}

// A second zone appears in the equipment state only once the receiver
// has named it.
func TestStateCarriesASecondZoneOnceTheReceiverNamesIt(t *testing.T) {
	client := NewClient("192.0.2.1", nil)
	client.fold("Z2ON")
	client.fold("Z290")

	state := client.State()
	zone, held := state.Zone("zone2")
	mustMatch(t, held, true)
	mustMatch(t, zone.Power, equipment.PowerOn)
	mustMatch(t, zone.Volume, 180)

	if _, held := state.Zone("zone3"); held {
		t.Error("a third zone the receiver never named is held")
	}
}
