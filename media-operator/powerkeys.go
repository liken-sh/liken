package main

// A power key on a remote writes spec.power of the Player that the
// controller has focus on. The next pass switches the hardware to match
// (playerpower.go). A person with kubectl writes the same field, and the
// last write wins.

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

// toggleKeys are the two names that remotes send for a power button. A
// press switches the Player to the opposite power state.
var toggleKeys = []string{"KEY_POWER", "KEY_POWER2"}

// powerOffKey and powerOnKey are the names that the kernel's rc-cec
// keymap gives to a TV remote's Power Off Function and Power On
// Function (HDMI-CEC 1.3a, section 13.13.3). Each sets a fixed state,
// so a second press changes nothing. POWER in
// media-screen/src/screen/keys.rs lists the same four power keys for
// the Rust screen client. Change both together.
const (
	powerOffKey = "KEY_SLEEP"
	powerOnKey  = "KEY_WAKEUP"
)

// The values of spec.power. An absent field means On.
const (
	playerPowerOn  = "On"
	playerPowerOff = "Off"
)

// powerFieldManager is the server-side apply field manager for
// spec.power alone. An apply removes each field that its manager owned
// and the new body omits, so the operator's other applies, and a GitOps
// copy of the Player that omits spec.power, leave the field alone.
const powerFieldManager = "media-operator-power"

// powerKey reports whether key is one of the four power keys.
func powerKey(key string) bool {
	return slices.Contains(toggleKeys, key) || key == powerOffKey || key == powerOnKey
}

// specPower returns the Player's spec.power, with an absent field as On.
func specPower(player *Player) string {
	if player.Spec.Power == playerPowerOff {
		return playerPowerOff
	}
	return playerPowerOn
}

// pressedPower returns the spec.power that a press of key sets on a
// Player whose power is now, and whether it differs from now. Any key
// that is not a power key turns on a Player that is off, so any button
// of the remote turns the screen and speakers on.
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

// pressPower applies the spec.power that a press sets, and returns
// true when the press turned the Player on. The caller then ignores the
// press, because turning the Player on already selects its input, and
// a volume key must not change the level of a Player that was off. A
// key repeat or release changes nothing, so a held power key toggles
// the power once.
func (o *operator) pressPower(namespace, controller string, payload []byte) bool {
	key, pressed := pressOf(payload)
	if !pressed {
		return false
	}
	player, focused := o.focusedPlayer(namespace, controller)
	if focused == "" {
		return false
	}
	trigger := fmt.Sprintf("remote %s: %s pressed with focus on player %s",
		controllerKey(namespace, controller), key, player)
	// The controller can have focus on a Player that was deleted.
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

// playerPowerApply is the apply body: the Player's identity and
// spec.power, and no other field.
type playerPowerApply struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   ObjectMeta      `json:"metadata"`
	Spec       playerPowerSpec `json:"spec"`
}

type playerPowerSpec struct {
	Power string `json:"power"`
}

// ApplyPlayerPower applies one Player's spec.power under
// powerFieldManager, with force, so it takes the field from any other
// manager. It records the resource version that the API server
// returns, so the next press toggles from this write and not from a
// cached copy that the watch has not refreshed yet.
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
