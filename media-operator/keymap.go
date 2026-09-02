package main

// The compile step. A Keymap is written in names, evdev's on both
// sides, and the standing pod matches numbers. This file turns one
// Keymap's names into rows, and basekeys.go folds them over the base.
// The compile runs in the operator, before any pod reads the table,
// so a name that means nothing is logged instead of crash-looping a
// pod.

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// The repeat defaults, applied when a binding sets a repeat block but
// leaves a field empty. The delay is long enough that a tap does not
// repeat; the interval is a middling rate a keymap tunes per binding.
const (
	defaultRepeatDelay    = 400 * time.Millisecond
	defaultRepeatInterval = 300 * time.Millisecond
)

// A boundRemote is one Remote as the pod builders need it: its name,
// the events topic the command sidecar reads, and the focus topic it
// gates on. The gather sets the name, and the operator fills the two
// topics in reconcile, because the topic base lives with the
// operator.
type boundRemote struct {
	Name        string
	EventsTopic string
	FocusTopic  string
}

// compileRows turns one Keymap into its own rows, before the fold
// over the base. A button compiles to EV_KEY with value 1, the press,
// because the release and the autorepeat carry the same name. An axis
// compiles to EV_ABS with the value the entry states, because a hat's
// two directions arrive as -1 and 1 on one code.
func compileRows(keymap *Keymap) ([]compiledBinding, error) {
	name := keymap.Metadata.Name
	var bindings []compiledBinding

	for _, button := range keymap.Spec.Buttons {
		code, known := buttonCodes[button.Press]
		if !known {
			return nil, fmt.Errorf(
				"the Keymap %s binds %s, which is not an evdev button name this operator knows",
				name, button.Press)
		}
		if err := checkKey(name, button.Press, button.Key); err != nil {
			return nil, err
		}
		binding := compiledBinding{
			EventType: evKey,
			Code:      code,
			Value:     1,
			Key:       button.Key,
		}
		if err := applyRepeat(&binding, name, button.Press, button.Repeat); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}

	for _, axis := range keymap.Spec.Axes {
		code, known := axisCodes[axis.Axis]
		if !known {
			return nil, fmt.Errorf(
				"the Keymap %s binds the axis %s, which is not one of the hat axes this operator knows",
				name, axis.Axis)
		}
		entry := fmt.Sprintf("%s %d", axis.Axis, axis.Value)
		if err := checkKey(name, entry, axis.Key); err != nil {
			return nil, err
		}
		binding := compiledBinding{
			EventType: evAbs,
			Code:      code,
			Value:     int32(axis.Value),
			Key:       axis.Key,
		}
		if err := applyRepeat(&binding, name, entry, axis.Repeat); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}

	if len(bindings) == 0 {
		return nil, fmt.Errorf("the Keymap %s binds nothing; it needs at least one button or axis", name)
	}
	return bindings, nil
}

// checkKey holds the right side to the kernel's own names, plus none,
// which drops the control. The CRD pattern states the shape, and the
// compile states the rule again, because a resource can predate the
// rule or arrive through a server that never validated it, and this
// is the last gate before a pod reads the table.
func checkKey(keymap, entry, key string) error {
	if key == keyNone {
		return nil
	}
	if _, known := buttonCodes[key]; !known {
		return fmt.Errorf("the Keymap %s maps %s to %s, which is not an evdev key name this operator knows",
			keymap, entry, key)
	}
	return nil
}

// applyRepeat folds a Keymap's repeat block into the row. A row with
// no block reports only what the kernel reports. A block turns
// synthesis on, and an empty delay or interval takes the default. The
// operator parses the durations here, so the pod carries milliseconds
// and parses nothing.
func applyRepeat(binding *compiledBinding, keymap, entry string, repeat *KeymapRepeat) error {
	if repeat == nil {
		return nil
	}
	delay, err := repeatDuration(keymap, entry, "delay", repeat.Delay, defaultRepeatDelay)
	if err != nil {
		return err
	}
	interval, err := repeatDuration(keymap, entry, "interval", repeat.Interval, defaultRepeatInterval)
	if err != nil {
		return err
	}
	binding.RepeatDelay = int(delay / time.Millisecond)
	binding.RepeatInterval = int(interval / time.Millisecond)
	return nil
}

// repeatDuration reads one repeat field, an empty value taking the
// default. The compile is the gate a bad duration hits, so a Keymap with
// a value like "soon" fails the Play with a message instead of reaching a
// pod that cannot parse it.
func repeatDuration(keymap, entry, field, value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("the Keymap %s sets the repeat %s on %s to %q, which is not a duration like 400ms",
			keymap, field, entry, value)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("the Keymap %s sets the repeat %s on %s to %q, which is not a positive duration",
			keymap, field, entry, value)
	}
	return parsed, nil
}

// gatherRemotes reads every Remote a Player owns. It reads
// spec.remotes in name order, because the result becomes a pod spec,
// and a pod spec built twice from the same resources must be the same
// spec. A named Remote that does not exist fails the gather, and the
// message names it. The table is the standing pod's business,
// published on the Remote's own keys topic, so nothing here reads a
// Keymap.
func gatherRemotes(c *Client, player *Player) ([]boundRemote, error) {
	namespace := player.Metadata.Namespace

	entries := make([]PlayerRemote, len(player.Spec.Remotes))
	copy(entries, player.Spec.Remotes)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	bound := make([]boundRemote, 0, len(entries))
	for _, entry := range entries {
		remote, err := GetRemote(c, namespace, entry.Name)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("the Player %s names the Remote %s, which does not exist in this namespace",
				player.Metadata.Name, entry.Name)
		}
		if err != nil {
			return nil, err
		}
		bound = append(bound, boundRemote{Name: remote.Metadata.Name})
	}
	return bound, nil
}
