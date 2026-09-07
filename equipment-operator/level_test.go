package main

// The arithmetic between the bus scale and the receiver's own half
// steps, and the payloads that carry a level on the bus.

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestParseHalfStepsReadsTheDigitsTheReceiverSends(t *testing.T) {
	cases := []struct {
		digits string
		halves int
		ok     bool
	}{
		{digits: "50", halves: 100, ok: true},
		{digits: "505", halves: 101, ok: true},
		{digits: "05", halves: 10, ok: true},
		{digits: "055", halves: 11, ok: true},
		{digits: "695", halves: 139, ok: true},
		{digits: "00", halves: 0, ok: true},
		{digits: "5", halves: unknownHalves, ok: false},
		{digits: "5055", halves: unknownHalves, ok: false},
		{digits: "abc", halves: unknownHalves, ok: false},
		{digits: "501", halves: unknownHalves, ok: false},
		{digits: "-5", halves: unknownHalves, ok: false},
	}
	for _, test := range cases {
		t.Run(test.digits, func(t *testing.T) {
			halves, ok := parseHalfSteps(test.digits)
			mustMatch(t, ok, test.ok)
			mustMatch(t, halves, test.halves)
		})
	}
}

func TestFormatHalfStepsWritesTheVolumeAPersonReads(t *testing.T) {
	cases := []struct {
		halves int
		want   string
	}{
		{halves: 100, want: "50"},
		{halves: 101, want: "50.5"},
		{halves: 139, want: "69.5"},
		{halves: 0, want: "0"},
		{halves: unknownHalves, want: ""},
	}
	for _, test := range cases {
		t.Run(strconv.Itoa(test.halves), func(t *testing.T) {
			mustMatch(t, formatHalfSteps(test.halves), test.want)
		})
	}
}

func TestHalfStepDigitsWritesTheDigitsASetCommandCarries(t *testing.T) {
	cases := []struct {
		halves int
		want   string
	}{
		{halves: 100, want: "50"},
		{halves: 101, want: "505"},
		{halves: 10, want: "05"},
		{halves: 11, want: "055"},
		{halves: 139, want: "695"},
	}
	for _, test := range cases {
		t.Run(strconv.Itoa(test.halves), func(t *testing.T) {
			mustMatch(t, halfStepDigits(test.halves), test.want)
		})
	}
}

// Every count the receiver can report survives the trip out to digits
// and back, which is what makes the set command and the echo
// comparable.
func TestEveryHalfStepCountSurvivesTheRoundTrip(t *testing.T) {
	for count := 0; count <= 196; count++ {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			halves, ok := parseHalfSteps(halfStepDigits(count))
			mustMatch(t, ok, true)
			mustMatch(t, halves, count)
		})
	}
}

func TestHalvesForLevelMapsTheBusScaleOntoTheReceiver(t *testing.T) {
	cases := []struct {
		name      string
		level     int
		maxHalves int
		halves    int
		ok        bool
	}{
		{name: "silent on a 69.5 limit", level: 0, maxHalves: 139, halves: 0, ok: true},
		{name: "half way up a 69.5 limit", level: 50, maxHalves: 139, halves: 70, ok: true},
		{name: "full on a 69.5 limit", level: 100, maxHalves: 139, halves: 139, ok: true},
		{name: "silent on a 98.0 limit", level: 0, maxHalves: 196, halves: 0, ok: true},
		{name: "half way up a 98.0 limit", level: 50, maxHalves: 196, halves: 98, ok: true},
		{name: "full on a 98.0 limit", level: 100, maxHalves: 196, halves: 196, ok: true},
		{name: "below the bus scale", level: -20, maxHalves: 139, halves: 0, ok: true},
		{name: "above the bus scale", level: 140, maxHalves: 139, halves: 139, ok: true},
		{name: "no limit reported", level: 50, maxHalves: 0, halves: unknownHalves, ok: false},
		{name: "limit still unknown", level: 50, maxHalves: unknownHalves, halves: unknownHalves, ok: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			halves, ok := halvesForLevel(test.level, test.maxHalves)
			mustMatch(t, ok, test.ok)
			mustMatch(t, halves, test.halves)
		})
	}
}

func TestLevelForHalvesMapsTheReceiverBackOntoTheBus(t *testing.T) {
	cases := []struct {
		name      string
		halves    int
		maxHalves int
		level     int
		ok        bool
	}{
		{name: "silent on a 69.5 limit", halves: 0, maxHalves: 139, level: 0, ok: true},
		{name: "half way up a 69.5 limit", halves: 70, maxHalves: 139, level: 50, ok: true},
		{name: "at a 69.5 limit", halves: 139, maxHalves: 139, level: 100, ok: true},
		{name: "above a 69.5 limit", halves: 150, maxHalves: 139, level: 100, ok: true},
		{name: "half way up a 98.0 limit", halves: 98, maxHalves: 196, level: 50, ok: true},
		{name: "at a 98.0 limit", halves: 196, maxHalves: 196, level: 100, ok: true},
		{name: "volume still unknown", halves: unknownHalves, maxHalves: 139, level: 0, ok: false},
		{name: "no limit reported", halves: 70, maxHalves: 0, level: 0, ok: false},
		{name: "limit still unknown", halves: 70, maxHalves: unknownHalves, level: 0, ok: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			level, ok := levelForHalves(test.halves, test.maxHalves)
			mustMatch(t, ok, test.ok)
			mustMatch(t, level, test.level)
		})
	}
}

// The echo suppression the session stands on: the level the receiver
// reports back maps to the same half steps the operator sent, so an
// echo is not read as a second change.
func TestTheLevelMappingReachesAFixedPointInOneStep(t *testing.T) {
	limits := []struct {
		name      string
		maxHalves int
	}{
		{name: "69.5 limit", maxHalves: 139},
		{name: "98.0 limit", maxHalves: 196},
	}
	for _, limit := range limits {
		t.Run(limit.name, func(t *testing.T) {
			for level := minLevel; level <= maxLevel; level++ {
				t.Run(strconv.Itoa(level), func(t *testing.T) {
					sent, ok := halvesForLevel(level, limit.maxHalves)
					mustMatch(t, ok, true)
					echoed, ok := levelForHalves(sent, limit.maxHalves)
					mustMatch(t, ok, true)
					again, ok := halvesForLevel(echoed, limit.maxHalves)
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
