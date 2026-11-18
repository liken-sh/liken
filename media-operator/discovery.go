package main

// Discovery mode prints each input event with the Keymap entry that
// would bind it, ready to paste, so a person can write a Keymap. When no
// entry can bind the event, the line says why.

import "fmt"

// keymapKeyHint is the placeholder that the person replaces with the
// KEY_* name the control should report, or with none to drop the control.
const keymapKeyHint = "<a KEY_* name, or none>"

// discoveryLines returns the event's fields, then its Keymap entry
// indented to paste under spec.buttons or spec.axes. It returns nil for
// an event that is not EV_KEY or EV_ABS.
func discoveryLines(node string, event inputEvent) []string {
	switch event.Type {
	case evKey:
		return keyLines(node, event)
	case evAbs:
		return axisLines(node, event)
	}
	return nil
}

// Only a press gets a Keymap entry, because a Keymap binds presses.
// Release and repeat lines say so, so that a stream of repeat events
// does not look like a fault.
func keyLines(node string, event inputEvent) []string {
	name := keyCodeNames[event.Code]
	line := fmt.Sprintf("%s %s %s %s", node, eventTypeField(event.Type),
		codeField(name, event.Code), keyValueField(event.Value))
	switch {
	case name == "":
		return []string{line + ": the kernel gives this code no name, so a Keymap cannot bind it"}
	case event.Value != 1:
		return []string{line + ": a Keymap binds the press alone"}
	}
	return []string{
		line,
		fmt.Sprintf("  - press: %s   # code %d", name, event.Code),
		"    key: " + keymapKeyHint,
	}
}

// A Keymap binds only the hat axes, at the values -1 and 1, and treats 0
// as the release.
func axisLines(node string, event inputEvent) []string {
	name := axisCodeNames[event.Code]
	line := fmt.Sprintf("%s %s %s %d", node, eventTypeField(event.Type),
		codeField(name, event.Code), event.Value)
	switch {
	case name == "":
		return []string{line + ": a Keymap binds the two hat axes alone"}
	case event.Value == 0:
		return []string{line + ": a Keymap binds -1 and 1, not the return to the middle"}
	}
	return []string{
		line,
		fmt.Sprintf("  - axis: %s   # code %d", name, event.Code),
		fmt.Sprintf("    value: %d", event.Value),
		"    key: " + keymapKeyHint,
	}
}

func eventTypeField(eventType uint16) string {
	name := "EV_KEY"
	if eventType == evAbs {
		name = "EV_ABS"
	}
	return fmt.Sprintf("%s (%d)", name, eventType)
}

func codeField(name string, code uint16) string {
	if name == "" {
		return fmt.Sprintf("%d", code)
	}
	return fmt.Sprintf("%s (%d)", name, code)
}

func keyValueField(value int32) string {
	switch value {
	case 0:
		return "release (0)"
	case 1:
		return "press (1)"
	case 2:
		return "repeat (2)"
	}
	return fmt.Sprintf("(%d)", value)
}
