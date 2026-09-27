package main

// These tests cover the panel override across an operator restart: the
// pass reads the override the Display carries before it writes one, and
// a restart that finds the override it wants writes nothing.

import "testing"

// A restarted operator holds no record of what it wrote, and the bus
// delivers the idle client's retained desire again. The Display already
// carries the override that desire asks for, in either spelling of off,
// so the pass adopts it and writes nothing.
func TestARestartAdoptsTheOverrideTheDisplayCarries(t *testing.T) {
	cases := []struct {
		name     string
		desire   string
		defaults *IdlePolicy
		standing *DisplayOverride
	}{
		{name: "a dark backlight", desire: panelDesireOff, standing: &DisplayOverride{Backlight: displayPowerOff}},
		{name: "a dark backlight in lowercase", desire: panelDesireOff, standing: &DisplayOverride{Backlight: "off"}},
		{
			name: "a panel powered down", desire: panelDesireOff, defaults: &IdlePolicy{OffMode: offModePower},
			standing: &DisplayOverride{Power: displayPowerOff},
		},
		{
			name: "a panel powered down in lowercase", desire: panelDesireOff, defaults: &IdlePolicy{OffMode: offModePower},
			standing: &DisplayOverride{Power: "off"},
		},
		{name: "a lit panel", desire: panelDesireOn},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := screenCluster()
			cluster.displays[testMonitor].Spec.Override = each.standing
			media := testOperator(t, cluster, make(chan struct{}, 1))

			statePanel(media, []Player{*housePlayer()}, each.desire, each.defaults)

			mustMatch(t, len(cluster.applies), 0)
		})
	}
}

// An override the Display carries that differs from the desire is
// written over, and the write is the one line a person reads.
func TestARestartWritesAnOverrideThatDiffers(t *testing.T) {
	cases := []struct {
		name     string
		desire   string
		defaults *IdlePolicy
		standing *DisplayOverride
		want     *DisplayOverride
	}{
		{name: "the desire is off and the panel is lit", desire: panelDesireOff, want: &DisplayOverride{Backlight: displayPowerOff}},
		{name: "the desire is on and the panel is dark", desire: panelDesireOn, standing: &DisplayOverride{Backlight: displayPowerOff}},
		{
			name: "the off mode moved from the backlight to the power", desire: panelDesireOff,
			defaults: &IdlePolicy{OffMode: offModePower},
			standing: &DisplayOverride{Backlight: displayPowerOff}, want: &DisplayOverride{Power: displayPowerOff},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := screenCluster()
			cluster.displays[testMonitor].Spec.Override = each.standing
			media := testOperator(t, cluster, make(chan struct{}, 1))

			statePanel(media, []Player{*housePlayer()}, each.desire, each.defaults)

			mustMatch(t, len(cluster.applies), 1)
			mustMatch(t, describeOverride(cluster.applies[0].override), describeOverride(each.want))
		})
	}
}
