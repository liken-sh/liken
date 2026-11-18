package main

// The base key table is the set of rows that every Remote starts from,
// and a Keymap's rows replace or extend it. The kernel and udev's hwdb
// already turn a device's scancodes into KEY_* codes such as
// KEY_VOLUMEUP, and a KEY_* code with no row is sent as itself, so a
// Remote with no Keymap already works. The base adds rows only for
// controls that have no KEY_* code. The hat axes become the arrow keys,
// because a d-pad reports an axis and not a button, and the south and
// east face buttons become enter and back, as they are on game consoles.

import "fmt"

// The kernel does not autorepeat a gamepad's controls, so the Remote's
// pod generates the repeat for a held hat direction. The delays of 400ms
// and then 250ms match keyboard arrow keys.
var baseHatRepeat = KeymapRepeat{Delay: "400ms", Interval: "250ms"}

// baseKeymap is the base table written as a Keymap, so it compiles
// through the same compileRows as a Keymap from the cluster.
var baseKeymap = &Keymap{
	Metadata: ObjectMeta{Name: "base"},
	Spec: KeymapSpec{
		Buttons: []KeymapButton{
			{Press: "BTN_SOUTH", Key: "KEY_ENTER"},
			{Press: "BTN_EAST", Key: "KEY_BACK"},
		},
		Axes: []KeymapAxis{
			{Axis: "ABS_HAT0Y", Value: -1, Key: "KEY_UP", Repeat: &baseHatRepeat},
			{Axis: "ABS_HAT0Y", Value: 1, Key: "KEY_DOWN", Repeat: &baseHatRepeat},
			{Axis: "ABS_HAT0X", Value: -1, Key: "KEY_LEFT", Repeat: &baseHatRepeat},
			{Axis: "ABS_HAT0X", Value: 1, Key: "KEY_RIGHT", Repeat: &baseHatRepeat},
		},
	},
}

// baseKeys is the compiled base table. The operator panics at start if
// it does not compile, because the rows are part of the binary, so a
// failure is a mistake in this file and not in a cluster's object.
var baseKeys = mustCompileBase()

func mustCompileBase() []compiledBinding {
	rows, err := compileRows(baseKeymap)
	if err != nil {
		panic(fmt.Sprintf("the base key table does not compile: %v", err))
	}
	return rows
}

// compileTable returns the base rows with the Keymap's rows applied
// over them. A Keymap row replaces the base row for the same control,
// and any other Keymap row is appended. A nil Keymap returns the base
// alone. The row order is deterministic, because the operator compares
// the payload it would publish with the last one it published.
func compileTable(keymap *Keymap) ([]compiledBinding, error) {
	table := make([]compiledBinding, len(baseKeys))
	copy(table, baseKeys)
	if keymap == nil {
		return table, nil
	}
	rows, err := compileRows(keymap)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if index := indexOfControl(table, row); index >= 0 {
			table[index] = row
			continue
		}
		table = append(table, row)
	}
	return table, nil
}

// indexOfControl returns the index of the row in table for the same
// control as row, or -1. A control is identified by event type, code,
// and value. A button row always has value 1, and an axis row has the
// direction, so the two directions of a hat are two controls.
func indexOfControl(table []compiledBinding, row compiledBinding) int {
	for index, held := range table {
		if held.EventType == row.EventType && held.Code == row.Code && held.Value == row.Value {
			return index
		}
	}
	return -1
}

// lookupKey returns the row that matches an input event. The Remote's
// pod calls it for every event. A key event matches its row whatever
// its value, so the press, the release, and the autorepeat of a key all
// return the same row. An axis event must match the row's value
// exactly, because each direction of a hat has its own row.
func lookupKey(table []compiledBinding, event inputEvent) (compiledBinding, bool) {
	value := event.Value
	if event.Type == evKey {
		value = 1
	}
	for _, row := range table {
		if row.EventType == event.Type && row.Code == event.Code && row.Value == value {
			return row, true
		}
	}
	return compiledBinding{}, false
}
