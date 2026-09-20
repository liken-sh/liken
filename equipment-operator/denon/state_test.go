// The branches of the parser that a transcript does not reach: every
// alternate word each setting can take, and the lines that name no
// setting at all.

package denon

import (
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

func TestDimmerWordReadsEverySetting(t *testing.T) {
	cases := map[string]string{"OFF": "off", "DIM": "dim", "DAR": "dark", "BRI": "bright"}
	for word, want := range cases {
		t.Run(word, func(t *testing.T) {
			got, ok := dimmerWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
		})
	}
	_, ok := dimmerWord("SEL")
	mustMatch(t, ok, false)
}

func TestAutoStandbyWordReadsEveryTimer(t *testing.T) {
	cases := map[string]string{"OFF": "off", "15M": "15m", "30M": "30m", "60M": "60m", "2H": "2h", "4H": "4h", "8H": "8h"}
	for word, want := range cases {
		t.Run(word, func(t *testing.T) {
			got, ok := autoStandbyWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
		})
	}
	_, ok := autoStandbyWord("5M")
	mustMatch(t, ok, false)
}

func TestInputModeWordReadsEveryMode(t *testing.T) {
	cases := map[string]string{"AUTO": "auto", "HDMI": "hdmi", "DIGITAL": "digital", "ANALOG": "analog"}
	for word, want := range cases {
		t.Run(word, func(t *testing.T) {
			got, ok := inputModeWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
		})
	}
	_, ok := inputModeWord("OTHER")
	mustMatch(t, ok, false)
}

func TestMultEqWordReadsEveryMode(t *testing.T) {
	cases := map[string]string{
		"AUDYSSEY": "reference",
		"BYP.LR":   "l/r bypass",
		"FLAT":     "flat",
		"MANUAL":   "manual",
		"OFF":      "off",
	}
	for word, want := range cases {
		t.Run(word, func(t *testing.T) {
			got, ok := multEqWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
		})
	}
	_, ok := multEqWord("MAGIC")
	mustMatch(t, ok, false)
}

func TestDynamicVolumeWordReadsEverySetting(t *testing.T) {
	cases := map[string]string{"OFF": "off", "LIT": "light", "MED": "medium", "HEV": "heavy"}
	for word, want := range cases {
		t.Run(word, func(t *testing.T) {
			got, ok := dynamicVolumeWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
		})
	}
	_, ok := dynamicVolumeWord("BLARING")
	mustMatch(t, ok, false)
}

func TestRestorerWordReadsEverySetting(t *testing.T) {
	cases := map[string]string{"OFF": "off", "LOW": "low", "MED": "medium", "HI": "high"}
	for word, want := range cases {
		t.Run(word, func(t *testing.T) {
			got, ok := restorerWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
		})
	}
	_, ok := restorerWord("SOFT")
	mustMatch(t, ok, false)
}

func TestDrcWordReadsEverySetting(t *testing.T) {
	cases := map[string]string{"OFF": "off", "LOW": "low", "MID": "mid", "HI": "hi", "AUTO": "auto"}
	for word, want := range cases {
		t.Run(word, func(t *testing.T) {
			got, ok := drcWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
		})
	}
	_, ok := drcWord("EXTREME")
	mustMatch(t, ok, false)
}

func TestDialogEnhancerWordReadsEverySetting(t *testing.T) {
	cases := map[string]string{"OFF": "off", "LOW": "low", "MID": "mid", "HIGH": "high"}
	for word, want := range cases {
		t.Run(word, func(t *testing.T) {
			got, ok := dialogEnhancerWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
		})
	}
	_, ok := dialogEnhancerWord("SUPER")
	mustMatch(t, ok, false)
}

func TestOnOffWordAndText(t *testing.T) {
	for word, want := range map[string]bool{"ON": true, "OFF": false} {
		t.Run(word, func(t *testing.T) {
			got, ok := onOffWord(word)
			mustMatch(t, ok, true)
			mustMatch(t, got, want)
			mustMatch(t, onOffText(want), map[bool]string{true: "on", false: "off"}[want])
		})
	}
	_, ok := onOffWord("MAYBE")
	mustMatch(t, ok, false)
}

func TestVideoSelectReadsOffAndASource(t *testing.T) {
	mustMatch(t, videoSelect("OFF"), "off")
	mustMatch(t, videoSelect(""), "off")
	mustMatch(t, videoSelect("DVD"), "DVD")
}

func TestBluetoothWordReadsBothLines(t *testing.T) {
	cases := []struct {
		body   string
		mode   string
		output string
		ok     bool
	}{
		{"ON", "on", "", true},
		{"OFF", "off", "", true},
		{"SP", "", "speakers", true},
		{"BT", "", "bluetooth", true},
		{"SOMETHING", "", "", false},
	}
	for _, one := range cases {
		t.Run(one.body, func(t *testing.T) {
			mode, output, ok := bluetoothWord(one.body)
			mustMatch(t, ok, one.ok)
			mustMatch(t, mode, one.mode)
			mustMatch(t, output, one.output)
		})
	}
}

func TestParseSleepReadsOffAndMinutes(t *testing.T) {
	cases := []struct {
		body string
		want int
		ok   bool
	}{
		{"OFF", 0, true},
		{"030", 30, true},
		{"120", 120, true},
		{"121", 0, false},
		{"abc", 0, false},
	}
	for _, one := range cases {
		t.Run(one.body, func(t *testing.T) {
			got, ok := parseSleep(one.body)
			mustMatch(t, ok, one.ok)
			if one.ok {
				mustMatch(t, got, one.want)
			}
		})
	}
}

func TestParseQuickReadsEverySlot(t *testing.T) {
	for slot := 0; slot <= 5; slot++ {
		t.Run(string(rune('0'+slot)), func(t *testing.T) {
			got, ok := parseQuick("MSQUICK" + string(rune('0'+slot)))
			mustMatch(t, ok, true)
			mustMatch(t, got, slot)
		})
	}
	_, ok := parseQuick("MSQUICK9")
	mustMatch(t, ok, false)
}

func TestParseChannelVolumeReadsATrim(t *testing.T) {
	channel, value, ok := parseChannelVolume("CVSW 505")
	mustMatch(t, ok, true)
	mustMatch(t, channel, "SW")
	mustMatch(t, value, 0.5)

	if _, _, ok := parseChannelVolume("CVFL"); ok {
		t.Error("a channel line with no value was read")
	}
	if _, _, ok := parseChannelVolume("CVFL FOO"); ok {
		t.Error("a channel line with no digits was read")
	}
}

func TestApplyZoneLineReadsEveryShape(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
		want  zoneState
	}{
		{name: "off", body: "OFF", field: powerField,
			want: zoneState{Power: equipment.PowerStandby, Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves, seen: true}},
		{name: "mute on", body: "MUON", field: muteField,
			want: zoneState{Mute: true, Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves, seen: true}},
		{name: "a sleep timer", body: "SLP030", field: sleepField,
			want: zoneState{Sleep: 30, Volume: unknownHalves, VolumeMax: unknownHalves, Quick: unknownHalves, seen: true}},
		{name: "a whole volume", body: "65", field: volumeField,
			want: zoneState{Volume: 130, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves, seen: true}},
		{name: "an input", body: "TUNER", field: inputField,
			want: zoneState{Input: "TUNER", Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves, seen: true}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got, field, ok := applyZoneLine(newDenonState().Zone2, one.body)
			mustMatch(t, ok, true)
			mustMatch(t, field, one.field)
			mustMatchDenonState(t, denonState{Zone2: got}, denonState{Zone2: one.want})
		})
	}

	ignored := []string{"CVFL 50", "PSBAS 50", "HPF 80", "CS", "", "MUBAD", "SLP999"}
	for _, body := range ignored {
		t.Run("ignore "+body, func(t *testing.T) {
			_, _, ok := applyZoneLine(newDenonState().Zone2, body)
			mustMatch(t, ok, false)
		})
	}
}

