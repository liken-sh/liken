package main

import "testing"

// An empty payload clears a retained message and is not a new desire.
func TestAClearedPanelTopicLeavesTheDesireAlone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	topic := playerPanelTopic(defaultTopicBase, "house", "theater")

	media.handleBusMessage(topic, []byte(`{"desire":"off"}`))
	media.handleBusMessage(topic, nil)
	media.handleBusMessage(topic, []byte("not json"))

	mustMatch(t, media.panels.stateFor(playerKey("house", "theater")), panelDesireOff)
}

func TestThePanelDeskWakesOnlyOnChange(t *testing.T) {
	wake := make(chan struct{}, 1)
	cache := newPanelCache(wake)
	key := playerKey("house", "theater")

	cache.setState(key, panelDesireOff)
	mustMatch(t, len(wake), 1)

	<-wake
	cache.setState(key, panelDesireOff)
	mustMatch(t, len(wake), 0)

	cache.setState(key, panelDesireOn)
	mustMatch(t, len(wake), 1)
}

func TestThePanelDeskDropsADeletedPlayer(t *testing.T) {
	cache := newPanelCache(make(chan struct{}, 1))
	cache.setState(playerKey("house", "theater"), panelDesireOff)
	cache.setState(playerKey("house", "studio"), panelDesireOn)

	cache.retain(map[string]bool{playerKey("house", "studio"): true})

	mustMatch(t, cache.stateFor(playerKey("house", "theater")), "")
	mustMatch(t, cache.stateFor(playerKey("house", "studio")), panelDesireOn)
}

// display-operator can report a power value in lowercase or in
// PascalCase, so both spellings map to the same status.
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
		{power: "On", want: panelOn},
		{power: "Standby", want: panelOff},
		{power: "Suspend", want: panelOff},
		{power: "Off", want: panelOff},
		{power: "HardOff", want: panelOff},
	}
	for _, one := range cases {
		t.Run(one.power, func(t *testing.T) {
			observed := DisplayObserved{Brightness: ptr(70), Power: one.power}
			mustMatch(t, panelFromDisplay(observed), one.want)
		})
	}
}

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

// The Display resource's enum values are PascalCase.
func TestTheOverrideWritesThePascalCaseOff(t *testing.T) {
	cases := []struct {
		mode string
		want DisplayOverride
	}{
		{mode: offModeBacklight, want: DisplayOverride{Backlight: "Off"}},
		{mode: offModePower, want: DisplayOverride{Power: "Off"}},
	}
	for _, one := range cases {
		t.Run(one.mode, func(t *testing.T) {
			mustMatch(t, *overrideFor(panelDesireOff, one.mode), one.want)
		})
	}
}
