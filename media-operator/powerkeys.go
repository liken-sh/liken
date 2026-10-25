package main

// The power keys. A person turns a Player off and on with the remote,
// so this operator reads each press off the controller's events topic,
// gated by the same focus mark as the input asks (ensure.go), and
// writes the unit's spec.power. A pass then turns the hardware to
// follow it (playerpower.go). kubectl writes the same field, and the
// last write wins.
//
// The press writes spec.power by server-side apply under a field
// manager that owns that one field. A GitOps copy of the Player that
// leaves the field out then never removes it or takes it back, and an
// apply that states the field again forces it away from kubectl, which
// is what the press means.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// toggleKeys are the two power synonyms a shell and a Bluetooth remote
// send. Each press turns the Player off when it is on and on when it is
// off.
var toggleKeys = []string{"KEY_POWER", "KEY_POWER2"}

// powerOffKey and powerOnKey are the names the kernel's rc-cec keymap
// gives a TV remote's Power Off Function and Power On Function. Each
// names the state it wants (HDMI-CEC 1.3a, CEC 13.13.3), so a second
// press leaves the Player in that state. The Rust crate media-screen
// holds the four power names as POWER in
// media-screen/src/screen/keys.rs, because a Go list cannot reach it:
// a change to one changes both.
const (
	powerOffKey = "KEY_SLEEP"
	powerOnKey  = "KEY_WAKEUP"
)

// The two values of spec.power. An absent field is On.
const (
	playerPowerOn  = "On"
	playerPowerOff = "Off"
)

// powerFieldManager owns spec.power and no other field. It is not the
// manager of the operator's other applies, because an apply removes
// each field its manager owned and did not state again.
const powerFieldManager = "media-operator-power"

// powerKey answers whether one key name is a power key.
func powerKey(key string) bool {
	return slices.Contains(toggleKeys, key) || key == powerOffKey || key == powerOnKey
}

// specPower reads a Player's spec.power, with an absent field as On.
func specPower(player *Player) string {
	if player.Spec.Power == playerPowerOff {
		return playerPowerOff
	}
	return playerPowerOn
}

// pressedPower answers the spec.power one press sets on a Player whose
// power is now, and whether that moves it. Any key that is not a power
// key turns a Player that is off on, so a person gets the room back
// with whatever control they touched.
func pressedPower(key, now string) (string, bool) {
	switch {
	case slices.Contains(toggleKeys, key) && now == playerPowerOn:
		return playerPowerOff, true
	case slices.Contains(toggleKeys, key):
		return playerPowerOn, true
	case key == powerOffKey:
		return playerPowerOff, now != playerPowerOff
	case key == powerOnKey, now == playerPowerOff:
		return playerPowerOn, now == playerPowerOff
	}
	return now, false
}

// pressPower writes the spec.power one press sets, and answers whether
// the press turned the Player on. A press that turns the Player on does
// nothing else: the wake that follows selects the unit's input, and a
// room that was off takes no volume step. A power key acts on the press
// alone and never on a repeat, because a held key would turn the room
// off and on under the hand.
func (o *operator) pressPower(namespace, controller string, payload []byte) bool {
	key, pressed := pressOf(payload)
	if !pressed {
		return false
	}
	player, unit := o.focusedUnit(namespace, controller)
	if unit == "" {
		return false
	}
	trigger := fmt.Sprintf("remote %s: %s pressed with focus on player %s",
		controllerKey(namespace, controller), key, player)
	// A mark can name a Player that is gone, and that press has no power
	// to move.
	held, err := o.view.Player(o.client, namespace, player)
	if errors.Is(err, apiclient.ErrNotFound) {
		return false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s, reading the player's power: %v\n", trigger, err)
		return false
	}
	now := specPower(held)
	next, moves := pressedPower(key, now)
	if !moves {
		return false
	}
	if err := ApplyPlayerPower(o.client, o.view.players.Versions, namespace, player, next); err != nil {
		fmt.Fprintf(os.Stderr, "%s, applying spec.power %s: %v\n", trigger, next, err)
		return false
	}
	logLine(o.log, "%s, applied spec.power %s, was %s", trigger, next, now)
	return next == playerPowerOn
}

// playerPowerApply is the body of the apply: the Player's identity and
// spec.power, and nothing else.
type playerPowerApply struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   ObjectMeta      `json:"metadata"`
	Spec       playerPowerSpec `json:"spec"`
}

type playerPowerSpec struct {
	Power string `json:"power"`
}

// ApplyPlayerPower writes one Player's spec.power under the power field
// manager, and notes the version of the copy the API server answered,
// so the next press toggles from this write and not from a store copy
// the watch has not updated yet.
func ApplyPlayerPower(c *apiclient.Client, versions *memo.Versions, namespace, name, power string) error {
	body, err := json.Marshal(&playerPowerApply{
		APIVersion: mediaAPIVersion,
		Kind:       "Player",
		Metadata:   ObjectMeta{Name: name, Namespace: namespace},
		Spec:       playerPowerSpec{Power: power},
	})
	if err != nil {
		return err
	}
	path := playerPath(namespace, name) + "?fieldManager=" + powerFieldManager + "&force=true"
	_, err = memo.Written[Player](versions, namespacedKey(namespace, name), func() (*Player, error) {
		answer := &Player{}
		return answer, c.Request(http.MethodPatch, path, applyContentType, body, answer)
	})
	return err
}
