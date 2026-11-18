package denon

import (
	"reflect"
	"testing"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// newStateWith returns newDenonState with change applied.
func newStateWith(change func(*denonState)) denonState {
	state := newDenonState()
	change(&state)
	return state
}

// mustMatchDenonState uses reflect.DeepEqual because denonState holds a
// map and cannot be compared with ==.
func mustMatchDenonState(t *testing.T, got, want denonState) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the state is %+v, want %+v", got, want)
	}
}

func TestApplyDenonLineUpdatesTheStateFromEachLine(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		zone  string
		field string
		want  denonState
	}{
		{name: "power on", line: "PWON", zone: equipment.MainZone, field: powerField,
			want: newStateWith(func(s *denonState) {
				s.Settings.System.Power = powerOn
				s.Main.Power = equipment.PowerOn
				s.Main.seen = true
			})},
		{name: "power to standby", line: "PWSTANDBY", zone: equipment.MainZone, field: powerField,
			want: newStateWith(func(s *denonState) {
				s.Settings.System.Power = powerStandby
				s.Main.Power = equipment.PowerStandby
				s.Main.seen = true
			})},
		{name: "main zone on", line: "ZMON", zone: equipment.MainZone, field: powerField,
			want: newStateWith(func(s *denonState) { s.Main.Power = equipment.PowerOn; s.Main.seen = true; s.mainFromZM = true })},
		{name: "main zone off", line: "ZMOFF", zone: equipment.MainZone, field: powerField,
			want: newStateWith(func(s *denonState) { s.Main.Power = equipment.PowerStandby; s.Main.seen = true; s.mainFromZM = true })},
		{name: "a whole volume", line: "MV50", zone: equipment.MainZone, field: volumeField,
			want: newStateWith(func(s *denonState) { s.Main.Volume = 100; s.Main.seen = true })},
		{name: "a half volume", line: "MV505", zone: equipment.MainZone, field: volumeField,
			want: newStateWith(func(s *denonState) { s.Main.Volume = 101; s.Main.seen = true })},
		{name: "the volume limit", line: "MVMAX 695", zone: equipment.MainZone, field: volumeMaxField,
			want: newStateWith(func(s *denonState) { s.Main.VolumeMax = 139; s.Main.seen = true })},
		{name: "mute on", line: "MUON", zone: equipment.MainZone, field: muteField,
			want: newStateWith(func(s *denonState) { s.Main.Mute = true; s.Main.seen = true })},
		{name: "mute off", line: "MUOFF", zone: equipment.MainZone, field: muteField,
			want: newStateWith(func(s *denonState) { s.Main.Mute = false; s.Main.seen = true })},
		{name: "an input", line: "SIMPLAY", zone: equipment.MainZone, field: inputField,
			want: newStateWith(func(s *denonState) { s.Main.Input = "MPLAY"; s.Main.seen = true })},
		{name: "an input with a slash", line: "SICBL/SAT", zone: equipment.MainZone, field: inputField,
			want: newStateWith(func(s *denonState) { s.Main.Input = "CBL/SAT"; s.Main.seen = true })},
		{name: "a sound mode with a space", line: "MSMULTI CH IN", zone: equipment.MainZone, field: soundModeField,
			want: newStateWith(func(s *denonState) { s.Main.SoundMode = "MULTI CH IN"; s.Main.seen = true })},
		{name: "a sound mode that ends in spaces", line: "MSDOLBY AUDIO-DD  ", zone: equipment.MainZone, field: soundModeField,
			want: newStateWith(func(s *denonState) { s.Main.SoundMode = "DOLBY AUDIO-DD"; s.Main.seen = true })},
		{name: "a sound mode", line: "MSSTEREO", zone: equipment.MainZone, field: soundModeField,
			want: newStateWith(func(s *denonState) { s.Main.SoundMode = "STEREO"; s.Main.seen = true })},
		{name: "a quick select", line: "MSQUICK2", zone: equipment.MainZone, field: quickField,
			want: newStateWith(func(s *denonState) { s.Main.Quick = 2; s.Main.seen = true })},
		{name: "a sleep timer", line: "SLP030", zone: equipment.MainZone, field: sleepField,
			want: newStateWith(func(s *denonState) { s.Main.Sleep = 30; s.Main.seen = true })},
		{name: "the second zone on", line: "Z2ON", zone: zone2, field: powerField,
			want: newStateWith(func(s *denonState) { s.Zone2.Power = equipment.PowerOn; s.Zone2.seen = true })},
		{name: "the second zone volume", line: "Z290", zone: zone2, field: volumeField,
			want: newStateWith(func(s *denonState) { s.Zone2.Volume = 180; s.Zone2.seen = true })},
		{name: "the second zone input", line: "Z2PHONO", zone: zone2, field: inputField,
			want: newStateWith(func(s *denonState) { s.Zone2.Input = "PHONO"; s.Zone2.seen = true })},
		{name: "the second zone mute", line: "Z2MUOFF", zone: zone2, field: muteField,
			want: newStateWith(func(s *denonState) { s.Zone2.Mute = false; s.Zone2.seen = true })},
		{name: "the eco mode", line: "ECOAUTO", zone: "", field: ecoField,
			want: newStateWith(func(s *denonState) { s.Settings.System.Eco = strPtr("auto") })},
		{name: "the dimmer", line: "DIM BRI", zone: "", field: dimmerField,
			want: newStateWith(func(s *denonState) { s.Settings.System.Dimmer = strPtr("bright") })},
		{name: "the auto standby timer", line: "STBY2H", zone: "", field: standbyField,
			want: newStateWith(func(s *denonState) { s.Settings.System.AutoStandby = strPtr("2h") })},
		{name: "the speaker preset", line: "SPPR 1", zone: "", field: speakerPresetField,
			want: newStateWith(func(s *denonState) { s.Settings.System.SpeakerPreset = intPtr(1) })},
		{name: "the audio input mode", line: "SDHDMI", zone: "", field: inputModeField,
			want: newStateWith(func(s *denonState) { s.Settings.System.AudioInputMode = strPtr("hdmi") })},
		{name: "the video select", line: "SVOFF", zone: "", field: videoSelectField,
			want: newStateWith(func(s *denonState) { s.Settings.System.VideoSelect = strPtr("off") })},
		{name: "the bluetooth transmitter off", line: "BTTX OFF", zone: "", field: bluetoothField,
			want: newStateWith(func(s *denonState) { s.Settings.System.BluetoothTransmitter = strPtr("off") })},
		{name: "the bluetooth output", line: "BTTX SP", zone: "", field: bluetoothField,
			want: newStateWith(func(s *denonState) { s.Settings.System.BluetoothOutput = strPtr("speakers") })},
		{name: "a channel volume at zero", line: "CVFL 50", zone: "", field: channelField,
			want: newStateWith(func(s *denonState) { s.Settings.ChannelVolumes["FL"] = 0 })},
		{name: "a channel volume above zero", line: "CVSW 505", zone: "", field: channelField,
			want: newStateWith(func(s *denonState) { s.Settings.ChannelVolumes["SW"] = 0.5 })},
		{name: "the Audyssey mode", line: "PSMULTEQ:AUDYSSEY", zone: "", field: multeqField,
			want: newStateWith(func(s *denonState) { s.Settings.Audyssey.Multeq = strPtr("reference") })},
		{name: "the dynamic EQ", line: "PSDYNEQ ON", zone: "", field: dynamicEqField,
			want: newStateWith(func(s *denonState) { s.Settings.Audyssey.DynamicEq = boolPtr(true) })},
		{name: "the reference level", line: "PSREFLEV 10", zone: "", field: referenceLevelField,
			want: newStateWith(func(s *denonState) { s.Settings.Audyssey.ReferenceLevelOffset = intPtr(10) })},
		{name: "the dynamic volume", line: "PSDYNVOL OFF", zone: "", field: dynamicVolumeField,
			want: newStateWith(func(s *denonState) { s.Settings.Audyssey.DynamicVolume = strPtr("off") })},
		{name: "the loudness management", line: "PSLOM ON", zone: "", field: loudnessField,
			want: newStateWith(func(s *denonState) { s.Settings.Audyssey.LoudnessManagement = boolPtr(true) })},
		{name: "the dynamic range", line: "PSDRC OFF", zone: "", field: drcField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.DRC = strPtr("off") })},
		{name: "the LFE level", line: "PSLFE 10", zone: "", field: lfeField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.LFE = intPtr(-10) })},
		{name: "the effect level", line: "PSEFF 05", zone: "", field: effectField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.Effect = intPtr(5) })},
		{name: "the audio delay", line: "PSDELAY 200", zone: "", field: delayField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.AudioDelay = intPtr(200) })},
		{name: "the delay", line: "PSDEL 100", zone: "", field: delayField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.Delay = intPtr(100) })},
		{name: "the subwoofer", line: "PSSWR ON", zone: "", field: subwooferField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.Subwoofer = boolPtr(true) })},
		{name: "the restorer", line: "PSRSTR LOW", zone: "", field: restorerField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.Restorer = strPtr("low") })},
		{name: "the graphic equalizer", line: "PSGEQ OFF", zone: "", field: graphicEqField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.GraphicEq = strPtr("off") })},
		{name: "the headphone equalizer", line: "PSHEQ OFF", zone: "", field: headphoneEqField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.HeadphoneEq = strPtr("off") })},
		{name: "the speaker virtualizer", line: "PSSPV ON", zone: "", field: virtualizerField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.SpeakerVirtualizer = boolPtr(true) })},
		{name: "the dialog enhancer", line: "PSDEH OFF", zone: "", field: dialogEnhancerField,
			want: newStateWith(func(s *denonState) { s.Settings.Audio.DialogEnhancer = strPtr("off") })},
		{name: "the bass trim", line: "PSBAS 44", zone: "", field: bassField,
			want: newStateWith(func(s *denonState) { s.Settings.Tone.Bass = intPtr(-6) })},
		{name: "the treble trim", line: "PSTRE 56", zone: "", field: trebleField,
			want: newStateWith(func(s *denonState) { s.Settings.Tone.Treble = intPtr(6) })},
		{name: "the tone control", line: "PSTONE CTRL OFF", zone: "", field: toneControlField,
			want: newStateWith(func(s *denonState) { s.Settings.Tone.Control = boolPtr(false) })},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state, zone, field, known := applyDenonLine(newDenonState(), test.line)
			mustMatch(t, known, true)
			mustMatch(t, zone, test.zone)
			mustMatch(t, field, test.field)
			mustMatchDenonState(t, state, test.want)
		})
	}
}

