package main

// These tests cover the match from a controller event to a binding and
// the translation from a binding's action to mpv's own command words.

import (
	"strings"
	"testing"
)

// One gamepad's compiled table: the south button and both directions
// of the vertical hat axis.
var testBindings = []compiledBinding{
	{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause},
	{EventType: evAbs, Code: 0x11, Value: -1, Action: actionVolume, Amount: 5},
	{EventType: evAbs, Code: 0x11, Value: 1, Action: actionVolume, Amount: -5},
}

func keyEvent(code uint16, value int32) inputEvent {
	return inputEvent{Type: evKey, Code: code, Value: value}
}

func axisEvent(code uint16, value int32) inputEvent {
	return inputEvent{Type: evAbs, Code: code, Value: value}
}

func TestMatchBinding(t *testing.T) {
	cases := []struct {
		name  string
		event inputEvent
		want  string
	}{
		{name: "the button goes down", event: keyEvent(0x130, 1), want: actionPause},
		{name: "the button repeats while it is held", event: keyEvent(0x130, 2), want: ""},
		{name: "the button comes up", event: keyEvent(0x130, 0), want: ""},
		{name: "a button nothing binds", event: keyEvent(0x131, 1), want: ""},
		{name: "the hat goes up", event: axisEvent(0x11, -1), want: actionVolume},
		{name: "the hat goes down", event: axisEvent(0x11, 1), want: actionVolume},
		{name: "the hat returns to the middle", event: axisEvent(0x11, 0), want: ""},
		{name: "an axis nothing binds", event: axisEvent(0x10, 1), want: ""},
		{name: "the button's code on the wrong event type", event: axisEvent(0x130, 1), want: ""},
		{name: "a synchronization event", event: inputEvent{Type: 0x00, Code: 0x00, Value: 0}, want: ""},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			binding, matched := matchBinding(testBindings, each.event)
			mustMatch(t, matched, each.want != "")
			mustMatch(t, binding.Action, each.want)
		})
	}
}

func TestCommandFor(t *testing.T) {
	cases := []struct {
		name    string
		command mediaCommand
		want    []any
	}{
		{name: "pause", command: mediaCommand{Action: actionPause}, want: []any{"no-osd", "cycle", "pause"}},
		// A volume step and a mute press become no mpv command at
		// all. Each publishes the unit's next level, and the subscription
		// on the volume topic is what applies it.
		{name: "mute", command: mediaCommand{Action: actionMute}, want: nil},
		{name: "seek forward", command: mediaCommand{Action: actionSeek, Amount: 30}, want: []any{"no-osd", "seek", 30}},
		{name: "seek back", command: mediaCommand{Action: actionSeek, Amount: -10}, want: []any{"no-osd", "seek", -10}},
		{name: "volume", command: mediaCommand{Action: actionVolume, Amount: 5}, want: nil},
		{name: "chapter", command: mediaCommand{Action: actionChapter, Amount: -1}, want: []any{"no-osd", "add", "chapter", -1}},
		{name: "subtitles", command: mediaCommand{Action: actionSubtitles}, want: []any{"osd-auto", "cycle", "sub"}},
		{name: "audio", command: mediaCommand{Action: actionAudio}, want: []any{"osd-auto", "cycle", "audio"}},
		{name: "up", command: mediaCommand{Action: actionUp}, want: []any{"script-message-to", "display", "up"}},
		{name: "down", command: mediaCommand{Action: actionDown}, want: []any{"script-message-to", "display", "down"}},
		{name: "left", command: mediaCommand{Action: actionLeft}, want: []any{"script-message-to", "display", "left"}},
		{name: "right", command: mediaCommand{Action: actionRight}, want: []any{"script-message-to", "display", "right"}},
		{name: "select", command: mediaCommand{Action: actionSelect}, want: []any{"script-message-to", "display", "select"}},
		{name: "back", command: mediaCommand{Action: actionBack}, want: []any{"script-message-to", "display", "back"}},
		{
			name:    "info",
			command: mediaCommand{Action: actionInfo},
			want:    []any{"expand-properties", "show-text", "${filename}\n${time-pos} / ${duration}", 4000},
		},
		{name: "an action from a newer operator", command: mediaCommand{Action: "brightness", Amount: 1}, want: nil},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := commandFor(each.command)
			if len(got) != len(each.want) {
				t.Fatalf("command = %v, want %v", got, each.want)
			}
			for index := range got {
				if got[index] != each.want[index] {
					t.Errorf("command[%d] = %v, want %v", index, got[index], each.want[index])
				}
			}
		})
	}
}

