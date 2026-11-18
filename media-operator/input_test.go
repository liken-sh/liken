package main

import (
	"strings"
	"testing"
)

func TestCommandFor(t *testing.T) {
	cases := []struct {
		name    string
		command mediaCommand
		want    []any
	}{
		{name: "pause", command: mediaCommand{Action: actionPause}, want: []any{"no-osd", "cycle", "pause"}},
		{name: "play", command: mediaCommand{Action: actionPlay}, want: []any{"no-osd", "set", "pause", "no"}},
		{name: "hold", command: mediaCommand{Action: actionHold}, want: []any{"no-osd", "set", "pause", "yes"}},
		{name: "seek forward", command: mediaCommand{Action: actionSeek, Amount: 30}, want: []any{"no-osd", "seek", 30}},
		{name: "seek back", command: mediaCommand{Action: actionSeek, Amount: -10}, want: []any{"no-osd", "seek", -10}},
		{name: "chapter", command: mediaCommand{Action: actionChapter, Amount: -1}, want: []any{"no-osd", "add", "chapter", -1}},
		{name: "subtitles", command: mediaCommand{Action: actionSubtitles}, want: []any{"osd-auto", "cycle", "sub"}},
		{name: "audio", command: mediaCommand{Action: actionAudio}, want: []any{"osd-auto", "cycle", "audio"}},
		{name: "up", command: mediaCommand{Action: actionUp}, want: []any{"script-message", "up"}},
		{name: "down", command: mediaCommand{Action: actionDown}, want: []any{"script-message", "down"}},
		{name: "left", command: mediaCommand{Action: actionLeft}, want: []any{"script-message", "left"}},
		{name: "right", command: mediaCommand{Action: actionRight}, want: []any{"script-message", "right"}},
		{name: "select", command: mediaCommand{Action: actionSelect}, want: []any{"script-message", "select"}},
		{name: "back", command: mediaCommand{Action: actionBack}, want: []any{"script-message", "back"}},
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

func TestFeedbackFor(t *testing.T) {
	cases := []struct {
		name    string
		command mediaCommand
		want    []any
	}{
		{name: "seek summons the display", command: mediaCommand{Action: actionSeek, Amount: 30}, want: []any{"script-message", "summon"}},
		{name: "chapter summons the display", command: mediaCommand{Action: actionChapter, Amount: 1}, want: []any{"script-message", "summon"}},
		{name: "pause needs no follow-up", command: mediaCommand{Action: actionPause}, want: nil},
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
		t.Errorf("sendCommand wrote %q, want one JSON line holding the command", got)
	}
}

func TestButtonCodesContainEveryKeyName(t *testing.T) {
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

func TestCodeNamesMapBackToTheFirstName(t *testing.T) {
	cases := []struct {
		name string
		code uint16
		want string
	}{
		{name: "a gamepad face button", code: 0x130, want: "BTN_SOUTH"},
		{name: "a transport key", code: 0x0a4, want: "KEY_PLAYPAUSE"},
		{name: "a hat axis", code: 0x10, want: "ABS_HAT0X"},
		{name: "a code the kernel has no name for", code: 0x2ff, want: ""},
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