func TestApplyDenonLineIgnoresLinesTheDriverDoesNotRead(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "the digital audio mode", line: "DCAUTO"},
		{name: "the signal info", line: "SSINFAISSIG 13"},
		{name: "the input capability bitmap", line: "OPINFINS 1111"},
		{name: "a volume that is not digits", line: "MVFOO"},
		{name: "an input with no name", line: "SI"},
		{name: "a sound mode with no name", line: "MS"},
		{name: "an empty line", line: ""},
		{name: "the channel volume end marker", line: "CVEND"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state, zone, field, known := applyDenonLine(newDenonState(), test.line)
			mustMatch(t, known, false)
			mustMatch(t, zone, "")
			mustMatch(t, field, "")
			mustMatchDenonState(t, state, newDenonState())
		})
	}
}

// MVMAX is the maximum volume set in the receiver's menu, and the
// receiver sends it right after the MV line. It starts with MV, so a
// parser that read it as a volume would report the maximum as the
// current volume.
func TestVolumeLimitLineDoesNotChangeTheVolume(t *testing.T) {
	state, _, _, _ := applyDenonLine(newDenonState(), "MV50")
	state, _, field, known := applyDenonLine(state, "MVMAX 695")

	mustMatch(t, known, true)
	mustMatch(t, field, volumeMaxField)
	mustMatch(t, state.Main.Volume, 100)
	mustMatch(t, state.Main.VolumeMax, 139)
}

