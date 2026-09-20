// The settings vocabulary: the wire commands each field carries, the
// apply path, and the bus writes that reach them.

package denon

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

func TestTheCommandBuildersWriteTheWireLines(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		build func() (string, error)
	}{
		{"eco auto", "ECOAUTO", func() (string, error) { return EcoCommand("auto") }},
		{"eco on", "ECOON", func() (string, error) { return EcoCommand("on") }},
		{"eco off", "ECOOFF", func() (string, error) { return EcoCommand("off") }},
		{"dimmer off", "DIM OFF", func() (string, error) { return DimmerCommand("off") }},
		{"dimmer dim", "DIM DIM", func() (string, error) { return DimmerCommand("dim") }},
		{"dimmer dark", "DIM DAR", func() (string, error) { return DimmerCommand("dark") }},
		{"dimmer bright", "DIM BRI", func() (string, error) { return DimmerCommand("bright") }},
		{"auto standby 15m", "STBY15M", func() (string, error) { return AutoStandbyCommand("15m") }},
		{"auto standby 30m", "STBY30M", func() (string, error) { return AutoStandbyCommand("30m") }},
		{"auto standby 60m", "STBY60M", func() (string, error) { return AutoStandbyCommand("60m") }},
		{"auto standby 2h", "STBY2H", func() (string, error) { return AutoStandbyCommand("2h") }},
		{"auto standby 4h", "STBY4H", func() (string, error) { return AutoStandbyCommand("4h") }},
		{"auto standby 8h", "STBY8H", func() (string, error) { return AutoStandbyCommand("8h") }},
		{"auto standby off", "STBYOFF", func() (string, error) { return AutoStandbyCommand("off") }},
		{"speaker preset", "SPPR 2", func() (string, error) { return SpeakerPresetCommand(2) }},
		{"input mode auto", "SDAUTO", func() (string, error) { return AudioInputModeCommand("auto") }},
		{"input mode hdmi", "SDHDMI", func() (string, error) { return AudioInputModeCommand("hdmi") }},
		{"input mode digital", "SDDIGITAL", func() (string, error) { return AudioInputModeCommand("digital") }},
		{"input mode analog", "SDANALOG", func() (string, error) { return AudioInputModeCommand("analog") }},
		{"video select off", "SVOFF", func() (string, error) { return VideoSelectCommand("off") }},
		{"video select a source", "SVDVD", func() (string, error) { return VideoSelectCommand("DVD") }},
		{"bluetooth transmitter on", "BTTX ON", func() (string, error) { return BluetoothTransmitterCommand("on") }},
		{"bluetooth transmitter off", "BTTX OFF", func() (string, error) { return BluetoothTransmitterCommand("off") }},
		{"bluetooth output speakers", "BTTX SP", func() (string, error) { return BluetoothOutputCommand("speakers") }},
		{"bluetooth output bluetooth", "BTTX BT", func() (string, error) { return BluetoothOutputCommand("bluetooth") }},
		{"tone control on", "PSTONE CTRL ON", func() (string, error) { return ToneControlCommand(true) }},
		{"tone control off", "PSTONE CTRL OFF", func() (string, error) { return ToneControlCommand(false) }},
		{"bass", "PSBAS 53", func() (string, error) { return BassCommand(3) }},
		{"treble", "PSTRE 44", func() (string, error) { return TrebleCommand(-6) }},
		{"multEQ reference", "PSMULTEQ:AUDYSSEY", func() (string, error) { return MultEqCommand("reference") }},
		{"multEQ bypass", "PSMULTEQ:BYP.LR", func() (string, error) { return MultEqCommand("l/r bypass") }},
		{"multEQ flat", "PSMULTEQ:FLAT", func() (string, error) { return MultEqCommand("flat") }},
		{"multEQ manual", "PSMULTEQ:MANUAL", func() (string, error) { return MultEqCommand("manual") }},
		{"multEQ off", "PSMULTEQ:OFF", func() (string, error) { return MultEqCommand("off") }},
		{"dynamic EQ on", "PSDYNEQ ON", func() (string, error) { return DynamicEqCommand(true) }},
		{"dynamic EQ off", "PSDYNEQ OFF", func() (string, error) { return DynamicEqCommand(false) }},
		{"reference level", "PSREFLEV 10", func() (string, error) { return ReferenceLevelOffsetCommand(10) }},
		{"dynamic volume off", "PSDYNVOL OFF", func() (string, error) { return DynamicVolumeCommand("off") }},
		{"dynamic volume light", "PSDYNVOL LIT", func() (string, error) { return DynamicVolumeCommand("light") }},
		{"dynamic volume medium", "PSDYNVOL MED", func() (string, error) { return DynamicVolumeCommand("medium") }},
		{"dynamic volume heavy", "PSDYNVOL HEV", func() (string, error) { return DynamicVolumeCommand("heavy") }},
		{"loudness on", "PSLOM ON", func() (string, error) { return LoudnessManagementCommand(true) }},
		{"loudness off", "PSLOM OFF", func() (string, error) { return LoudnessManagementCommand(false) }},
		{"dynamic range off", "PSDRC OFF", func() (string, error) { return DynamicRangeCommand("off") }},
		{"dynamic range low", "PSDRC LOW", func() (string, error) { return DynamicRangeCommand("low") }},
		{"dynamic range mid", "PSDRC MID", func() (string, error) { return DynamicRangeCommand("mid") }},
		{"dynamic range hi", "PSDRC HI", func() (string, error) { return DynamicRangeCommand("hi") }},
		{"LFE", "PSLFE 10", func() (string, error) { return LFECommand(-10) }},
		{"effect", "PSEFF 05", func() (string, error) { return EffectCommand(5) }},
		{"delay", "PSDEL 100", func() (string, error) { return DelayCommand(100) }},
		{"audio delay", "PSDELAY 200", func() (string, error) { return AudioDelayCommand(200) }},
		{"subwoofer on", "PSSWR ON", func() (string, error) { return SubwooferCommand(true) }},
		{"subwoofer off", "PSSWR OFF", func() (string, error) { return SubwooferCommand(false) }},
		{"restorer off", "PSRSTR OFF", func() (string, error) { return RestorerCommand("off") }},
		{"restorer low", "PSRSTR LOW", func() (string, error) { return RestorerCommand("low") }},
		{"restorer medium", "PSRSTR MED", func() (string, error) { return RestorerCommand("medium") }},
		{"restorer high", "PSRSTR HI", func() (string, error) { return RestorerCommand("high") }},
		{"graphic EQ on", "PSGEQ ON", func() (string, error) { return GraphicEqCommand("on") }},
		{"graphic EQ off", "PSGEQ OFF", func() (string, error) { return GraphicEqCommand("off") }},
		{"headphone EQ on", "PSHEQ ON", func() (string, error) { return HeadphoneEqCommand("on") }},
		{"headphone EQ off", "PSHEQ OFF", func() (string, error) { return HeadphoneEqCommand("off") }},
		{"speaker virtualizer on", "PSSPV ON", func() (string, error) { return SpeakerVirtualizerCommand(true) }},
		{"speaker virtualizer off", "PSSPV OFF", func() (string, error) { return SpeakerVirtualizerCommand(false) }},
		{"dialog enhancer off", "PSDEH OFF", func() (string, error) { return DialogEnhancerCommand("off") }},
		{"dialog enhancer low", "PSDEH LOW", func() (string, error) { return DialogEnhancerCommand("low") }},
		{"dialog enhancer mid", "PSDEH MID", func() (string, error) { return DialogEnhancerCommand("mid") }},
		{"dialog enhancer high", "PSDEH HIGH", func() (string, error) { return DialogEnhancerCommand("high") }},
		{"channel volume", "CVFL 505", func() (string, error) { return ChannelVolumeCommand("FL", 0.5) }},
		{"an unknown eco mode", "", func() (string, error) { return EcoCommand("turbo") }},
		{"an unknown dimmer", "", func() (string, error) { return DimmerCommand("blinding") }},
		{"an empty video select", "", func() (string, error) { return VideoSelectCommand("") }},
		{"an empty channel", "", func() (string, error) { return ChannelVolumeCommand("", 0) }},
		{"an unknown multEQ mode", "", func() (string, error) { return MultEqCommand("magic") }},
		{"an unknown dynamic volume", "", func() (string, error) { return DynamicVolumeCommand("blaring") }},
		{"an unknown restorer", "", func() (string, error) { return RestorerCommand("soft") }},
		{"an unknown switch", "", func() (string, error) { return GraphicEqCommand("maybe") }},
		{"an unknown auto standby", "", func() (string, error) { return AutoStandbyCommand("5m") }},
		{"an unknown input mode", "", func() (string, error) { return AudioInputModeCommand("radio") }},
		{"an unknown bluetooth mode", "", func() (string, error) { return BluetoothTransmitterCommand("pair") }},
		{"an unknown bluetooth output", "", func() (string, error) { return BluetoothOutputCommand("both") }},
		{"an unknown dynamic range", "", func() (string, error) { return DynamicRangeCommand("extreme") }},
		{"an unknown dialog enhancer", "", func() (string, error) { return DialogEnhancerCommand("super") }},
		{"a reference level not offered", "", func() (string, error) { return ReferenceLevelOffsetCommand(3) }},
		{"a speaker preset below one", "", func() (string, error) { return SpeakerPresetCommand(0) }},
		{"a speaker preset above four", "", func() (string, error) { return SpeakerPresetCommand(5) }},
		{"a delay below zero", "", func() (string, error) { return DelayCommand(-1) }},
		{"a delay above the limit", "", func() (string, error) { return DelayCommand(1000) }},
		{"an audio delay below zero", "", func() (string, error) { return AudioDelayCommand(-1) }},
		{"an audio delay above the limit", "", func() (string, error) { return AudioDelayCommand(1000) }},
		{"a positive LFE", "", func() (string, error) { return LFECommand(3) }},
		{"an LFE beyond the cut", "", func() (string, error) { return LFECommand(-11) }},
		{"a bass below the band", "", func() (string, error) { return BassCommand(-13) }},
		{"a bass above the band", "", func() (string, error) { return BassCommand(13) }},
		{"a treble below the band", "", func() (string, error) { return TrebleCommand(-13) }},
		{"a treble above the band", "", func() (string, error) { return TrebleCommand(13) }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got, err := one.build()
			mustMatch(t, got, one.want)
			if one.want == "" {
				mustMatch(t, err != nil, true)
			} else {
				mustSucceed(t, err)
			}
		})
	}
}

