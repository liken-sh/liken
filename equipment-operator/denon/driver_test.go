package denon

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// A setter returns an error for a zone the receiver does not have, and
// for a sound mode on a zone that has none, before it sends anything.
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
				t.Fatalf("%s on zone9 returned nil, want an error because the receiver has no zone9", one.name)
			}
		})
	}
}

func TestTheZoneSettersReachTheirOwnWireLines(t *testing.T) {
	cases := []struct {
		name string
		send func(*Client) error
		want string
	}{
		{"zone2 power on", func(c *Client) error { return c.SetPower("zone2", true) }, "Z2ON"},
		{"zone2 power off", func(c *Client) error { return c.SetPower("zone2", false) }, "Z2OFF"},
		{"zone3 power on", func(c *Client) error { return c.SetPower("zone3", true) }, "Z3ON"},
		{"zone3 input", func(c *Client) error { return c.SetInput("zone3", "TV") }, "Z3TV"},
		{"zone2 volume", func(c *Client) error { return c.SetVolume("zone2", 101) }, "Z2MV505"},
		{"zone3 mute on", func(c *Client) error { return c.SetMute("zone3", true) }, "Z3MUON"},
		{"zone2 mute off", func(c *Client) error { return c.SetMute("zone2", false) }, "Z2MUOFF"},
		{"zone3 sleep", func(c *Client) error { return c.SetSleep("zone3", 30) }, "Z3SLP030"},
		{"zone2 sleep off", func(c *Client) error { return c.SetSleep("zone2", 0) }, "Z2SLPOFF"},
		{"main sleep", func(c *Client) error { return c.SetSleep(equipment.MainZone, 60) }, "SLP060"},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				harness := startHarness(t)
				drainQueries(t, harness)

				mustSucceed(t, one.send(harness.client))
				mustMatch(t, harness.receiver.waitForCommand(t), one.want)
			})
		})
	}
}

// The receiver's sleep timer takes 0 to 120 minutes.
func TestSetSleepRejectsAnOutOfRangeTimer(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	if err := client.SetSleep(equipment.MainZone, 121); err == nil {
		t.Fatal("SetSleep(121) returned nil, want an error because the timer takes 0 to 120 minutes")
	}
	if err := client.SetSleep("zone2", -1); err == nil {
		t.Fatal("SetSleep(-1) returned nil, want an error because the timer takes 0 to 120 minutes")
	}
}

func TestVolumeResolutionIsTwoStepsPerDisplayUnit(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	mustMatch(t, client.VolumeResolution(), 2)
}

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
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestSettingsCarriesTheParsedSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)
		waitForField(t, harness.events, equipment.EventSoundMode)

		// The fake receiver sends these lines after the sound mode.
		got := waitForSettings(t, harness.client, func(s Settings) bool {
			return s.System.VideoSelect != nil && s.Audio.DRC != nil && s.Audio.LFE != nil
		})
		mustMatch(t, got.System.Power, "Standby")
		mustMatch(t, *got.System.VideoSelect, "off")
		mustMatch(t, *got.Audio.DRC, "off")
		mustMatch(t, *got.Audio.LFE, 0)
	})
}

func TestTheSettersSendEveryCommand(t *testing.T) {
	cases := []struct {
		name string
		send func(*Client)
		want string
	}{
		{"power on", func(c *Client) { c.SetPower(equipment.MainZone, true) }, PowerOnCommand},
		{"power to standby", func(c *Client) { c.SetPower(equipment.MainZone, false) }, powerStandbyCommand},
		{"select input", func(c *Client) { c.SetInput(equipment.MainZone, "GAME") }, "SIGAME"},
		{"mute on", func(c *Client) { c.SetMute(equipment.MainZone, true) }, MuteOnCommand},
		{"mute off", func(c *Client) { c.SetMute(equipment.MainZone, false) }, MuteOffCommand},
		{"sound mode", func(c *Client) { c.SetSoundMode(equipment.MainZone, "STEREO") }, "MSSTEREO"},
		{"volume", func(c *Client) { c.SetVolume(equipment.MainZone, 101) }, "MV505"},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				harness := startHarness(t)
				drainQueries(t, harness)

				one.send(harness.client)
				mustMatch(t, harness.receiver.waitForCommand(t), one.want)
			})
		})
	}
}

func TestADroppedConnectionReportsUnreachable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)
		waitForField(t, harness.events, equipment.EventSoundMode)

		harness.receiver.dropConnections()

		mustMatch(t, waitForField(t, harness.events, equipment.EventReachable).Reachable, equipment.ConditionFalse)
	})
}

func TestACommandReportsItsOutcomeToTheWiredReporter(t *testing.T) {
	var statuses []string
	client := NewClient("192.0.2.1", nil)
	client.Reporter = func(status string) { statuses = append(statuses, status) }

	client.SetPower(equipment.MainZone, true)

	mustMatch(t, len(statuses), 1)
	mustMatch(t, statuses[0], CommandFailed)
}

func TestStateHasASecondZoneOnlyAfterTheReceiverSendsALineForIt(t *testing.T) {
	client := NewClient("192.0.2.1", nil)
	client.fold("Z2ON")
	client.fold("Z290")

	state := client.State()
	zone, held := state.Zone("zone2")
	mustMatch(t, held, true)
	mustMatch(t, zone.Power, equipment.PowerOn)
	mustMatch(t, zone.Volume, 180)

	if _, held := state.Zone("zone3"); held {
		t.Error("State has a zone3, want none because the receiver never reported one")
	}
}
