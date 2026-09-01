package main

// These tests cover the panel desk and the fold: the desire
// the idle command pod published reaches the desk, and the Display's
// observed values become the word the Player's status carries.

import "testing"

// A cleared retained value is not a live signal, so the desk
// holds the desire it had.
func TestAClearedPanelTopicLeavesTheDesireAlone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	topic := playerPanelTopic(defaultTopicBase, "house", "theater")

	media.handleBusMessage(topic, []byte(`{"desire":"off"}`))
	media.handleBusMessage(topic, nil)
	media.handleBusMessage(topic, []byte("not json"))

	mustMatch(t, media.panels.stateFor(playerKey("house", "theater")), panelDesireOff)
}

// The desk wakes the loop on a state it did not hold and on a
// change, and not when the retained topic delivers the same message
// again.
func TestThePanelDeskWakesOnlyOnChange(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newPanelDesk(wake)
	key := playerKey("house", "theater")

	desk.setState(key, panelDesireOff)
	mustMatch(t, len(wake), 1)

	<-wake
	desk.setState(key, panelDesireOff)
	mustMatch(t, len(wake), 0)

	desk.setState(key, panelDesireOn)
	mustMatch(t, len(wake), 1)
}

// The desk shrinks to the units the cluster still holds, so a
// long-running operator keeps no key for a deleted Player.
func TestThePanelDeskDropsADeletedPlayer(t *testing.T) {
	desk := newPanelDesk(make(chan struct{}, 1))
	desk.setState(playerKey("house", "theater"), panelDesireOff)
	desk.setState(playerKey("house", "studio"), panelDesireOn)

	desk.retain(map[string]bool{playerKey("house", "studio"): true})

	mustMatch(t, desk.stateFor(playerKey("house", "theater")), "")
	mustMatch(t, desk.stateFor(playerKey("house", "studio")), panelDesireOn)
}

// The display-operator observes five power words, and its
// override writes whichever of off, hardOff, and standby the panel
// declares. Every word but on is a panel held down, so every one of
// them reads Off.
func TestThePanelWordReadsEveryPowerDownWord(t *testing.T) {
	cases := []struct {
		power string
		want  string
	}{
		{power: "on", want: panelOn},
		{power: "standby", want: panelOff},
		{power: "suspend", want: panelOff},
		{power: "off", want: panelOff},
		{power: "hardOff", want: panelOff},
	}
	for _, one := range cases {
		t.Run(one.power, func(t *testing.T) {
			observed := DisplayObserved{Brightness: ptr(70), Power: one.power}
			mustMatch(t, panelFromDisplay(observed), one.want)
		})
	}
}

// The status word is folded from what the display-operator
// last observed, so a panel a person turned down by hand still reads
// On, and a Display that observed nothing carries no word at all.
func TestThePanelWordFoldsTheObservedState(t *testing.T) {
	cases := []struct {
		name     string
		observed DisplayObserved
		want     string
	}{
		{name: "a lit panel", observed: DisplayObserved{Brightness: ptr(70), Power: "on"}, want: panelOn},
		{name: "the backlight at zero",
			observed: DisplayObserved{Brightness: ptr(0), Power: "on"}, want: panelBacklightOff},
		{name: "the power off",
			observed: DisplayObserved{Brightness: ptr(0), Power: "off"}, want: panelOff},
		{name: "the power off with the backlight up",
			observed: DisplayObserved{Brightness: ptr(70), Power: "off"}, want: panelOff},
		{name: "a panel with a brightness alone",
			observed: DisplayObserved{Brightness: ptr(35)}, want: panelOn},
		{name: "nothing observed yet"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, panelFromDisplay(one.observed), one.want)
		})
	}
}
