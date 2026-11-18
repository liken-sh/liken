package main

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

var (
	statusNow     = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	statusEarlier = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
)

// testState returns a reachable state with only a main zone.
func testState(power equipment.Power, volume, volumeMax int) equipment.State {
	return equipment.State{
		Reachable: ConditionTrue,
		Zones: map[string]equipment.ZoneState{
			equipment.MainZone: {
				Power:     power,
				Input:     "MPLAY",
				SoundMode: "MULTI CH IN",
				Volume:    volume,
				VolumeMax: volumeMax,
			},
		},
	}
}

func TestBuildReceiverStatusFormatsPowerAndVolumeInTheReceiversUnits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		state     equipment.State
		power     string
		volume    string
		volumeMax string
	}{
		{"whole steps", testState(equipment.PowerOn, 100, 139), "On", "50", "69.5"},
		{"half steps", testState(equipment.PowerOn, 131, 139), "On", "65.5", "69.5"},
		{"zero", testState(equipment.PowerStandby, 0, 139), "Standby", "0", "69.5"},
		{"unknown volume", testState(equipment.PowerStandby, equipment.Unknown, equipment.Unknown), "Standby", "", ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := buildReceiverStatus(one.state, nil, nil, "192.0.2.7", 2, 3, nil, statusNow)
			main := status.Zones[equipment.MainZone]

			mustMatch(t, status.Address, "192.0.2.7")

			mustMatch(t, main.Power, one.power)
			mustMatch(t, main.Input, "MPLAY")
			mustMatch(t, main.Volume, one.volume)
			mustMatch(t, main.VolumeMax, one.volumeMax)
			mustMatch(t, main.SoundMode, "MULTI CH IN")
			mustMatch(t, len(status.Conditions), 1)
		})
	}
}

func TestBuildReceiverStatusIncludesTheProtocolSnapshot(t *testing.T) {
	t.Parallel()
	state := testState(equipment.PowerOn, 100, 139)
	eco := "auto"
	settings := &denon.Settings{System: denon.SystemSettings{Eco: &eco}}

	status := buildReceiverStatus(state, settings, nil, "", 2, 1, nil, statusNow)

	mustMatch(t, *status.Denon.System.Eco, "auto")
	mustMatch(t, status.Driver, "denon")
}

func TestBuildReceiverStatusIncludesTheModelAndManufacturer(t *testing.T) {
	t.Parallel()
	state := testState(equipment.PowerOn, 100, 139)
	state.Model, state.Manufacturer = "AVR-X1700H", "Denon"

	status := buildReceiverStatus(state, nil, nil, "", 2, 1, nil, statusNow)

	mustMatch(t, status.Model, "AVR-X1700H")
	mustMatch(t, status.Manufacturer, "Denon")
}

func TestBuildReceiverStatusIncludesTheMuteFlag(t *testing.T) {
	t.Parallel()
	state := testState(equipment.PowerOn, 100, 139)
	zone := state.Zones[equipment.MainZone]
	zone.Mute = true
	state.Zones[equipment.MainZone] = zone

	mustMatch(t, buildReceiverStatus(state, nil, nil, "", 2, 1, nil, statusNow).Zones[equipment.MainZone].Mute, true)
}

func TestReachableSetsTheReasonAndTheTransitionTimeForEachStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		status   ConditionStatus
		previous []Condition
		reason   string
		stamp    time.Time
	}{
		{"receiver responded", ConditionTrue, nil, reasonConnected, statusNow},
		{"receiver did not respond", ConditionFalse, nil, reasonUnreachable, statusNow},
		{"not reached yet", ConditionUnknown, nil, reasonConnecting, statusNow},
		{
			"status unchanged keeps the transition time",
			ConditionTrue,
			[]Condition{{Type: reachableConditionType, Status: ConditionTrue, LastTransitionTime: statusEarlier}},
			reasonConnected,
			statusEarlier,
		},
		{
			"status changed moves the transition time",
			ConditionTrue,
			[]Condition{{Type: reachableConditionType, Status: ConditionFalse, LastTransitionTime: statusEarlier}},
			reasonConnected,
			statusNow,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			condition := reachable(one.status, 7, one.previous, statusNow)

			mustMatch(t, condition.Type, reachableConditionType)
			mustMatch(t, condition.Status, one.status)
			mustMatch(t, condition.Reason, one.reason)
			mustMatch(t, condition.LastTransitionTime, one.stamp)
			mustMatch(t, condition.ObservedGeneration, int64(7))
		})
	}
}

func TestSameStatusIsTrueOnlyWhenAWriteWouldChangeNothing(t *testing.T) {
	t.Parallel()
	baseline := ReceiverStatus{
		Zones: map[string]ZoneStatus{
			equipment.MainZone: {Power: "On", Input: "MPLAY", Volume: "50", VolumeMax: "69.5", SoundMode: "MULTI CH IN"},
		},
		Conditions: []Condition{{Type: reachableConditionType, Status: ConditionTrue}},
	}
	movedVolume := baseline
	movedVolume.Zones = map[string]ZoneStatus{
		equipment.MainZone: {Power: "On", Input: "MPLAY", Volume: "51", VolumeMax: "69.5", SoundMode: "MULTI CH IN"},
	}
	addedZone := baseline
	addedZone.Zones = map[string]ZoneStatus{
		equipment.MainZone: {Power: "On", Input: "MPLAY", Volume: "50"},
		"zone2":            {Power: "On", Input: "PHONO", Volume: "90"},
	}
	movedProtocol := baseline
	bass := 3
	movedProtocol.Denon = &denon.Settings{Tone: denon.ToneSettings{Bass: &bass}}
	unreached := baseline
	unreached.Conditions = []Condition{{Type: reachableConditionType, Status: ConditionFalse}}
	noConditions := baseline
	noConditions.Conditions = nil
	settledBlock := baseline
	settledBlock.SettledSettings = map[string]string{zonesBlock: "0123456789abcdef"}
	settledPower := baseline
	settledPower.SettledPower = equipment.PowerOn
	modelRead := baseline
	modelRead.Model = "WiiM Amp"
	manufacturerRead := baseline
	manufacturerRead.Manufacturer = "Linkplay Technology Inc."

	cases := []struct {
		name string
		next ReceiverStatus
		same bool
	}{
		{"identical", baseline, true},
		{"volume moved", movedVolume, false},
		{"a zone appeared", addedZone, false},
		{"the protocol snapshot moved", movedProtocol, false},
		{"the condition status changed", unreached, false},
		{"condition dropped", noConditions, false},
		{"a block settled", settledBlock, false},
		{"a power settled", settledPower, false},
		{"the model was read", modelRead, false},
		{"the manufacturer was read", manufacturerRead, false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, sameStatus(baseline, one.next), one.same)
		})
	}
}
