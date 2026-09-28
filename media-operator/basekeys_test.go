package main

// These tests cover the base table and the fold a Keymap makes over
// it, which together are what a controller means before any consumer
// reads it.

import (
	"reflect"
	"strings"
	"testing"
)

// The base compiled, as the rows a pod with no Keymap reads. It is
// written out in full because it is the contract every controller
// arrives on.
var wantedBase = []compiledBinding{
	{EventType: evKey, Code: 0x130, Value: 1, Key: "KEY_ENTER"},
	{EventType: evKey, Code: 0x131, Value: 1, Key: "KEY_BACK"},
	{EventType: evAbs, Code: 0x11, Value: -1, Key: "KEY_UP", RepeatDelay: 400, RepeatInterval: 250},
	{EventType: evAbs, Code: 0x11, Value: 1, Key: "KEY_DOWN", RepeatDelay: 400, RepeatInterval: 250},
	{EventType: evAbs, Code: 0x10, Value: -1, Key: "KEY_LEFT", RepeatDelay: 400, RepeatInterval: 250},
	{EventType: evAbs, Code: 0x10, Value: 1, Key: "KEY_RIGHT", RepeatDelay: 400, RepeatInterval: 250},
}

// A Remote that names no Keymap gets the base alone, so a controller
// works before anybody writes a table for it.
func TestARemoteWithNoKeymapGetsTheBaseTable(t *testing.T) {
	table, err := compileTable(nil)

	mustSucceed(t, err)
	if !reflect.DeepEqual(table, wantedBase) {
		t.Errorf("table = %+v, want %+v", table, wantedBase)
	}
}

// The base binds the two face buttons every console reads as enter and
// back, and the two hat axes as the four arrows with a repeat.
func TestTheBaseBindsTheHatsAndTheTwoFaceButtons(t *testing.T) {
	if !reflect.DeepEqual(baseKeys, wantedBase) {
		t.Errorf("base = %+v, want %+v", baseKeys, wantedBase)
	}
}

// A Keymap row replaces the base row for the same control, so a pad
// whose south button should mute reports the mute and not the enter.
func TestAKeymapRowReplacesTheBaseRowForTheSameControl(t *testing.T) {
	keymap := &Keymap{
		Metadata: ObjectMeta{Name: "pad"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{{Press: "BTN_SOUTH", Key: "KEY_MUTE"}},
			Axes:    []KeymapAxis{{Axis: "ABS_HAT0Y", Value: -1, Key: keyNone}},
		},
	}

	table, err := compileTable(keymap)

	mustSucceed(t, err)
	mustMatch(t, len(table), len(wantedBase))
	mustMatch(t, table[0], compiledBinding{EventType: evKey, Code: 0x130, Value: 1, Key: "KEY_MUTE"})
	mustMatch(t, table[2], compiledBinding{EventType: evAbs, Code: 0x11, Value: -1, Key: keyNone})
}

// A Keymap row for a control the base does not name joins the table
// after it, so the base rows the Keymap says nothing about survive.
func TestAKeymapRowForANewControlJoinsTheTable(t *testing.T) {
	keymap := &Keymap{
		Metadata: ObjectMeta{Name: "x6"},
		Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_LEFT", Key: "KEY_ENTER"}}},
	}

	table, err := compileTable(keymap)

	mustSucceed(t, err)
	mustMatch(t, len(table), len(wantedBase)+1)
	mustMatch(t, table[len(table)-1], compiledBinding{EventType: evKey, Code: 0x110, Value: 1, Key: "KEY_ENTER"})
}

// A Keymap the operator cannot compile leaves no table at all, so the
// caller publishes nothing and the last good table stands.
func TestAKeymapThatDoesNotCompileFoldsNoTable(t *testing.T) {
	keymap := &Keymap{
		Metadata: ObjectMeta{Name: "pad"},
		Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_SOUTH", Key: "KEY_NONSENSE"}}},
	}

	table, err := compileTable(keymap)

	mustFail(t, err)
	if table != nil {
		t.Errorf("table = %+v, want none", table)
	}
	if !strings.Contains(err.Error(), "KEY_NONSENSE") {
		t.Errorf("err = %q, want it to name the key", err)
	}
}

// A key's row states the press, and the release and the autorepeat
// carry the same name, so the lookup ignores the value for a key and
// matches it exactly for a hat.
func TestTheLookupNormalisesAKeysValueAndMatchesAHatsExactly(t *testing.T) {
	cases := []struct {
		name  string
		event inputEvent
		want  string
	}{
		{name: "a mapped button going down", event: inputEvent{Type: evKey, Code: 0x130, Value: 1}, want: "KEY_ENTER"},
		{name: "the same button coming up", event: inputEvent{Type: evKey, Code: 0x130, Value: 0}, want: "KEY_ENTER"},
		{name: "the same button autorepeating", event: inputEvent{Type: evKey, Code: 0x130, Value: 2}, want: "KEY_ENTER"},
		{name: "a button no row names", event: inputEvent{Type: evKey, Code: 0x133, Value: 1}, want: ""},
		{name: "the hat one way", event: inputEvent{Type: evAbs, Code: 0x11, Value: -1}, want: "KEY_UP"},
		{name: "the hat the other way", event: inputEvent{Type: evAbs, Code: 0x11, Value: 1}, want: "KEY_DOWN"},
		{name: "the hat back at center", event: inputEvent{Type: evAbs, Code: 0x11, Value: 0}, want: ""},
		{name: "a button's code on the axis type", event: inputEvent{Type: evAbs, Code: 0x130, Value: 1}, want: ""},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			row, found := lookupKey(baseKeys, each.event)
			mustMatch(t, found, each.want != "")
			mustMatch(t, row.Key, each.want)
		})
	}
}
