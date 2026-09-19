package main

// The arithmetic between the bus scale and a driver's own steps, and
// the payloads that carry a level on the bus.

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

func TestStepsForLevelMapsTheBusScaleOntoTheDriver(t *testing.T) {
	cases := []struct {
		name     string
		level    int
		maxSteps int
		steps    int
		ok       bool
	}{
		{name: "silent on a 69.5 limit", level: 0, maxSteps: 139, steps: 0, ok: true},
		{name: "half way up a 69.5 limit", level: 50, maxSteps: 139, steps: 70, ok: true},
		{name: "full on a 69.5 limit", level: 100, maxSteps: 139, steps: 139, ok: true},
		{name: "silent on a 98.0 limit", level: 0, maxSteps: 196, steps: 0, ok: true},
		{name: "half way up a 98.0 limit", level: 50, maxSteps: 196, steps: 98, ok: true},
		{name: "full on a 98.0 limit", level: 100, maxSteps: 196, steps: 196, ok: true},
		{name: "below the bus scale", level: -20, maxSteps: 139, steps: 0, ok: true},
		{name: "above the bus scale", level: 140, maxSteps: 139, steps: 139, ok: true},
		{name: "no limit reported", level: 50, maxSteps: 0, steps: 0, ok: false},
		{name: "limit still unknown", level: 50, maxSteps: equipment.Unknown, steps: 0, ok: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			steps, ok := stepsForLevel(test.level, test.maxSteps)
			mustMatch(t, ok, test.ok)
			mustMatch(t, steps, test.steps)
		})
	}
}

func TestLevelForStepsMapsTheDriverBackOntoTheBus(t *testing.T) {
	cases := []struct {
		name     string
		steps    int
		maxSteps int
		level    int
		ok       bool
	}{
		{name: "silent on a 69.5 limit", steps: 0, maxSteps: 139, level: 0, ok: true},
		{name: "half way up a 69.5 limit", steps: 70, maxSteps: 139, level: 50, ok: true},
		{name: "at a 69.5 limit", steps: 139, maxSteps: 139, level: 100, ok: true},
		{name: "above a 69.5 limit", steps: 150, maxSteps: 139, level: 100, ok: true},
		{name: "half way up a 98.0 limit", steps: 98, maxSteps: 196, level: 50, ok: true},
		{name: "at a 98.0 limit", steps: 196, maxSteps: 196, level: 100, ok: true},
		{name: "volume still unknown", steps: equipment.Unknown, maxSteps: 139, level: 0, ok: false},
		{name: "no limit reported", steps: 70, maxSteps: 0, level: 0, ok: false},
		{name: "limit still unknown", steps: 70, maxSteps: equipment.Unknown, level: 0, ok: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			level, ok := levelForSteps(test.steps, test.maxSteps)
			mustMatch(t, ok, test.ok)
			mustMatch(t, level, test.level)
		})
	}
}

// The echo suppression the session stands on: the level the receiver
// reports back maps to the same steps the operator sent, so an echo is
// not read as a second change.
func TestTheLevelMappingReachesAFixedPointInOneStep(t *testing.T) {
	limits := []struct {
		name     string
		maxSteps int
	}{
		{name: "69.5 limit", maxSteps: 139},
		{name: "98.0 limit", maxSteps: 196},
	}
	for _, limit := range limits {
		t.Run(limit.name, func(t *testing.T) {
			for level := minLevel; level <= maxLevel; level++ {
				t.Run(strconv.Itoa(level), func(t *testing.T) {
					sent, ok := stepsForLevel(level, limit.maxSteps)
					mustMatch(t, ok, true)
					echoed, ok := levelForSteps(sent, limit.maxSteps)
					mustMatch(t, ok, true)
					again, ok := stepsForLevel(echoed, limit.maxSteps)
					mustMatch(t, ok, true)
					mustMatch(t, again, sent)
				})
			}
		})
	}
}

func TestParseVolumeStateReadsThePayloadOnTheVolumeTopic(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		state   volumeState
		ok      bool
	}{
		{name: "a level and a mute", payload: `{"level":40,"muted":true}`, state: volumeState{Level: 40, Muted: true}, ok: true},
		{name: "not json", payload: `not json`, state: volumeState{}, ok: false},
		{name: "above the bus scale", payload: `{"level":140,"muted":false}`, state: volumeState{Level: 100}, ok: true},
		{name: "below the bus scale", payload: `{"level":-40,"muted":false}`, state: volumeState{Level: 0}, ok: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state, ok := parseVolumeState([]byte(test.payload))
			mustMatch(t, ok, test.ok)
			mustMatch(t, state, test.state)
		})
	}
}

func TestMarshalVolumeStateWritesTheClampedPayload(t *testing.T) {
	cases := []struct {
		name  string
		state volumeState
		want  string
	}{
		{name: "a level and a mute", state: volumeState{Level: 40, Muted: true}, want: `{"level":40,"muted":true}`},
		{name: "above the bus scale", state: volumeState{Level: 140}, want: `{"level":100,"muted":false}`},
		{name: "below the bus scale", state: volumeState{Level: -40}, want: `{"level":0,"muted":false}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			payload, err := marshalVolumeState(test.state)
			mustSucceed(t, err)
			mustMatch(t, string(payload), test.want)
		})
	}
}

func TestTheOwnerTopicSitsUnderTheVolumeTopic(t *testing.T) {
	mustMatch(t, ownerTopic("liken/player/den/volume"), "liken/player/den/volume/owner")
}

func TestTheOwnerMarkNamesTheReceiverThatHoldsTheSession(t *testing.T) {
	mark, err := ownerMark("den")
	mustSucceed(t, err)
	mustMatch(t, string(mark), `{"owner":"receiver/den"}`)

	var owner volumeOwner
	mustSucceed(t, json.Unmarshal(mark, &owner))
	mustMatch(t, owner.Owner, "receiver/den")
}

// The ceiling comes from the spec alone, because a driver's own volume
// limit line moves while the room plays.
func TestCeilingStepsTakesTheDeclaredCeilingAlone(t *testing.T) {
	cases := []struct {
		name string
		rule ReceiverVolume
		want int
	}{
		{"a whole unit", ReceiverVolume{Max: 52}, 104},
		{"a half unit", ReceiverVolume{Max: 69.5}, 139},
		{"a figure above the driver's own scale", ReceiverVolume{Max: 120}, 240},
		{"none declared", ReceiverVolume{}, 0},
		{"below zero", ReceiverVolume{Max: -5}, 0},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, ceilingSteps(one.rule, 2), one.want)
		})
	}
}

// An absent step is one whole unit of the driver's scale.
func TestPressStepsTakesTheDeclaredStep(t *testing.T) {
	cases := []struct {
		name       string
		rule       ReceiverVolume
		resolution int
		want       int
	}{
		{"none declared", ReceiverVolume{}, 2, 2},
		{"one unit", ReceiverVolume{Step: 1}, 2, 2},
		{"a half unit", ReceiverVolume{Step: 0.5}, 2, 1},
		{"two units", ReceiverVolume{Step: 2}, 2, 4},
		{"below the smallest the driver takes", ReceiverVolume{Step: 0.1}, 2, 2},
		{"no resolution at all", ReceiverVolume{Step: 1}, 0, 1},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, pressSteps(one.rule, one.resolution), one.want)
		})
	}
}

// A figure the scale cannot express reads as no steps.
func TestStepsFromScaleOnAnImpossibleResolution(t *testing.T) {
	mustMatch(t, stepsFromScale(72, 0), 0)
}
