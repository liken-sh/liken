package main

import (
	"reflect"
	"strings"
	"testing"
)

// wantedBase is the compiled base table, written out in full so that a
// change to the base fails a test.
var wantedBase = []compiledBinding{
	{EventType: evKey, Code: 0x130, Value: 1, Key: "KEY_ENTER"},
	{EventType: evKey, Code: 0x131, Value: 1, Key: "KEY_BACK"},
	{EventType: evAbs, Code: 0x11, Value: -1, Key: "KEY_UP", RepeatDelay: 400, RepeatInterval: 250},
	{EventType: evAbs, Code: 0x11, Value: 1, Key: "KEY_DOWN", RepeatDelay: 400, RepeatInterval: 250},
	{EventType: evAbs, Code: 0x10, Value: -1, Key: "KEY_LEFT", RepeatDelay: 400, RepeatInterval: 250},
	{EventType: evAbs, Code: 0x10, Value: 1, Key: "KEY_RIGHT", RepeatDelay: 400, RepeatInterval: 250},
}

func TestARemoteWithNoKeymapGetsTheBaseTable(t *testing.T) {
	table, err := compileTable(nil)

	mustSucceed(t, err)
	if !reflect.DeepEqual(table, wantedBase) {
		t.Errorf("table = %+v, want %+v", table, wantedBase)
	}
}

func TestTheBaseBindsTheHatsAndTheSouthAndEastButtons(t *testing.T) {
	if !reflect.DeepEqual(baseKeys, wantedBase) {
		t.Errorf("base = %+v, want %+v", baseKeys, wantedBase)
	}
}

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

func TestAKeymapRowForANewControlIsAppended(t *testing.T) {
	keymap := &Keymap{
		Metadata: ObjectMeta{Name: "x6"},
		Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_LEFT", Key: "KEY_ENTER"}}},
	}

	table, err := compileTable(keymap)

	mustSucceed(t, err)
	mustMatch(t, len(table), len(wantedBase)+1)
	mustMatch(t, table[len(table)-1], compiledBinding{EventType: evKey, Code: 0x110, Value: 1, Key: "KEY_ENTER"})
}

func TestAKeymapThatDoesNotCompileReturnsNoTable(t *testing.T) {
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
		t.Errorf("the error is %q, want it to name KEY_NONSENSE", err)
	}
}

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