// A declared field the receiver has not reported is skipped, so the
// declared settings confirm against an empty report. There is nothing
// to confirm the field against, and blocking on it would retry forever.
func TestConfirmedBySkipsAnUnreportedField(t *testing.T) {
	eco := "on"
	want := Settings{System: SystemSettings{Eco: &eco}}

	if !want.ConfirmedBy(Settings{}) {
		t.Error("a declared field the receiver has not reported did not confirm")
	}
}

// A declared field reported at another value is not confirmed, so the
// operator sends it again.
func TestConfirmedByRejectsADifferentReportedValue(t *testing.T) {
	eco, observed := "on", "off"
	want := Settings{System: SystemSettings{Eco: &eco}}
	got := Settings{System: SystemSettings{Eco: &observed}}

	if want.ConfirmedBy(got) {
		t.Error("a field reported at another value was confirmed")
	}
}

// A declared field reported at the same value is confirmed.
func TestConfirmedByAcceptsAnEqualReportedValue(t *testing.T) {
	eco := "on"
	want := Settings{System: SystemSettings{Eco: &eco}}
	got := Settings{System: SystemSettings{Eco: &eco}}

	if !want.ConfirmedBy(got) {
		t.Error("a field reported at the same value was not confirmed")
	}
}

// A different value in any one declared field fails the whole block, so
// a partial apply is never taken for confirmed. Each row declares every
// family and differs in exactly the field it names.
func TestConfirmedByRejectsADifferenceInAnyDeclaredField(t *testing.T) {
	want := Settings{
		System: SystemSettings{
			Eco: strPtr("on"), Dimmer: strPtr("dim"), AutoStandby: strPtr("30m"),
			SpeakerPreset: intPtr(2), AudioInputMode: strPtr("auto"), VideoSelect: strPtr("off"),
			BluetoothTransmitter: strPtr("on"), BluetoothOutput: strPtr("speakers"),
		},
		Tone: ToneSettings{Control: boolPtr(true), Bass: intPtr(3), Treble: intPtr(-1)},
		Audyssey: AudysseySettings{
			Multeq: strPtr("flat"), DynamicEq: boolPtr(true), ReferenceLevelOffset: intPtr(0),
			DynamicVolume: strPtr("medium"), LoudnessManagement: boolPtr(true),
		},
		Audio: AudioSettings{
			DRC: strPtr("mid"), LFE: intPtr(-5), Effect: intPtr(5), Delay: intPtr(10),
			AudioDelay: intPtr(20), Subwoofer: boolPtr(true), Restorer: strPtr("low"),
			GraphicEq: strPtr("on"), HeadphoneEq: strPtr("off"), SpeakerVirtualizer: boolPtr(true),
			DialogEnhancer: strPtr("low"),
		},
	}
	cases := []struct {
		name     string
		observed Settings
	}{
		{"eco", Settings{System: SystemSettings{Eco: strPtr("off")}}},
		{"dimmer", Settings{System: SystemSettings{Dimmer: strPtr("bright")}}},
		{"auto standby", Settings{System: SystemSettings{AutoStandby: strPtr("60m")}}},
		{"speaker preset", Settings{System: SystemSettings{SpeakerPreset: intPtr(3)}}},
		{"audio input mode", Settings{System: SystemSettings{AudioInputMode: strPtr("hdmi")}}},
		{"video select", Settings{System: SystemSettings{VideoSelect: strPtr("DVD")}}},
		{"bluetooth transmitter", Settings{System: SystemSettings{BluetoothTransmitter: strPtr("off")}}},
		{"bluetooth output", Settings{System: SystemSettings{BluetoothOutput: strPtr("bluetooth")}}},
		{"tone control", Settings{Tone: ToneSettings{Control: boolPtr(false)}}},
		{"bass", Settings{Tone: ToneSettings{Bass: intPtr(5)}}},
		{"treble", Settings{Tone: ToneSettings{Treble: intPtr(2)}}},
		{"multeq", Settings{Audyssey: AudysseySettings{Multeq: strPtr("manual")}}},
		{"dynamic eq", Settings{Audyssey: AudysseySettings{DynamicEq: boolPtr(false)}}},
		{"reference level", Settings{Audyssey: AudysseySettings{ReferenceLevelOffset: intPtr(5)}}},
		{"dynamic volume", Settings{Audyssey: AudysseySettings{DynamicVolume: strPtr("heavy")}}},
		{"loudness", Settings{Audyssey: AudysseySettings{LoudnessManagement: boolPtr(false)}}},
		{"dynamic range", Settings{Audio: AudioSettings{DRC: strPtr("low")}}},
		{"lfe", Settings{Audio: AudioSettings{LFE: intPtr(-8)}}},
		{"effect", Settings{Audio: AudioSettings{Effect: intPtr(8)}}},
		{"delay", Settings{Audio: AudioSettings{Delay: intPtr(40)}}},
		{"audio delay", Settings{Audio: AudioSettings{AudioDelay: intPtr(90)}}},
		{"subwoofer", Settings{Audio: AudioSettings{Subwoofer: boolPtr(false)}}},
		{"restorer", Settings{Audio: AudioSettings{Restorer: strPtr("high")}}},
		{"graphic eq", Settings{Audio: AudioSettings{GraphicEq: strPtr("off")}}},
		{"headphone eq", Settings{Audio: AudioSettings{HeadphoneEq: strPtr("on")}}},
		{"speaker virtualizer", Settings{Audio: AudioSettings{SpeakerVirtualizer: boolPtr(false)}}},
		{"dialog enhancer", Settings{Audio: AudioSettings{DialogEnhancer: strPtr("high")}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if want.ConfirmedBy(one.observed) {
				t.Error("a field reported at another value was confirmed")
			}
		})
	}
}

