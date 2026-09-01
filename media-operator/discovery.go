package main

// Discovery is the reader's teaching mode. This file renders what one
// event reports: the node it came from, the event type, the code by
// its evdev name, the value as a press, a release, or a repeat, and
// the Keymap entry that would bind it, ready to paste. Where no entry
// can bind it, the line states why instead.

import (
	"fmt"
	"sort"
	"strings"
)

// The action vocabulary in one sorted line, so the fragment a person
// pastes carries the words the Keymap schema accepts.
var keymapActions = actionVocabulary()

func actionVocabulary() string {
	actions := make([]string, 0, len(amountActions)+len(wordActions))
	for action := range amountActions {
		actions = append(actions, action)
	}
	for action := range wordActions {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return strings.Join(actions, ", ")
}

// discoveryLines renders one event: the fields on the first line, and
// under them the Keymap entry that binds it, indented to paste under
// spec.buttons or spec.axes.
func discoveryLines(node string, event inputEvent) []string {
	switch event.Type {
	case evKey:
		return keyLines(node, event)
	case evAbs:
		return axisLines(node, event)
	}
	return nil
}

// keyLines names a key event's code and value. Only the press earns a
// fragment, because a Keymap binds the press alone; the release and
// the repeat lines state that instead, so a stream of value 2 does not
// read as a bug.
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
		fmt.Sprintf("    action: <one of %s>", keymapActions),
	}
}

// axisLines names a hat event's code and direction. The two directions
// earn a fragment; the return to the middle does not, because a Keymap
// binds -1 and 1 and treats 0 as the release.
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
		fmt.Sprintf("    action: <one of %s>", keymapActions),
	}
}

// eventTypeField gives the type by the kernel's own name, with the
// number beside it.
func eventTypeField(eventType uint16) string {
	name := "EV_KEY"
	if eventType == evAbs {
		name = "EV_ABS"
	}
	return fmt.Sprintf("%s (%d)", name, eventType)
}

// codeField gives the code by name and number, or the number alone
// when the kernel gives the code no name.
func codeField(name string, code uint16) string {
	if name == "" {
		return fmt.Sprintf("%d", code)
	}
	return fmt.Sprintf("%s (%d)", name, code)
}

// keyValueField names a key's three values for what a person did.
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
