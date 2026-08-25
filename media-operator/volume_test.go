package main

// These tests cover the level as state: the payload on the wire,
// the clamp every path runs through, the arithmetic of one press,
// the mpv words that apply a state, and the desk the operator seeds
// from.

import (
	"encoding/json"
	"testing"
)

// The payload is the pair the plan names, both fields always
// written, so a reader never needs a default for a field a message
// omits.
func TestVolumeStateMarshalsBothFields(t *testing.T) {
	payload, err := marshalVolumeState(volumeState{Level: 40, Muted: true})
	mustSucceed(t, err)
	mustMatch(t, string(payload), `{"level":40,"muted":true}`)
}

// The clamp holds the level inside 0 to 100 on the way in and on
// the way out, so nothing this project publishes and nothing it applies is
// ever out of range.
func TestVolumeClampsToTheRange(t *testing.T) {
	cases := []struct {
		name  string
		level int
		want  int
	}{
		{name: "below the floor", level: -20, want: 0},
		{name: "the floor", level: 0, want: 0},
		{name: "in range", level: 45, want: 45},
		{name: "unity", level: 100, want: 100},
		{name: "above unity", level: 140, want: 100},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, volumeState{Level: each.level}.clamped().Level, each.want)
		})
	}
}

// A message another program published out of range is clamped and
// not refused, so nothing on the bus can drive mpv past unity.
func TestParseVolumeStateClampsAndRejectsGarbage(t *testing.T) {
	state, ok := parseVolumeState([]byte(`{"level":250,"muted":true}`))
	mustMatch(t, ok, true)
	mustMatch(t, state, volumeState{Level: 100, Muted: true})

	_, ok = parseVolumeState([]byte("not json"))
	mustMatch(t, ok, false)
}

// One press against the state the topic last delivered. The step
// carries its sign, mute is a plain toggle, and both clamp.
func TestNextVolumeFromAPress(t *testing.T) {
	cases := []struct {
		name    string
		held    volumeState
		command mediaCommand
		want    volumeState
	}{
		{
			name:    "a step up",
			held:    volumeState{Level: 40},
			command: mediaCommand{Action: actionVolume, Amount: 5},
			want:    volumeState{Level: 45},
		},
		{
			name:    "a step down",
			held:    volumeState{Level: 40},
			command: mediaCommand{Action: actionVolume, Amount: -5},
			want:    volumeState{Level: 35},
		},
		{
			name:    "a step that would pass unity",
			held:    volumeState{Level: 98},
			command: mediaCommand{Action: actionVolume, Amount: 5},
			want:    volumeState{Level: 100},
		},
		{
			name:    "a step that would pass silence",
			held:    volumeState{Level: 3},
			command: mediaCommand{Action: actionVolume, Amount: -5},
			want:    volumeState{Level: 0},
		},
		{
			name:    "a step before any message arrives",
			held:    defaultVolumeState(),
			command: mediaCommand{Action: actionVolume, Amount: -10},
			want:    volumeState{Level: 90},
		},
		{
			name:    "mute",
			held:    volumeState{Level: 40},
			command: mediaCommand{Action: actionMute},
			want:    volumeState{Level: 40, Muted: true},
		},
		{
			name:    "unmute",
			held:    volumeState{Level: 40, Muted: true},
			command: mediaCommand{Action: actionMute},
			want:    volumeState{Level: 40},
		},
		{
			name:    "a step keeps the muted flag",
			held:    volumeState{Level: 40, Muted: true},
			command: mediaCommand{Action: actionVolume, Amount: 5},
			want:    volumeState{Level: 45, Muted: true},
		},
		{
			name:    "an action that is not a level",
			held:    volumeState{Level: 40},
			command: mediaCommand{Action: actionPause},
			want:    volumeState{Level: 40},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, nextVolume(each.held, each.command), each.want)
		})
	}
}

// Two pods that pressed against the same message publish the same
// absolute state, so the step lands once however many screens read it.
func TestTwoPressesAgainstOneMessageAgree(t *testing.T) {
	held := volumeState{Level: 40}
	press := mediaCommand{Action: actionVolume, Amount: 5}
	mustMatch(t, nextVolume(held, press), nextVolume(held, press))
}

// The level becomes mpv's own words, both carrying no-osd because
// the display draws the indicator.
func TestVolumeCommandsCarryNoOSD(t *testing.T) {
	got := volumeCommands(volumeState{Level: 45, Muted: true})
	mustMatchAll(t, commandLines(t, got), []string{
		`{"command":["no-osd","set","volume","45"]}`,
		`{"command":["no-osd","set","mute","yes"]}`,
	})

	got = volumeCommands(volumeState{Level: 100})
	mustMatchAll(t, commandLines(t, got), []string{
		`{"command":["no-osd","set","volume","100"]}`,
		`{"command":["no-osd","set","mute","no"]}`,
	})
}

// commandLines encodes each command the way the sidecar writes it to mpv's
// socket, so a test reads the line and not the slice.
func commandLines(t *testing.T, commands [][]any) []string {
	t.Helper()
	lines := make([]string, 0, len(commands))
	for _, command := range commands {
		encoded, err := json.Marshal(mpvCommand{Command: command})
		mustSucceed(t, err)
		lines = append(lines, string(encoded))
	}
	return lines
}

// A Play's block lies over the unit's current state field by field,
// so a Play that states only a level leaves the muted flag alone.
func TestMergedWithLaysThePlayOverTheUnit(t *testing.T) {
	held := volumeState{Level: 30, Muted: true}
	cases := []struct {
		name     string
		override *PlayVolume
		want     volumeState
	}{
		{name: "no block at all", override: nil, want: volumeState{Level: 30, Muted: true}},
		{name: "an empty block", override: &PlayVolume{}, want: volumeState{Level: 30, Muted: true}},
		{name: "a level alone", override: &PlayVolume{Level: level(70)}, want: volumeState{Level: 70, Muted: true}},
		{name: "a muted flag alone", override: &PlayVolume{Muted: muted(false)}, want: volumeState{Level: 30}},
		{
			name:     "both",
			override: &PlayVolume{Level: level(55), Muted: muted(false)},
			want:     volumeState{Level: 55},
		},
		{name: "a level past unity", override: &PlayVolume{Level: level(400)}, want: volumeState{Level: 100, Muted: true}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, held.mergedWith(each.override), each.want)
		})
	}
}

func level(value int) *int { return &value }

func muted(value bool) *bool { return &value }

// The desk answers the seed's one question, and it drops the units
// the cluster no longer holds.
func TestVolumeDeskHoldsAndForgets(t *testing.T) {
	desk := newVolumeDesk()
	key := playerKey("house", "theater")

	_, held := desk.stateFor(key)
	mustMatch(t, held, false)

	desk.setState(key, volumeState{Level: 250})
	state, held := desk.stateFor(key)
	mustMatch(t, held, true)
	mustMatch(t, state, volumeState{Level: 100})

	desk.retain(map[string]bool{})
	_, held = desk.stateFor(key)
	mustMatch(t, held, false)
}