// A declared channel the receiver has not reported is skipped, and a
// channel reported at the same trim confirms.
func TestConfirmedByHandlesTheChannelVolumes(t *testing.T) {
	want := Settings{ChannelVolumes: map[string]float64{"FL": 0.5}}
	if !want.ConfirmedBy(Settings{}) {
		t.Error("a declared channel the receiver has not reported did not confirm")
	}
	if !want.ConfirmedBy(Settings{ChannelVolumes: map[string]float64{"FL": 0.5}}) {
		t.Error("a channel reported at the same trim was not confirmed")
	}
	if want.ConfirmedBy(Settings{ChannelVolumes: map[string]float64{"FL": 0.0}}) {
		t.Error("a channel reported at another trim was confirmed")
	}
}

func TestApplySettingsSendsEveryDeclaredField(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	want := Settings{
		System:         SystemSettings{Eco: strPtr("auto"), Dimmer: strPtr("bright")},
		Tone:           ToneSettings{Control: boolPtr(true), Bass: intPtr(3)},
		Audio:          AudioSettings{LFE: intPtr(-10), GraphicEq: strPtr("on")},
		ChannelVolumes: map[string]float64{"FL": 0.5},
	}
	mustSucceed(t, harness.client.ApplySettings(want))

	sent := harness.receiver.waitForCommands(t, "CVFL 505")
	expected := "ECOAUTO DIM BRI PSTONE CTRL ON PSBAS 53 PSLFE 10 PSGEQ ON CVFL 505"
	mustMatch(t, strings.Join(sent, " "), expected)
}

