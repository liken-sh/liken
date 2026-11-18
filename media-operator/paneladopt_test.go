package main

import "testing"

// After a restart, the operator has no record of what it wrote, and the
// bus sends the idle client's retained desire again. When the Display's
// override already matches the desire, with "off" in either case, the
// operator writes nothing.
func TestARestartKeepsAnOverrideThatMatchesTheDesire(t *testing.T) {
	cases := []struct {
		name     string
		desire   string
		defaults *IdlePolicy
		existing *DisplayOverride
	}{
		{name: "backlight off", desire: panelDesireOff, existing: &DisplayOverride{Backlight: displayPowerOff}},
		{name: "backlight off in lowercase", desire: panelDesireOff, existing: &DisplayOverride{Backlight: "off"}},
		{
			name: "panel power off", desire: panelDesireOff, defaults: &IdlePolicy{OffMode: offModePower},
			existing: &DisplayOverride{Power: displayPowerOff},
		},
		{
			name: "panel power off in lowercase", desire: panelDesireOff, defaults: &IdlePolicy{OffMode: offModePower},
			existing: &DisplayOverride{Power: "off"},
		},
		{name: "panel on", desire: panelDesireOn},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := screenCluster()
			cluster.displays[testMonitor].Spec.Override = each.existing
			media := testOperator(t, cluster, make(chan struct{}, 1))

			sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, each.desire, each.defaults)

			mustMatch(t, len(cluster.applies), 0)
		})
	}
}

// An override that differs from the desire is replaced by one write.
func TestARestartReplacesAnOverrideThatDiffers(t *testing.T) {
	cases := []struct {
		name     string
		desire   string
		defaults *IdlePolicy
		existing *DisplayOverride
		want     *DisplayOverride
	}{
		{name: "the desire is off and the panel is on", desire: panelDesireOff, want: &DisplayOverride{Backlight: displayPowerOff}},
		{name: "the desire is on and the backlight is off", desire: panelDesireOn, existing: &DisplayOverride{Backlight: displayPowerOff}},
		{
			name: "the off mode changed from backlight to power", desire: panelDesireOff,
			defaults: &IdlePolicy{OffMode: offModePower},
			existing: &DisplayOverride{Backlight: displayPowerOff}, want: &DisplayOverride{Power: displayPowerOff},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := screenCluster()
			cluster.displays[testMonitor].Spec.Override = each.existing
			media := testOperator(t, cluster, make(chan struct{}, 1))

			sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, each.desire, each.defaults)

			mustMatch(t, len(cluster.applies), 1)
			mustMatch(t, describeOverride(cluster.applies[0].override), describeOverride(each.want))
		})
	}
}