// PW is the power of the whole unit and ZM is the power of the main
// zone. A model that sends no ZM line reports the main zone through PW,
// so applyDenonLine uses PW for the main zone only until a ZM line
// arrives.
func TestWholeUnitPowerLineKeepsTheMainZonePower(t *testing.T) {
	state, _, _, _ := applyDenonLine(newDenonState(), "ZMOFF")
	state, _, _, _ = applyDenonLine(state, "PWON")

	mustMatch(t, state.Main.Power, equipment.PowerStandby)
	mustMatch(t, state.Settings.System.Power, powerOn)
}

func TestVolumeCommandWritesHalfSteps(t *testing.T) {
	cases := []struct {
		name   string
		halves int
		want   string
	}{
		{name: "a whole step", halves: 100, want: "MV50"},
		{name: "a half step", halves: 101, want: "MV505"},
		{name: "a single digit step", halves: 10, want: "MV05"},
		{name: "the limit", halves: 139, want: "MV695"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mustMatch(t, VolumeCommand(test.halves), test.want)
		})
	}
}

func TestMuteInputAndSoundModeCommandsWriteTheirValues(t *testing.T) {
	mustMatch(t, MuteCommand(true), "MUON")
	mustMatch(t, MuteCommand(false), "MUOFF")
	mustMatch(t, InputCommand("MPLAY"), "SIMPLAY")
	mustMatch(t, InputCommand("CBL/SAT"), "SICBL/SAT")
	mustMatch(t, SoundModeCommand("STEREO"), "MSSTEREO")
	mustMatch(t, SoundModeCommand("MULTI CH IN"), "MSMULTI CH IN")
}

func TestDenonAddressAddsThePortWhenMissing(t *testing.T) {
	cases := []struct {
		name    string
		address string
		want    string
	}{
		{name: "a bare address", address: "192.168.1.5", want: "192.168.1.5:23"},
		{name: "an address with a port", address: "192.168.1.5:8023", want: "192.168.1.5:8023"},
		{name: "a bare host name", address: "receiver.local", want: "receiver.local:23"},
		{name: "a bare ipv6 literal", address: "fd00::1", want: "[fd00::1]:23"},
		{name: "an ipv6 literal with a port", address: "[fd00::1]:8023", want: "[fd00::1]:8023"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mustMatch(t, denonAddress(test.address), test.want)
		})
	}
}

func TestEquipmentFieldNamesMatchTheContract(t *testing.T) {
	cases := []struct {
		field string
		want  string
	}{
		{field: powerField, want: equipment.EventPower},
		{field: inputField, want: equipment.EventInput},
		{field: volumeField, want: equipment.EventVolume},
		{field: volumeMaxField, want: equipment.EventVolume},
		{field: muteField, want: equipment.EventMute},
		{field: soundModeField, want: equipment.EventSoundMode},
		{field: sleepField, want: ""},
		{field: ecoField, want: ""},
		{field: "unknown", want: ""},
	}
	for _, test := range cases {
		t.Run(test.field, func(t *testing.T) {
			mustMatch(t, equipmentField(test.field), test.want)
		})
	}
}
