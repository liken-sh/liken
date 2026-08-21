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
		binding compiledBinding
		want    []any
	}{
		{name: "pause", binding: compiledBinding{Action: actionPause}, want: []any{"osd-auto", "cycle", "pause"}},
		{name: "mute", binding: compiledBinding{Action: actionMute}, want: []any{"osd-auto", "cycle", "mute"}},
		{name: "seek forward", binding: compiledBinding{Action: actionSeek, Amount: 30}, want: []any{"osd-auto", "seek", 30}},
		{name: "seek back", binding: compiledBinding{Action: actionSeek, Amount: -10}, want: []any{"osd-auto", "seek", -10}},
		{name: "volume", binding: compiledBinding{Action: actionVolume, Amount: 5}, want: []any{"osd-auto", "add", "volume", 5}},
		{name: "chapter", binding: compiledBinding{Action: actionChapter, Amount: -1}, want: []any{"osd-auto", "add", "chapter", -1}},
		{name: "subtitles", binding: compiledBinding{Action: actionSubtitles}, want: []any{"osd-auto", "cycle", "sub"}},
		{name: "audio", binding: compiledBinding{Action: actionAudio}, want: []any{"osd-auto", "cycle", "audio"}},
		{
			name:    "info",
			binding: compiledBinding{Action: actionInfo},
			want:    []any{"expand-properties", "show-text", "${filename}\n${time-pos} / ${duration}", 4000},
		},
		{name: "an action from a newer operator", binding: compiledBinding{Action: "brightness", Amount: 1}, want: nil},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := commandFor(each.binding)
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
func TestSendCommandWritesOneJSONLine(t *testing.T) {
	var written strings.Builder
	mustSucceed(t, sendCommand(&written, []any{"osd-auto", "cycle", "pause"}))
	if got := written.String(); got != "{\"command\":[\"osd-auto\",\"cycle\",\"pause\"]}\n" {
		t.Errorf("command = %q", got)
	}
}
