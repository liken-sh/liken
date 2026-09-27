package main

// Every power value has two spellings: the lowercase word and its
// PascalCase form. The operator reports the PascalCase form. An object
// can hold either one, depending on the build of the writer that made
// it, so both spellings must reach the panel as the same value, and a
// stored value that differs from a new one only in case is the same
// value: it never makes a restarted operator write to a panel.

import (
	"slices"
	"testing"
)

func TestBothSpellingsOfAClaimsPowerAreOneValue(t *testing.T) {
	cases := []struct {
		name  string
		power string
		want  string
	}{
		{"on", `"on"`, powerOn},
		{"On", `"On"`, powerOn},
		{"onWhileClaimed", `"onWhileClaimed"`, powerOnWhileClaimed},
		{"OnWhileClaimed", `"OnWhileClaimed"`, powerOnWhileClaimed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			controls, err := claimControls(resolvedConfig(t, claimControl(`{"power": `+c.power+`}`)))
			if err != nil {
				t.Fatal(err)
			}
			if got := controls.forRequest("screen").Power; got != c.want {
				t.Errorf("power = %q, want %q", got, c.want)
			}
		})
	}
}

// A spelling outside the two forms is a typo, and the claim fails on
// it the way it fails on any other value this driver does not take.
func TestAnotherSpellingOfAClaimsPowerFails(t *testing.T) {
	for _, power := range []string{`"ON"`, `"onwhileclaimed"`} {
		t.Run(power, func(t *testing.T) {
			if _, err := claimControls(resolvedConfig(t, claimControl(`{"power": `+power+`}`))); err == nil {
				t.Errorf("the parse accepted the power %s", power)
			}
		})
	}
}

// The panel's power state reports under the PascalCase names, in
// status.observed and in the values status.capabilities lists.
func TestThePanelsPowerReportsInPascalCase(t *testing.T) {
	cases := []struct {
		raw  uint16
		name string
	}{
		{0x01, "On"},
		{0x02, "Standby"},
		{0x03, "Suspend"},
		{0x04, "Off"},
		{0x05, "HardOff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := valueName(vcpPowerMode, c.raw); got != c.name {
				t.Errorf("valueName(power, %#02x) = %q, want %q", c.raw, got, c.name)
			}
		})
	}
}

func TestTheDisplayReportsItsPowerInPascalCase(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	status := fixture.display().Status
	if status.Observed == nil || status.Observed.Power == nil || *status.Observed.Power != "On" {
		t.Errorf("observed = %+v, want the power On", status.Observed)
	}
	if got := status.Capabilities[powerControl].Values; !slices.Equal(got, []string{"On", "Off"}) {
		t.Errorf("power values = %v, want [On Off]", got)
	}
}

// A capture an operator saved in either spelling brings the panel back
// on.
func TestACaptureInEitherSpellingRestoresThePower(t *testing.T) {
	for _, power := range []string{"on", "On"} {
		t.Run(power, func(t *testing.T) {
			panel := drillPanel(t, "lg-hdr-wqhd")
			panel.values[vcpPowerMode] = powerModeOff
			fixture := newDisplayFixture(t, panel)
			fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Power: stringOf(power)})

			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}
			fixture.awaitRestore()

			if got := fixture.holds(vcpPowerMode); got != powerModeOn {
				t.Errorf("the panel holds the power mode %#02x, want %#02x", got, powerModeOn)
			}
		})
	}
}

// A restore the panel never confirmed is recorded in status, and the
// record holds a restarted operator off the panel. A record in the
// lowercase spelling holds it the same way.
func TestARecordInEitherSpellingHoldsARestartOffThePanel(t *testing.T) {
	for _, power := range []string{"on", "On"} {
		t.Run(power, func(t *testing.T) {
			panel := drillPanel(t, "lg-hdr-wqhd")
			panel.values[vcpPowerMode] = powerModeOff
			fixture := newDisplayFixture(t, panel)
			display := fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Power: stringOf(power)})
			display.Status.Unconfirmed = []DisplayUnconfirmed{{Control: powerControl, Value: power}}

			fixture.restartOperator()
			fixture.passes(3)

			if fixture.control.restoresRunning() {
				t.Error("a restore started against a panel the record holds it off")
			}
			if got := panel.took(vcpPowerMode); len(got) != 0 {
				t.Errorf("the panel took the power writes %v, want none", got)
			}
		})
	}
}

// The power record file keeps the lowercase word, whichever spelling
// the claim stated, so the release after the claim ends finds it.
func TestThePascalCasePowerRecordsAndReleasesTheLowercaseWord(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "OnWhileClaimed"}`), claimedPanel(panel))
	grace := holdGrace(plugin)

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}
	if got := powerRecord(t, plugin)["HDMI-A-2"]; got != powerOnWhileClaimed {
		t.Errorf("record = %q, want %s", got, powerOnWhileClaimed)
	}
	unprepare(t, plugin)

	if got := powerRecord(t, plugin)["HDMI-A-2"]; got != powerReleased {
		t.Errorf("record = %q, want the panel marked %s", got, powerReleased)
	}
	if got := grace.scheduled(); len(got) != 1 {
		t.Errorf("scheduled %v, want one standby", got)
	}
}

func TestBothSpellingsOfAnOverrideDarkenThePanel(t *testing.T) {
	cases := []struct {
		name     string
		override DisplayOverride
		captured string
		written  string
	}{
		{"power off", DisplayOverride{Power: "off"}, "status captured=power On", "set power=4"},
		{"power Off", DisplayOverride{Power: "Off"}, "status captured=power On", "set power=4"},
		{"backlight off", DisplayOverride{Backlight: "off"}, "status captured=brightness 50", "set brightness=0"},
		{"backlight Off", DisplayOverride{Backlight: "Off"}, "status captured=brightness 50", "set brightness=0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
			fixture.declare(DisplaySpec{Override: &c.override})

			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}

			journal := fixture.lines()
			captured := slices.Index(journal, c.captured)
			written := slices.Index(journal, c.written)
			if captured < 0 || written < 0 || captured > written {
				t.Errorf("journal = %q, want %q before %q", journal, c.captured, c.written)
			}
		})
	}
}
