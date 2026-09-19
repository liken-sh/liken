// The driver's half of the equipment.Driver contract: the state it
// reports, the snapshot it hands the status, and the commands it sends.

package denon

import (
	"encoding/json"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

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

func TestProtocolStatusIsTheDriversOwnSnapshot(t *testing.T) {
	harness := startHarness(t)
	waitForField(t, harness.events, equipment.EventSoundMode)

	raw := harness.client.ProtocolStatus()
	if len(raw) == 0 {
		t.Fatal("the protocol snapshot is empty")
	}
	var snapshot struct {
		System struct {
			Power string `json:"power"`
		} `json:"system"`
		Tone struct {
			Bass int `json:"bass"`
		} `json:"tone"`
	}
	mustSucceed(t, json.Unmarshal(raw, &snapshot))
	mustMatch(t, snapshot.System.Power, "standby")
	mustMatch(t, snapshot.Tone.Bass, 0)
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
