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

// A boundRemote is one Remote as the pod builders need it: the name that
// names its translator sidecar, the resolved Keymap name, and the three
// topics the translator subscribes to. The Keymap name is the Player
// entry's per-unit override when it sets one, and the Remote's own Keymap
// otherwise, so one controller can map two ways on two units. The gather
// sets Name and Keymap; the operator fills the three topics in reconcile.
type boundRemote struct {
	Name        string
	Keymap      string
	EventsTopic string
	KeymapTopic string
	FocusTopic  string
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
// operator parses the durations here, so the translator carries
// milliseconds and parses nothing.
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

// gatherRemotes reads every Remote a Player owns. It reads spec.remotes
// in name order, because the result becomes a pod spec, and a pod spec
// built twice from the same resources must be the same spec, container
// for container. Each unit's keymap is the entry's own override when it
// sets one, and the Remote's base keymap otherwise, so one controller can
// map one way on one unit and another way on another. A named Remote that
// does not exist fails the gather, and the message names it. The Keymap
// itself is not read here: the operator compiles and publishes it in the
// keymap reconcile, and the translator reads the table off the bus.
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
		keymap := entry.Keymap
		if keymap == "" {
			keymap = remote.Spec.Keymap
		}
		bound = append(bound, boundRemote{
			Name:   remote.Metadata.Name,
			Keymap: keymap,
		})
	}
	return bound, nil
}