func TestApplySettingsSendsNothingForUndeclaredFields(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	mustSucceed(t, harness.client.ApplySettings(Settings{}))

	mustStaySilentCommands(t, harness.receiver.commands, 100*time.Millisecond)
}

// mustStaySilentCommands fails the test if any command arrives inside
// the window.
func mustStaySilentCommands(t *testing.T, commands <-chan string, window time.Duration) {
	t.Helper()
	select {
	case command := <-commands:
		t.Fatalf("wanted no command, got %q", command)
	case <-time.After(window):
	}
}

// A declared setting reaches the receiver, echoes back, and folds into
// the state the next Settings() reads.
func TestApplySettingsRoundTripsThroughTheReceiver(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	want := Settings{
		System:         SystemSettings{Eco: strPtr("auto"), Dimmer: strPtr("bright")},
		Tone:           ToneSettings{Bass: intPtr(3)},
		Audio:          AudioSettings{LFE: intPtr(-10), DRC: strPtr("low")},
		ChannelVolumes: map[string]float64{"FL": 0.5},
	}
	mustSucceed(t, harness.client.ApplySettings(want))
	harness.receiver.waitForCommands(t, "CVFL 505")

	got := waitForSettings(t, harness.client, func(s Settings) bool {
		return s.System.Eco != nil && *s.System.Eco == "auto" &&
			s.System.Dimmer != nil && *s.System.Dimmer == "bright" &&
			s.Tone.Bass != nil && *s.Tone.Bass == 3 &&
			s.Audio.LFE != nil && *s.Audio.LFE == -10 &&
			s.Audio.DRC != nil && *s.Audio.DRC == "low" &&
			s.ChannelVolumes["FL"] == 0.5
	})
	mustMatch(t, *got.System.Eco, "auto")
	mustMatch(t, *got.System.Dimmer, "bright")
	mustMatch(t, *got.Tone.Bass, 3)
	mustMatch(t, *got.Audio.LFE, -10)
	mustMatch(t, *got.Audio.DRC, "low")
	mustMatch(t, got.ChannelVolumes["FL"], 0.5)
}