func TestApplyDenonLineReadsTheThirdZoneAndRejectsBadDigits(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		zone  string
		known bool
	}{
		{name: "the third zone on", line: "Z3ON", zone: zone3, known: true},
		{name: "the third zone volume", line: "Z390", zone: zone3, known: true},
		{name: "a bad sleep timer", line: "SLP999", known: false},
		{name: "a bad quick select", line: "MSQUICK9", known: false},
		{name: "a bad bluetooth word", line: "BTTX MAYBE", known: false},
		{name: "a bad speaker preset", line: "SPPR XX", known: false},
		{name: "a bad dimmer", line: "DIM SEL", known: false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			_, zone, _, known := applyDenonLine(newDenonState(), one.line)
			mustMatch(t, known, one.known)
			if one.known {
				mustMatch(t, zone, one.zone)
			}
		})
	}
}

// Every PS setting that carries a word is read through the line, so a
// word the receiver sends is covered as the receiver sends it.
func TestApplyDenonLineReadsEveryMultEqWord(t *testing.T) {
	cases := map[string]string{
		"PSMULTEQ:FLAT":   "flat",
		"PSMULTEQ:BYP.LR": "l/r bypass",
		"PSMULTEQ:MANUAL": "manual",
		"PSMULTEQ:OFF":    "off",
	}
	for line, want := range cases {
		t.Run(line, func(t *testing.T) {
			state, _, _, known := applyDenonLine(newDenonState(), line)
			mustMatch(t, known, true)
			mustMatch(t, *state.Settings.Audyssey.Multeq, want)
		})
	}
}

func TestApplyDenonLineReadsEveryDynamicVolumeWord(t *testing.T) {
	cases := map[string]string{"PSDYNVOL LIT": "light", "PSDYNVOL MED": "medium", "PSDYNVOL HEV": "heavy"}
	for line, want := range cases {
		t.Run(line, func(t *testing.T) {
			state, _, _, known := applyDenonLine(newDenonState(), line)
			mustMatch(t, known, true)
			mustMatch(t, *state.Settings.Audyssey.DynamicVolume, want)
		})
	}
}

func TestApplyDenonLineReadsEveryRestorerWord(t *testing.T) {
	cases := map[string]string{"PSRSTR MED": "medium", "PSRSTR HI": "high"}
	for line, want := range cases {
		t.Run(line, func(t *testing.T) {
			state, _, _, known := applyDenonLine(newDenonState(), line)
			mustMatch(t, known, true)
			mustMatch(t, *state.Settings.Audio.Restorer, want)
		})
	}
}

func TestApplyDenonLineRejectsParameterSettingsWithNoWord(t *testing.T) {
	for _, line := range []string{
		"PSDYNEQ MAYBE", "PSDYNVOL", "PSLOM MAYBE", "PSDRC", "PSLFE XX",
		"PSEFF XX", "PSDEL XX", "PSDELAY XX", "PSSWR MAYBE", "PSGEQ MAYBE",
		"PSHEQ MAYBE", "PSSPV MAYBE", "PSDEH", "PSBAS XX", "PSTRE XX",
		"PSTONE CTRL MAYBE", "PSMULTEQ:MAGIC", "PSDYNVOL BLARING",
		"PSRSTR SOFT", "PSDRC EXTREME", "PSDEH SUPER",
	} {
		t.Run(line, func(t *testing.T) {
			_, _, _, known := applyDenonLine(newDenonState(), line)
			mustMatch(t, known, false)
		})
	}
}
