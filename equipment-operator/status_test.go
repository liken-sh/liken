package main

// The status the operator reports, built from what the receiver last
// said.

import (
	"testing"
	"time"
)

// The moment every status test stamps, and one from before it.
var (
	statusNow     = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	statusEarlier = "2026-09-01T00:00:00Z"
)

// testState is a state whose fields each case overrides.
func testState(power string, volume, volumeMax int) denonState {
	state := newDenonState()
	state.Power = power
	state.Volume = volume
	state.VolumeMax = volumeMax
	state.Input = "MPLAY"
	state.SoundMode = "MULTI CH IN"
	state.Reachable = ConditionTrue
	return state
}

func TestBuildReceiverStatusCarriesTheReceiversOwnUnits(t *testing.T) {
	cases := []struct {
		name      string
		state     denonState
		volume    string
		volumeMax string
	}{
		{"whole steps", testState(powerOn, 100, 139), "50", "69.5"},
		{"half steps", testState(powerOn, 131, 139), "65.5", "69.5"},
		{"zero", testState(powerStandby, 0, 139), "0", "69.5"},
		{"unknown volume", testState(powerStandby, unknownHalves, unknownHalves), "", ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := buildReceiverStatus(one.state, 3, nil, statusNow)

			mustMatch(t, status.Power, one.state.Power)
			mustMatch(t, status.Input, "MPLAY")
			mustMatch(t, status.Volume, one.volume)
			mustMatch(t, status.VolumeMax, one.volumeMax)
			mustMatch(t, status.SoundMode, "MULTI CH IN")
			mustMatch(t, status.Service, "")
			mustMatch(t, len(status.Conditions), 1)
		})
	}
}

func TestBuildReceiverStatusCarriesTheMuteFlag(t *testing.T) {
	state := testState(powerOn, 100, 139)
	state.Mute = true

	mustMatch(t, buildReceiverStatus(state, 1, nil, statusNow).Mute, true)
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
		Power: powerOn, Input: "MPLAY", Volume: "50", VolumeMax: "69.5", SoundMode: "MULTI CH IN",
		Conditions: []Condition{{Type: reachableConditionType, Status: ConditionTrue}},
	}
	movedVolume := held
	movedVolume.Volume = "51"
	muted := held
	muted.Mute = true
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
		{"mute moved", muted, false},
		{"verdict moved", unreached, false},
		{"condition dropped", noConditions, false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, sameStatus(held, one.next), one.same)
		})
	}
}
