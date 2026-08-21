package main

// The compile step. A Keymap is written in names, evdev's on the
// left and the action vocabulary's on the right, and the sidecar
// matches numbers. This file turns the names into the numbered table
// in input.go and gathers the Remotes bound to one Player. The
// compile runs in the operator, before any object is created, so a
// name that means nothing fails the Play with a message instead of
// crash-looping a sidecar.

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

// A boundRemote is one Remote as the pod builders need it: the name
// that names its request and its standing pod, the device to claim,
// the compiled table the playback pod's sidecar matches events
// against, and the events topic the sidecar subscribes to. The
// operator fills EventsTopic when it builds the sidecar's environment,
// because the topic base lives with the operator and not the gather.
type boundRemote struct {
	Name        string
	Device      RemoteDevice
	Bindings    []compiledBinding
	EventsTopic string
}

// compileKeymap turns one Keymap into the sidecar's table. A button
// compiles to EV_KEY with value 1, the press alone, so a held
// button's autorepeat and its release match nothing. An axis
// compiles to EV_ABS with the value the entry states, because a
// hat's two directions arrive as -1 and 1 on one axis and are two
// separate entries here.
func compileKeymap(keymap *Keymap) ([]compiledBinding, error) {
	name := keymap.Metadata.Name
	var bindings []compiledBinding

	for _, button := range keymap.Spec.Buttons {
		code, known := buttonCodes[button.Press]
		if !known {
			return nil, fmt.Errorf(
				"the Keymap %s binds %s, which is not an evdev button name this operator knows",
				name, button.Press)
		}
		if err := checkAction(name, button.Press, button.Action, button.Amount); err != nil {
			return nil, err
		}
		binding := compiledBinding{
			EventType: evKey,
			Code:      code,
			Value:     1,
			Action:    button.Action,
			Amount:    button.Amount,
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
		if err := checkAction(name, entry, axis.Action, axis.Amount); err != nil {
			return nil, err
		}
		binding := compiledBinding{
			EventType: evAbs,
			Code:      code,
			Value:     int32(axis.Value),
			Action:    axis.Action,
			Amount:    axis.Amount,
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

// checkAction holds the amount rule: the three actions that move
// need an amount and the rest refuse one. The CRD states the same
// rule in CEL, and the compile states it again, because a resource
// can predate the rule or arrive through a server that never
// validated it, and this is the last gate before a pod exists.
func checkAction(keymap, entry, action string, amount int) error {
	switch {
	case amountActions[action]:
		if amount == 0 {
			return fmt.Errorf("the Keymap %s binds %s to %s with no amount, and %s needs one",
				keymap, entry, action, action)
		}
	case wordActions[action]:
		if amount != 0 {
			return fmt.Errorf("the Keymap %s binds %s to %s with an amount, and %s takes none",
				keymap, entry, action, action)
		}
	default:
		return fmt.Errorf("the Keymap %s binds %s to %s, which is not an action this operator knows",
			keymap, entry, action)
	}
	return nil
}

// applyRepeat folds a Keymap's repeat block into the compiled binding. A
// binding with no block fires once. A block turns repeat on, whatever the
// action is, and an empty delay or interval takes the default. The
// operator parses the durations here, so the bridge carries milliseconds
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

// gatherRemotes reads every Remote bound to one player and compiles
// each one's Keymap. The list is sorted by name because it becomes a
// pod spec, and a pod spec built twice from the same resources must
// be the same spec, request for request and container for container.
func gatherRemotes(c *Client, namespace, player string) ([]boundRemote, error) {
	list, err := ListRemotes(c, namespace)
	if err != nil {
		return nil, err
	}

	var chosen []Remote
	for _, remote := range list.Items {
		if bindsPlayer(remote, player) {
			chosen = append(chosen, remote)
		}
	}
	sort.Slice(chosen, func(first, second int) bool {
		return chosen[first].Metadata.Name < chosen[second].Metadata.Name
	})

	bound := make([]boundRemote, 0, len(chosen))
	for _, remote := range chosen {
		keymap, err := GetKeymap(c, namespace, remote.Spec.Keymap)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("the Remote %s names the Keymap %s, which does not exist in this namespace",
				remote.Metadata.Name, remote.Spec.Keymap)
		}
		if err != nil {
			return nil, err
		}
		bindings, err := compileKeymap(keymap)
		if err != nil {
			return nil, err
		}
		bound = append(bound, boundRemote{
			Name:     remote.Metadata.Name,
			Device:   remote.Spec.Device,
			Bindings: bindings,
		})
	}
	return bound, nil
}

func bindsPlayer(remote Remote, player string) bool {
	for _, binding := range remote.Spec.Bindings {
		if binding.Player == player {
			return true
		}
	}
	return false
}
