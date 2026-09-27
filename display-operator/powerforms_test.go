package main

// Every power value a writer states has two spellings: the lowercase
// word and its PascalCase form. An object can hold either one,
// depending on the build of the writer that made it, so both spellings
// must reach the panel as the same value, and the values this operator
// writes and reports keep their lowercase form.

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
		{"power off", DisplayOverride{Power: "off"}, "status captured=power on", "set power=4"},
		{"power Off", DisplayOverride{Power: "Off"}, "status captured=power on", "set power=4"},
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
