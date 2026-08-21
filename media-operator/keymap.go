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
		bindings = append(bindings, compiledBinding{
			EventType: evKey,
			Code:      code,
			Value:     1,
			Action:    button.Action,
			Amount:    button.Amount,
		})
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
		bindings = append(bindings, compiledBinding{
			EventType: evAbs,
			Code:      code,
			Value:     int32(axis.Value),
			Action:    axis.Action,
			Amount:    axis.Amount,
		})
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
