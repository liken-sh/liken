package main

// The power keys: a press on a controller whose mark names a unit
// writes the unit's spec.power, by server-side apply under a field
// manager that owns that one field. The pass then turns the hardware
// off or on (playerpower_test.go).

import (
	"testing"
)

// The spec.power each press sets, from each state. A power key acts on
// the press alone, and any other key turns a Player that is off on.
func TestEachPressSetsThePower(t *testing.T) {
	cases := []struct {
		key   string
		from  string
		want  string
		moves bool
	}{
		{key: "KEY_POWER", from: playerPowerOn, want: playerPowerOff, moves: true},
		{key: "KEY_POWER", from: playerPowerOff, want: playerPowerOn, moves: true},
		{key: "KEY_POWER2", from: playerPowerOn, want: playerPowerOff, moves: true},
		{key: "KEY_POWER2", from: playerPowerOff, want: playerPowerOn, moves: true},
		{key: "KEY_SLEEP", from: playerPowerOn, want: playerPowerOff, moves: true},
		{key: "KEY_SLEEP", from: playerPowerOff, want: playerPowerOff},
		{key: "KEY_WAKEUP", from: playerPowerOff, want: playerPowerOn, moves: true},
		{key: "KEY_WAKEUP", from: playerPowerOn, want: playerPowerOn},
		{key: "KEY_UP", from: playerPowerOff, want: playerPowerOn, moves: true},
		{key: "KEY_VOLUMEUP", from: playerPowerOff, want: playerPowerOn, moves: true},
		{key: "KEY_UP", from: playerPowerOn, want: playerPowerOn},
	}
	for _, each := range cases {
		t.Run(each.key+" from "+each.from, func(t *testing.T) {
			got, moves := pressedPower(each.key, each.from)

			mustMatch(t, got, each.want)
			mustMatch(t, moves, each.moves)
		})
	}
}

// powerOperator is askOperator with the house theater in the fake
// cluster at the given power, so a press reads the Player from the view.
func powerOperator(t *testing.T, power string) (*operator, *fakeCluster) {
	t.Helper()
	media, cluster := askOperator(t)
	cluster.players["theater"].Spec.Power = power
	return media, cluster
}

// A press that moves the power applies spec.power alone, under the
// operator's power field manager, so a GitOps copy of the Player that
// leaves the field out never takes it back.
func TestAPowerPressAppliesSpecPowerUnderItsOwnManager(t *testing.T) {
	media, cluster := powerOperator(t, "")

	press(media, "KEY_POWER", 1)

	mustMatch(t, len(cluster.playerApplies), 1)
	mustMatch(t, cluster.playerApplies[0].manager, powerFieldManager)
	mustMatch(t, cluster.playerApplies[0].force, true)
	mustMatch(t, cluster.playerApplies[0].fields, `{"power":"Off"}`)
	mustMatch(t, cluster.players["theater"].Spec.Power, playerPowerOff)
}

// A press that does not move the power writes nothing: a repeat, a
// release, a press from a controller pointed at another unit, and a
// deterministic key on a Player already in its state.
func TestAPressThatMovesNoPowerWritesNothing(t *testing.T) {
	cases := []struct {
		name  string
		power string
		key   string
		value int
		mark  string
	}{
		{name: "a repeat of power", key: "KEY_POWER", value: 2},
		{name: "a release of power", key: "KEY_POWER", value: 0},
		{name: "power off on a Player that is off", power: playerPowerOff, key: "KEY_SLEEP", value: 1},
		{name: "power on on a Player that is on", key: "KEY_WAKEUP", value: 1},
		{name: "any key on a Player that is on", key: "KEY_UP", value: 1},
		{name: "a repeat on a Player that is off", power: playerPowerOff, key: "KEY_UP", value: 2},
		{name: "a mark on another unit", key: "KEY_POWER", value: 1, mark: "studio"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, cluster := powerOperator(t, each.power)
			if each.mark != "" {
				media.focus.setMark(controllerKey("house", "sofa"), each.mark)
			}

			press(media, each.key, each.value)

			mustMatch(t, len(cluster.playerApplies), 0)
		})
	}
}

// A press that turns the Player on does nothing else. The wake that
// follows claims the input, so the press writes no input ask, and a
// volume key moves no level on a room that is off.
func TestThePressThatTurnsThePlayerOnAsksNothingElse(t *testing.T) {
	for _, key := range []string{"KEY_UP", "KEY_HOMEPAGE", "KEY_VOLUMEUP"} {
		t.Run(key, func(t *testing.T) {
			media, cluster := powerOperator(t, playerPowerOff)

			press(media, key, 1)

			mustMatch(t, cluster.players["theater"].Spec.Power, playerPowerOn)
			mustMatch(t, lastInputAsk(cluster), "none")
			mustMatch(t, len(cluster.sessions), 0)
		})
	}
}

// A Player the view cannot read takes no write, and the press asks
// for its input as it would with no power at all.
func TestAPressOnAPlayerTheViewCannotReadWritesNoPower(t *testing.T) {
	media, cluster := powerOperator(t, playerPowerOff)
	delete(cluster.players, "theater")

	press(media, "KEY_UP", 1)

	mustMatch(t, len(cluster.playerApplies), 0)
	mustMatch(t, lastInputAsk(cluster), inputEnsure)
}

// An apply the API server refuses moves nothing, and the press does
// the rest of what it does.
func TestARefusedPowerApplyMovesNothing(t *testing.T) {
	media, cluster := powerOperator(t, playerPowerOff)
	cluster.playerApplyFails = true

	press(media, "KEY_UP", 1)

	mustMatch(t, cluster.players["theater"].Spec.Power, playerPowerOff)
	mustMatch(t, lastInputAsk(cluster), inputEnsure)
}
