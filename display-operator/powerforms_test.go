package main

// Every power value has two spellings, the lowercase word and the
// PascalCase form, and the operator reports the PascalCase form. A
// stored object can contain either spelling, depending on the version
// of the program that wrote it. Both spellings set the panel the same
// way, and a restarted operator does not write to the panel for a
// stored value that differs from the new one only in case.

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
				t.Fatalf("parsing the claim controls: %v", err)
			}
			if got := controls.forRequest("screen").Power; got != c.want {
				t.Errorf("power = %q, want %q", got, c.want)
			}
		})
	}
}

// The claim fails on any other spelling, as on any other value that
// the driver does not accept.
func TestAnotherSpellingOfAClaimsPowerFails(t *testing.T) {
	for _, power := range []string{`"ON"`, `"onwhileclaimed"`} {
		t.Run(power, func(t *testing.T) {
			if _, err := claimControls(resolvedConfig(t, claimControl(`{"power": `+power+`}`))); err == nil {
				t.Errorf("the parse accepted the power %s, want an error", power)
			}
		})
	}
}

// status.observed and the values in status.capabilities use the
// PascalCase names.
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
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))

	if err := fixture.pass(); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	status := fixture.display().Status
	if status.Observed == nil || status.Observed.Power == nil || *status.Observed.Power != "On" {
		t.Errorf("observed = %+v, want the power On", status.Observed)
	}
	if got := status.Capabilities[powerControl].Values; !slices.Equal(got, []string{"On", "Off"}) {
		t.Errorf("power values = %v, want [On Off]", got)
	}
}

// A captured power value in either spelling turns the panel back on.
func TestACapturedPowerInEitherSpellingIsRestored(t *testing.T) {
	for _, power := range []string{"on", "On"} {
		t.Run(power, func(t *testing.T) {
			panel := recordedPanel(t, "lg-hdr-wqhd")
			panel.values[vcpPowerMode] = powerModeOff
			fixture := newDisplayFixture(t, panel)
			fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Power: stringOf(power)})

			if err := fixture.pass(); err != nil {
				t.Fatalf("running the pass: %v", err)
			}
			fixture.awaitRestore()

			if got := fixture.value(vcpPowerMode); got != powerModeOn {
				t.Errorf("the panel reports the power mode %#02x, want %#02x", got, powerModeOn)
			}
		})
	}
}

// When the panel does not confirm a restore, the operator records it
// in status.unconfirmed, and a restarted operator that finds the
// record does not write to the panel. A record in either spelling has
// this effect.
func TestAnUnconfirmedRestoreInEitherSpellingIsNotRetriedAfterARestart(t *testing.T) {
	for _, power := range []string{"on", "On"} {
		t.Run(power, func(t *testing.T) {
			panel := recordedPanel(t, "lg-hdr-wqhd")
			panel.values[vcpPowerMode] = powerModeOff
			fixture := newDisplayFixture(t, panel)
			display := fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Power: stringOf(power)})
			display.Status.Unconfirmed = []DisplayUnconfirmed{{Control: powerControl, Value: power}}

			fixture.restartOperator()
			fixture.passes(3)

			if fixture.control.restoresRunning() {
				t.Error("a restore started on a panel that the record marks as off, want no restore")
			}
			if got := panel.writesTo(vcpPowerMode); len(got) != 0 {
				t.Errorf("the panel received the power writes %v, want none", got)
			}
		})
	}
}

// The power record file stores onWhileClaimed whichever spelling the
// claim used, because releasePower schedules standby only for a
// connector whose record is exactly onWhileClaimed.
func TestThePowerRecordFileUsesTheLowercaseWordForAPascalCaseClaim(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "OnWhileClaimed"}`), claimedPanel(panel))
	grace := holdGrace(plugin)

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("preparing the claim: %s", claim.Error)
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

func TestBothSpellingsOfAnOverrideTurnThePanelOff(t *testing.T) {
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
			fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
			fixture.declare(DisplaySpec{Override: &c.override})

			if err := fixture.pass(); err != nil {
				t.Fatalf("running the pass: %v", err)
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
