package main

// A Keymap writes each binding with evdev names, and the pod that reads
// a Remote's events matches evdev numbers. This file compiles one
// Keymap's names into numeric rows, and basekeys.go merges them with
// the base mapping. The operator compiles the table before it publishes
// it, so a bad name in a Keymap is logged and reported on the Remote
// instead of crash-looping the pod.

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The defaults for an empty field of a binding's repeat block. The
// delay is long enough that a tap does not repeat.
const (
	defaultRepeatDelay    = 400 * time.Millisecond
	defaultRepeatInterval = 300 * time.Millisecond
)

// boundRemote is one Remote as the pod builders need it: its name, the
// events topic that the command sidecar reads, and the focus topic that
// decides whether the sidecar acts on those events. gatherRemotes sets
// the name, and reconcile sets the topics from the operator's topic
// prefix.
type boundRemote struct {
	Name        string
	EventsTopic string
	FocusTopic  string
}

// compileRows compiles the buttons and axes of one Keymap into rows. A
// button compiles to an EV_KEY row with value 1, the press, and the
// pod's lookup matches a key row on any value, so a release (0) and a
// repeat (2) map to the same key. An axis compiles to an EV_ABS row
// with the value of its entry, because the two directions of a hat
// arrive on one code as -1 and 1.
func compileRows(keymap *Keymap) ([]compiledBinding, error) {
	name := keymap.Metadata.Name
	var bindings []compiledBinding

	for _, button := range keymap.Spec.Buttons {
		code, known := buttonCodes[button.Press]
		if !known {
			return nil, fmt.Errorf(
				"the Keymap %s binds %s, which is not an evdev button name in the operator's key table",
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
				"the Keymap %s binds the axis %s, which is not a hat axis in the operator's key table",
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
		return nil, fmt.Errorf("the Keymap %s binds no button and no axis, and needs at least one", name)
	}
	return bindings, nil
}

// checkKey returns an error unless key is a kernel key name or
// keyNone, which drops the control. The CRD pattern checks the same
// rule, but a Keymap written before the pattern existed was never
// validated, and this is the last check before a pod reads the table.
func checkKey(keymap, entry, key string) error {
	if key == keyNone {
		return nil
	}
	if _, known := buttonCodes[key]; !known {
		return fmt.Errorf("the Keymap %s maps %s to %s, which is not an evdev key name in the operator's key table",
			keymap, entry, key)
	}
	return nil
}

// applyRepeat sets the row's repeat fields from the Keymap's repeat
// block. Without a block, the row passes on only the repeats that the
// kernel reports. With a block, the pod generates repeats while the
// control is held. The durations are converted to milliseconds here,
// so the pod parses no duration.
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

// repeatDuration parses one repeat field and returns fallback when the
// value is empty.
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

// gatherRemotes reads, with read, the Remotes that the Player lists in
// spec.remotes. It returns them in name order, because they become part
// of a pod spec, and the same objects must always produce the same spec.
// It returns an error that names a listed Remote that does not exist.
// It reads no Keymap, because the pod that reads a Remote's events
// receives its table from the Remote's keys topic.
func gatherRemotes(read func(namespace, name string) (*Remote, error), player *Player) ([]boundRemote, error) {
	namespace := player.Metadata.Namespace

	entries := make([]PlayerRemote, len(player.Spec.Remotes))
	copy(entries, player.Spec.Remotes)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	bound := make([]boundRemote, 0, len(entries))
	for _, entry := range entries {
		remote, err := read(namespace, entry.Name)
		if errors.Is(err, apiclient.ErrNotFound) {
			return nil, fmt.Errorf("the Player %s lists the Remote %s in spec.remotes, and no Remote of that name exists in this namespace",
				player.Metadata.Name, entry.Name)
		}
		if err != nil {
			return nil, err
		}
		bound = append(bound, boundRemote{Name: remote.Metadata.Name})
	}
	return bound, nil
}