// sendCommand writes one newline-delimited JSON command, the shape
// mpv's IPC socket reads.
func TestFeedbackFor(t *testing.T) {
	cases := []struct {
		name    string
		command mediaCommand
		want    []any
	}{
		{name: "seek summons the display", command: mediaCommand{Action: actionSeek, Amount: 30}, want: []any{"script-message-to", "display", "summon"}},
		{name: "chapter summons the display", command: mediaCommand{Action: actionChapter, Amount: 1}, want: []any{"script-message-to", "display", "summon"}},
		{name: "pause needs no follow-up", command: mediaCommand{Action: actionPause}, want: nil},
		{name: "volume needs no follow-up", command: mediaCommand{Action: actionVolume, Amount: 5}, want: nil},
		{name: "up needs no follow-up", command: mediaCommand{Action: actionUp}, want: nil},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := feedbackFor(each.command)
			if len(got) != len(each.want) {
				t.Fatalf("feedback = %v, want %v", got, each.want)
			}
			for index := range got {
				if got[index] != each.want[index] {
					t.Errorf("feedback[%d] = %v, want %v", index, got[index], each.want[index])
				}
			}
		})
	}
}

func TestSendCommandWritesOneJSONLine(t *testing.T) {
	var written strings.Builder
	mustSucceed(t, sendCommand(&written, []any{"osd-auto", "cycle", "pause"}))
	if got := written.String(); got != "{\"command\":[\"osd-auto\",\"cycle\",\"pause\"]}\n" {
		t.Errorf("command = %q", got)
	}
}

// The button vocabulary is the whole EV_KEY name space, so a Keymap
// may name a face button, a transport key, or a d-pad button, at the
// codes the kernel gives them.
func TestTheButtonCodesCarryTheWholeKeyNameSpace(t *testing.T) {
	cases := []struct {
		name string
		code uint16
		want bool
	}{
		{name: "BTN_SOUTH", code: 0x130, want: true},
		{name: "BTN_A", code: 0x130, want: true},
		{name: "BTN_THUMBR", code: 0x13e, want: true},
		{name: "BTN_DPAD_UP", code: 0x220, want: true},
		{name: "BTN_TRIGGER_HAPPY1", code: 0x2c0, want: true},
		{name: "KEY_PLAYPAUSE", code: 0x0a4, want: true},
		{name: "KEY_OK", code: 0x160, want: true},
		{name: "KEY_HOMEPAGE", code: 0x00ac, want: true},
		{name: "KEY_MAX", want: false},
		{name: "ABS_HAT0X", want: false},
		{name: "BTN_NOTHING", want: false},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			code, known := buttonCodes[each.name]
			mustMatch(t, known, each.want)
			if each.want {
				mustMatch(t, code, each.code)
			}
		})
	}
}

// A code names back, so a report reads in the names a Keymap uses.
// The first name the header states wins where the kernel gives one
// code several.
func TestACodeNamesItselfBack(t *testing.T) {
	cases := []struct {
		name string
		code uint16
		want string
	}{
		{name: "the name a gamepad's face button reports", code: 0x130, want: "BTN_SOUTH"},
		{name: "a transport key", code: 0x0a4, want: "KEY_PLAYPAUSE"},
		{name: "a hat axis", code: 0x10, want: "ABS_HAT0X"},
		{name: "a code the kernel names nothing", code: 0x2ff, want: ""},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			if each.want != "" && strings.HasPrefix(each.want, "ABS_") {
				mustMatch(t, axisCodeNames[each.code], each.want)
				return
			}
			mustMatch(t, keyCodeNames[each.code], each.want)
		})
	}
}
