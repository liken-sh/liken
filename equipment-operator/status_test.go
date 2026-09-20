package main

// The status the operator reports, built from what the receiver last
// said.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// The moment every status test stamps, and one from before it.
var (
	statusNow     = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	statusEarlier = "2026-09-01T00:00:00Z"
)

// testState is a state whose fields each case overrides.
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

func TestBuildReceiverStatusCarriesTheReceiversOwnUnits(t *testing.T) {
	cases := []struct {
		name      string
		state     equipment.State
		power     string
		volume    string
		volumeMax string
	}{
		{"whole steps", testState(equipment.PowerOn, 100, 139), "on", "50", "69.5"},
		{"half steps", testState(equipment.PowerOn, 131, 139), "on", "65.5", "69.5"},
		{"zero", testState(equipment.PowerStandby, 0, 139), "standby", "0", "69.5"},
		{"unknown volume", testState(equipment.PowerStandby, equipment.Unknown, equipment.Unknown), "standby", "", ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := buildReceiverStatus(one.state, nil, 2, 3, nil, statusNow)
			main := status.Zones[equipment.MainZone]

			mustMatch(t, main.Power, one.power)
			mustMatch(t, main.Input, "MPLAY")
			mustMatch(t, main.Volume, one.volume)
			mustMatch(t, main.VolumeMax, one.volumeMax)
			mustMatch(t, main.SoundMode, "MULTI CH IN")
			mustMatch(t, status.Service, "")
			mustMatch(t, len(status.Conditions), 1)
		})
	}
}

func TestBuildReceiverStatusCarriesTheProtocolSnapshot(t *testing.T) {
	state := testState(equipment.PowerOn, 100, 139)
	eco := "auto"
	settings := &denon.Settings{System: denon.SystemSettings{Eco: &eco}}

	status := buildReceiverStatus(state, settings, 2, 1, nil, statusNow)

	mustMatch(t, *status.Denon.System.Eco, "auto")
}

func TestBuildReceiverStatusCarriesTheMuteFlag(t *testing.T) {
	state := testState(equipment.PowerOn, 100, 139)
	zone := state.Zones[equipment.MainZone]
	zone.Mute = true
	state.Zones[equipment.MainZone] = zone

	mustMatch(t, buildReceiverStatus(state, nil, 2, 1, nil, statusNow).Zones[equipment.MainZone].Mute, true)
}

func TestReachableNamesEachVerdict(t *testing.T) {
	cases := []struct {
		name     string
		status   ConditionStatus
		previous []Condition
		reason   string
		stamp    string
	}{
		{"answered", ConditionTrue, nil, reasonConnected, timestamp(statusNow)},
		{"silent", ConditionFalse, nil, reasonUnreachable, timestamp(statusNow)},
		{"not reached yet", ConditionUnknown, nil, reasonConnecting, timestamp(statusNow)},
		{
			"verdict unchanged keeps the stamp",
			ConditionTrue,
			[]Condition{{Type: reachableConditionType, Status: ConditionTrue, LastTransitionTime: statusEarlier}},
			reasonConnected,
			statusEarlier,
		},
		{
			"verdict flipped moves the stamp",
			ConditionTrue,
			[]Condition{{Type: reachableConditionType, Status: ConditionFalse, LastTransitionTime: statusEarlier}},
			reasonConnected,
			timestamp(statusNow),
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

func TestSameStatusAnswersWhetherAWriteWouldChangeAnything(t *testing.T) {
	held := ReceiverStatus{
		Zones: map[string]ZoneStatus{
			equipment.MainZone: {Power: "on", Input: "MPLAY", Volume: "50", VolumeMax: "69.5", SoundMode: "MULTI CH IN"},
		},
		Conditions: []Condition{{Type: reachableConditionType, Status: ConditionTrue}},
	}
	movedVolume := held
	movedVolume.Zones = map[string]ZoneStatus{
		equipment.MainZone: {Power: "on", Input: "MPLAY", Volume: "51", VolumeMax: "69.5", SoundMode: "MULTI CH IN"},
	}
	addedZone := held
	addedZone.Zones = map[string]ZoneStatus{
		equipment.MainZone: {Power: "on", Input: "MPLAY", Volume: "50"},
		"zone2":            {Power: "on", Input: "PHONO", Volume: "90"},
	}
	movedProtocol := held
	bass := 3
	movedProtocol.Denon = &denon.Settings{Tone: denon.ToneSettings{Bass: &bass}}
	unreached := held
	unreached.Conditions = []Condition{{Type: reachableConditionType, Status: ConditionFalse}}
	noConditions := held
	noConditions.Conditions = nil

	cases := []struct {
		name string
		next ReceiverStatus
		same bool
	}{
		{"identical", held, true},
		{"volume moved", movedVolume, false},
		{"a zone appeared", addedZone, false},
		{"the protocol snapshot moved", movedProtocol, false},
		{"verdict moved", unreached, false},
		{"condition dropped", noConditions, false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, sameStatus(held, one.next), one.same)
		})
	}
}
