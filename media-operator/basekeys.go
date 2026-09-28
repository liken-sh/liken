package main

// The base table, the layer under every Keymap. Linux splits input in
// two: the kernel and udev's hwdb normalise a device, so a scancode
// becomes KEY_VOLUMEUP, and each program binds those names to what
// they mean there. The base is this operator's half of the first
// layer. Every KEY_* code passes as itself with no row, so a Remote
// with no Keymap already works. The base binds only what the kernel
// cannot name as a key: the hat axes become the arrows, because a
// d-pad is not a button, and the south and east face buttons become
// enter and back, because every console reads them that way. Nothing
// else in the base binds a button.

import "fmt"

// The hat repeat cadence. A gamepad never autorepeats in the kernel,
// so the standing pod synthesises the repeat, and 400ms then 250ms is
// the cadence a person expects from a keyboard's arrows.
var baseHatRepeat = KeymapRepeat{Delay: "400ms", Interval: "250ms"}

// The base itself, written as a Keymap so it compiles through the
// same path a cluster's Keymap does. This is the one place the
// project states what a controller means before anybody writes a
// row.
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

// baseKeys is the compiled base. A failure here panics at start,
// because the rows are compiled into the binary: a name this build
// cannot resolve is a fault in this file and not in a cluster's
// object.
var baseKeys = mustCompileBase()

func mustCompileBase() []compiledBinding {
	rows, err := compileRows(baseKeymap)
	if err != nil {
		panic(fmt.Sprintf("the base key table does not compile: %v", err))
	}
	return rows
}

// compileTable is the whole fold: the base first, then the Remote's
// Keymap over it, one row replacing the base row for the same
// control. A Remote with no Keymap gets the base alone. The order is
// stable, because the operator compares the published payload against
// the last one it wrote.
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

// indexOfControl finds the row that names the same control. A button
// row always carries value 1, and an axis row carries the direction,
// so the three fields together are the control's identity.
func indexOfControl(table []compiledBinding, row compiledBinding) int {
	for index, held := range table {
		if held.EventType == row.EventType && held.Code == row.Code && held.Value == row.Value {
			return index
		}
	}
	return -1
}

// lookupKey is the match the standing pod runs on every event. The
// value is normalised for a key and exact for an axis: a key's row
// states the press, and the release and the autorepeat carry the same
// meaning under the same name, while a hat's two directions are two
// rows on one code.
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