func TestSetRoutesOneIdToTheWire(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	mustSucceed(t, harness.client.Set("tone.bass", equipment.NumberSettingValue(3)))

	mustMatch(t, harness.receiver.waitForCommand(t), "PSBAS 53")

	// The live setting moves on the receiver's echo, not on the write:
	// a command the receiver never took must not change what the status
	// reports.
	got := waitForSettings(t, harness.client, func(s Settings) bool {
		return s.Tone.Bass != nil && *s.Tone.Bass == 3
	})
	mustMatch(t, *got.Tone.Bass, 3)
}

func TestSetRoutesAChannelIdToTheWire(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	mustSucceed(t, harness.client.Set("channel.FL", equipment.NumberSettingValue(0.5)))

	mustMatch(t, harness.receiver.waitForCommand(t), "CVFL 505")

	got := waitForSettings(t, harness.client, func(s Settings) bool {
		return s.ChannelVolumes["FL"] == 0.5
	})
	mustMatch(t, got.ChannelVolumes["FL"], 0.5)
}

func TestSetUnknownIdIsAnErrorThatNamesIt(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("tone.bogus", equipment.NumberSettingValue(3))
	if err == nil {
		t.Fatal("an unknown id did not error")
	}
	if !strings.Contains(err.Error(), "tone.bogus") {
		t.Errorf("the error does not name the id: %v", err)
	}
}

func TestSetWrongValueTypeIsAnError(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("tone.bass", equipment.StringSettingValue("loud"))
	if err == nil {
		t.Fatal("a string on a numeric setting did not error")
	}
	if !strings.Contains(err.Error(), "tone.bass") {
		t.Errorf("the error does not name the id: %v", err)
	}
}

func TestSetAnUnknownWordIsAnError(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("system.eco", equipment.StringSettingValue("turbo"))
	if err == nil {
		t.Fatal("an unknown eco mode did not error")
	}
}

func TestDoHasNoActionsYet(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Do("quick.3", nil)
	if err == nil {
		t.Fatal("Do did not error")
	}
	if !strings.Contains(err.Error(), "quick.3") {
		t.Errorf("the error does not name the id: %v", err)
	}
}

func TestSetAChannelWithTheWrongTypeIsAnError(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("channel.FL", equipment.StringSettingValue("loud"))
	if err == nil {
		t.Fatal("a string on a channel volume did not error")
	}
}

func TestSetAChannelWithNoNameIsAnError(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("channel.", equipment.NumberSettingValue(0))
	if err == nil {
		t.Fatal("a channel volume with no name did not error")
	}
}

