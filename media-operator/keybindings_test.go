package main

// These tests cover the playback pod's table from key names to what
// it does with them, which is the pod's whole answer to a controller.

import "testing"

// Each key the playback pod binds becomes its own command, and the
// amounts are this pod's defaults.
func TestEachBoundKeyBecomesItsCommand(t *testing.T) {
	cases := []struct {
		key  string
		want mediaCommand
	}{
		{key: "KEY_PLAYPAUSE", want: mediaCommand{Action: actionPause}},
		{key: "KEY_PLAY", want: mediaCommand{Action: actionPause}},
		{key: "KEY_PAUSE", want: mediaCommand{Action: actionPause}},
		{key: "KEY_PLAYCD", want: mediaCommand{Action: actionPause}},
		{key: "KEY_REWIND", want: mediaCommand{Action: actionSeek, Amount: -10}},
		{key: "KEY_FASTFORWARD", want: mediaCommand{Action: actionSeek, Amount: 10}},
		{key: "KEY_PREVIOUSSONG", want: mediaCommand{Action: actionChapter, Amount: -1}},
		{key: "KEY_NEXTSONG", want: mediaCommand{Action: actionChapter, Amount: 1}},
		{key: "KEY_VOLUMEUP", want: mediaCommand{Action: actionVolume, Amount: 5}},
		{key: "KEY_VOLUMEDOWN", want: mediaCommand{Action: actionVolume, Amount: -5}},
		{key: "KEY_MUTE", want: mediaCommand{Action: actionMute}},
		{key: "KEY_SUBTITLE", want: mediaCommand{Action: actionSubtitles}},
		{key: "KEY_AUDIO", want: mediaCommand{Action: actionAudio}},
		{key: "KEY_INFO", want: mediaCommand{Action: actionInfo}},
		{key: "KEY_UP", want: mediaCommand{Action: actionUp}},
		{key: "KEY_DOWN", want: mediaCommand{Action: actionDown}},
		{key: "KEY_LEFT", want: mediaCommand{Action: actionLeft}},
		{key: "KEY_RIGHT", want: mediaCommand{Action: actionRight}},
		{key: "KEY_ENTER", want: mediaCommand{Action: actionSelect}},
		{key: "KEY_OK", want: mediaCommand{Action: actionSelect}},
		{key: "KEY_SELECT", want: mediaCommand{Action: actionSelect}},
		{key: "KEY_KPENTER", want: mediaCommand{Action: actionSelect}},
		{key: "KEY_BACK", want: mediaCommand{Action: actionBack}},
		{key: "KEY_ESC", want: mediaCommand{Action: actionBack}},
		{key: "KEY_EXIT", want: mediaCommand{Action: actionBack}},
		{key: "KEY_CYCLEWINDOWS", want: mediaCommand{Action: actionCycleFocus}},
	}

	for _, each := range cases {
		t.Run(each.key, func(t *testing.T) {
			command, bound := commandForKey(keyEvent{Key: each.key, Value: 1})
			mustMatch(t, bound, true)
			mustMatch(t, command, each.want)
		})
	}
}

// A seek, a chapter step, a volume step, and an arrow act on the
// repeat as well, because those are the four a person holds.
func TestTheFourHeldKindsActOnARepeat(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{key: "KEY_FASTFORWARD", want: true},
		{key: "KEY_REWIND", want: true},
		{key: "KEY_NEXTSONG", want: true},
		{key: "KEY_PREVIOUSSONG", want: true},
		{key: "KEY_VOLUMEUP", want: true},
		{key: "KEY_VOLUMEDOWN", want: true},
		{key: "KEY_UP", want: true},
		{key: "KEY_RIGHT", want: true},
		{key: "KEY_PLAYPAUSE"},
		{key: "KEY_MUTE"},
		{key: "KEY_ENTER"},
		{key: "KEY_BACK"},
		{key: "KEY_INFO"},
		{key: "KEY_CYCLEWINDOWS"},
	}

	for _, each := range cases {
		t.Run(each.key, func(t *testing.T) {
			_, bound := commandForKey(keyEvent{Key: each.key, Value: 2})
			mustMatch(t, bound, each.want)
		})
	}
}

// A release does nothing, and so does a key this pod has no row for,
// the reserved keys of a home surface among them.
func TestAReleaseAndAnUnboundKeyDoNothing(t *testing.T) {
	cases := []struct {
		name  string
		event keyEvent
	}{
		{name: "the release of a bound key", event: keyEvent{Key: "KEY_PLAYPAUSE", Value: 0}},
		{name: "a key of the home surface", event: keyEvent{Key: "KEY_HOMEPAGE", Value: 1}},
		{name: "the power key", event: keyEvent{Key: "KEY_POWER", Value: 1}},
		{name: "a letter on a keyboard remote", event: keyEvent{Key: "KEY_Q", Value: 1}},
		{name: "a value no kernel reports", event: keyEvent{Key: "KEY_PLAYPAUSE", Value: 7}},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			_, bound := commandForKey(each.event)
			mustMatch(t, bound, false)
		})
	}
}