// setChannel builds a row from a Settings with no channel map, so the
// nil map branch of the fold runs.
func TestSetChannelStartsTheChannelMap(t *testing.T) {
	s := Settings{}
	command, err := setChannel(&s, "FL", equipment.NumberSettingValue(0.5))
	mustSucceed(t, err)
	mustMatch(t, command, "CVFL 505")
	mustMatch(t, s.ChannelVolumes["FL"], 0.5)
}

// ApplySettings must not report success for a declared field no command
// can carry; it errors naming the setting id and the value instead.
func TestApplySettingsErrorsWhenADeclaredFieldCannotBeEncoded(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	err := harness.client.ApplySettings(Settings{System: SystemSettings{Eco: strPtr("turbo")}})
	if err == nil {
		t.Fatal("a declared field no command can carry did not error")
	}
	if !strings.Contains(err.Error(), "system.eco") {
		t.Errorf("the error does not name the id: %v", err)
	}
	if !strings.Contains(err.Error(), "turbo") {
		t.Errorf("the error does not name the value: %v", err)
	}
}

// waitForSettings polls the client's settings until the check passes,
// and fails the test instead of hanging when it never does.
func waitForSettings(t *testing.T, client *Client, check func(Settings) bool) Settings {
	t.Helper()
	deadline := time.After(testTimeout)
	for {
		got := client.Settings()
		if check(got) {
			return got
		}
		select {
		case <-deadline:
			t.Fatal("the settings never reached the declared values")
			return Settings{}
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Every word a builder accepts must round-trip through the parser's
// word function, so a value the operator sends is one the status can
// re-assert.
func TestEveryBuilderWordRoundTripsThroughItsParser(t *testing.T) {
	cases := []struct {
		name    string
		build   func(string) (string, error)
		command string
		get     func(Settings) *string
		want    string
	}{
		{"multEQ reference", MultEqCommand, "PSMULTEQ:AUDYSSEY", func(s Settings) *string { return s.Audyssey.Multeq }, "reference"},
		{"multEQ bypass", MultEqCommand, "PSMULTEQ:BYP.LR", func(s Settings) *string { return s.Audyssey.Multeq }, "l/r bypass"},
		{"multEQ flat", MultEqCommand, "PSMULTEQ:FLAT", func(s Settings) *string { return s.Audyssey.Multeq }, "flat"},
		{"multEQ manual", MultEqCommand, "PSMULTEQ:MANUAL", func(s Settings) *string { return s.Audyssey.Multeq }, "manual"},
		{"multEQ off", MultEqCommand, "PSMULTEQ:OFF", func(s Settings) *string { return s.Audyssey.Multeq }, "off"},
		{"dynamic volume off", DynamicVolumeCommand, "PSDYNVOL OFF", func(s Settings) *string { return s.Audyssey.DynamicVolume }, "off"},
		{"dynamic volume light", DynamicVolumeCommand, "PSDYNVOL LIT", func(s Settings) *string { return s.Audyssey.DynamicVolume }, "light"},
		{"dynamic volume medium", DynamicVolumeCommand, "PSDYNVOL MED", func(s Settings) *string { return s.Audyssey.DynamicVolume }, "medium"},
		{"dynamic volume heavy", DynamicVolumeCommand, "PSDYNVOL HEV", func(s Settings) *string { return s.Audyssey.DynamicVolume }, "heavy"},
		{"restorer off", RestorerCommand, "PSRSTR OFF", func(s Settings) *string { return s.Audio.Restorer }, "off"},
		{"restorer low", RestorerCommand, "PSRSTR LOW", func(s Settings) *string { return s.Audio.Restorer }, "low"},
		{"restorer medium", RestorerCommand, "PSRSTR MED", func(s Settings) *string { return s.Audio.Restorer }, "medium"},
		{"restorer high", RestorerCommand, "PSRSTR HI", func(s Settings) *string { return s.Audio.Restorer }, "high"},
		{"dynamic range off", DynamicRangeCommand, "PSDRC OFF", func(s Settings) *string { return s.Audio.DRC }, "off"},
		{"dynamic range low", DynamicRangeCommand, "PSDRC LOW", func(s Settings) *string { return s.Audio.DRC }, "low"},
		{"dynamic range mid", DynamicRangeCommand, "PSDRC MID", func(s Settings) *string { return s.Audio.DRC }, "mid"},
		{"dynamic range hi", DynamicRangeCommand, "PSDRC HI", func(s Settings) *string { return s.Audio.DRC }, "hi"},
		{"dynamic range auto", DynamicRangeCommand, "PSDRC AUTO", func(s Settings) *string { return s.Audio.DRC }, "auto"},
		{"dialog enhancer off", DialogEnhancerCommand, "PSDEH OFF", func(s Settings) *string { return s.Audio.DialogEnhancer }, "off"},
		{"dialog enhancer low", DialogEnhancerCommand, "PSDEH LOW", func(s Settings) *string { return s.Audio.DialogEnhancer }, "low"},
		{"dialog enhancer mid", DialogEnhancerCommand, "PSDEH MID", func(s Settings) *string { return s.Audio.DialogEnhancer }, "mid"},
		{"dialog enhancer high", DialogEnhancerCommand, "PSDEH HIGH", func(s Settings) *string { return s.Audio.DialogEnhancer }, "high"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			command, err := one.build(one.want)
			mustSucceed(t, err)
			mustMatch(t, command, one.command)

			state, _, _, known := applyDenonLine(newDenonState(), one.command)
			mustMatch(t, known, true)
			mustMatch(t, *one.get(state.Settings), one.want)
		})
	}
}

// A word the receiver sends that no builder accepts leaves its field
// nil rather than hold a value the operator could never send back.
func TestAnUnknownWordLeavesItsFieldUnset(t *testing.T) {
	cases := []struct {
		line string
		get  func(Settings) *string
	}{
		{"PSMULTEQ:MAGIC", func(s Settings) *string { return s.Audyssey.Multeq }},
		{"PSDYNVOL BLARING", func(s Settings) *string { return s.Audyssey.DynamicVolume }},
		{"PSRSTR SOFT", func(s Settings) *string { return s.Audio.Restorer }},
		{"PSDRC EXTREME", func(s Settings) *string { return s.Audio.DRC }},
		{"PSDEH SUPER", func(s Settings) *string { return s.Audio.DialogEnhancer }},
	}
	for _, one := range cases {
		t.Run(one.line, func(t *testing.T) {
			state, _, _, known := applyDenonLine(newDenonState(), one.line)
			mustMatch(t, known, false)
			mustMatch(t, one.get(state.Settings), (*string)(nil))
		})
	}
}

// An integer setting must not silently truncate a fraction; the error
// names the id so a caller knows which value was rejected.
func TestSetRejectsAFractionalInteger(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("tone.bass", equipment.NumberSettingValue(3.7))
	if err == nil {
		t.Fatal("a fractional value on an integer setting did not error")
	}
	if !strings.Contains(err.Error(), "tone.bass") {
		t.Errorf("the error does not name the id: %v", err)
	}
}

func TestSetAChannelWithAnExtraDotIsAnError(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("channel.FL.extra", equipment.NumberSettingValue(0))
	if err == nil {
		t.Fatal("a channel id with an extra dot did not error")
	}
	if !strings.Contains(err.Error(), "channel.FL.extra") {
		t.Errorf("the error does not name the id: %v", err)
	}
}

func TestSetAChannelWithWhitespaceIsAnError(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("channel. FL", equipment.NumberSettingValue(0))
	if err == nil {
		t.Fatal("a channel id with whitespace did not error")
	}
}

// Mutating a returned Settings must not change the next Settings():
// every pointer and the channel map are deep-copied.
func TestSettingsDoesNotAliasTheLiveState(t *testing.T) {
	harness := startHarness(t)
	drainQueries(t, harness)

	// The fake reports SVOFF, PSDRC OFF, and PSLFE 00, so these are
	// live pointers a shallow copy would share. Wait until they have
	// folded, since they arrive after the queries the test drains.
	waitForSettings(t, harness.client, func(s Settings) bool {
		return s.System.VideoSelect != nil && s.Audio.DRC != nil && s.Audio.LFE != nil
	})
	first := harness.client.Settings()
	*first.System.VideoSelect = "mutated"
	*first.Audio.DRC = "mutated"
	*first.Audio.LFE = 99
	first.ChannelVolumes["FL"] = 99

	second := harness.client.Settings()
	mustMatch(t, *second.System.VideoSelect, "off")
	mustMatch(t, *second.Audio.DRC, "off")
	mustMatch(t, *second.Audio.LFE, 0)
	_, held := second.ChannelVolumes["FL"]
	mustMatch(t, held, false)
}

// A command that cannot be delivered is reported to the caller, so a
// set on a client with no connection is an error and not a silent no-op.
func TestSetOnADisconnectedClientReportsTheFailure(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.Set("tone.bass", equipment.NumberSettingValue(3))
	if err == nil {
		t.Fatal("a set on a disconnected client did not error")
	}
}

func TestApplySettingsOnADisconnectedClientReportsTheFailure(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.ApplySettings(Settings{Tone: ToneSettings{Bass: intPtr(3)}})
	if err == nil {
		t.Fatal("an apply on a disconnected client did not error")
	}
}

// Every field without a round-trip test travels the apply path: it
// reaches the wire, the receiver echoes it, and the folded state reads
// it back, all against the receiver rather than the code's own builder.
func TestApplySettingsRoundTripsEveryRemainingField(t *testing.T) {
	cases := []struct {
		name    string
		want    Settings
		command string
		check   func(Settings) bool
	}{
		{"video select", Settings{System: SystemSettings{VideoSelect: strPtr("off")}}, "SVOFF",
			func(s Settings) bool { return s.System.VideoSelect != nil && *s.System.VideoSelect == "off" }},
		{"bluetooth transmitter", Settings{System: SystemSettings{BluetoothTransmitter: strPtr("on")}}, "BTTX ON",
			func(s Settings) bool {
				return s.System.BluetoothTransmitter != nil && *s.System.BluetoothTransmitter == "on"
			}},
		{"bluetooth output", Settings{System: SystemSettings{BluetoothOutput: strPtr("speakers")}}, "BTTX SP",
			func(s Settings) bool {
				return s.System.BluetoothOutput != nil && *s.System.BluetoothOutput == "speakers"
			}},
		{"multEQ", Settings{Audyssey: AudysseySettings{Multeq: strPtr("flat")}}, "PSMULTEQ:FLAT",
			func(s Settings) bool { return s.Audyssey.Multeq != nil && *s.Audyssey.Multeq == "flat" }},
		{"effect", Settings{Audio: AudioSettings{Effect: intPtr(5)}}, "PSEFF 05",
			func(s Settings) bool { return s.Audio.Effect != nil && *s.Audio.Effect == 5 }},
		{"delay", Settings{Audio: AudioSettings{Delay: intPtr(100)}}, "PSDEL 100",
			func(s Settings) bool { return s.Audio.Delay != nil && *s.Audio.Delay == 100 }},
		{"audio delay", Settings{Audio: AudioSettings{AudioDelay: intPtr(200)}}, "PSDELAY 200",
			func(s Settings) bool { return s.Audio.AudioDelay != nil && *s.Audio.AudioDelay == 200 }},
		{"subwoofer", Settings{Audio: AudioSettings{Subwoofer: boolPtr(true)}}, "PSSWR ON",
			func(s Settings) bool { return s.Audio.Subwoofer != nil && *s.Audio.Subwoofer }},
		{"restorer", Settings{Audio: AudioSettings{Restorer: strPtr("low")}}, "PSRSTR LOW",
			func(s Settings) bool { return s.Audio.Restorer != nil && *s.Audio.Restorer == "low" }},
		{"headphone EQ", Settings{Audio: AudioSettings{HeadphoneEq: strPtr("on")}}, "PSHEQ ON",
			func(s Settings) bool { return s.Audio.HeadphoneEq != nil && *s.Audio.HeadphoneEq == "on" }},
		{"speaker virtualizer", Settings{Audio: AudioSettings{SpeakerVirtualizer: boolPtr(true)}}, "PSSPV ON",
			func(s Settings) bool { return s.Audio.SpeakerVirtualizer != nil && *s.Audio.SpeakerVirtualizer }},
		{"dialog enhancer", Settings{Audio: AudioSettings{DialogEnhancer: strPtr("mid")}}, "PSDEH MID",
			func(s Settings) bool { return s.Audio.DialogEnhancer != nil && *s.Audio.DialogEnhancer == "mid" }},
		{"treble", Settings{Tone: ToneSettings{Treble: intPtr(-6)}}, "PSTRE 44",
			func(s Settings) bool { return s.Tone.Treble != nil && *s.Tone.Treble == -6 }},
		{"tone control", Settings{Tone: ToneSettings{Control: boolPtr(true)}}, "PSTONE CTRL ON",
			func(s Settings) bool { return s.Tone.Control != nil && *s.Tone.Control }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			harness := startHarness(t)
			drainQueries(t, harness)

			mustSucceed(t, harness.client.ApplySettings(one.want))
			harness.receiver.waitForCommands(t, one.command)

			waitForSettings(t, harness.client, one.check)
		})
	}
}
