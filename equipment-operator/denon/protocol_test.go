// The Denon control protocol as the AVR-X1700H speaks it.

package denon

import (
	"reflect"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

// foldedInto is the state before the receiver has said anything, with
// the one change a line makes applied to it.
func foldedInto(change func(*denonState)) denonState {
	state := newDenonState()
	change(&state)
	return state
}

// mustMatchState compares two Denon states, which carry a map and so
// cannot be compared with ==.
func mustMatchDenonState(t *testing.T, got, want denonState) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestApplyDenonLineFoldsEveryLineTheReceiverSends(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		zone  string
		field string
		want  denonState
	}{
		{name: "power on", line: "PWON", zone: equipment.MainZone, field: powerField,
			want: foldedInto(func(s *denonState) { s.System.Power = powerOn; s.Main.Power = equipment.PowerOn; s.Main.seen = true })},
		{name: "power to standby", line: "PWSTANDBY", zone: equipment.MainZone, field: powerField,
			want: foldedInto(func(s *denonState) {
				s.System.Power = powerStandby
				s.Main.Power = equipment.PowerStandby
				s.Main.seen = true
			})},
		{name: "main zone on", line: "ZMON", zone: equipment.MainZone, field: powerField,
			want: foldedInto(func(s *denonState) { s.Main.Power = equipment.PowerOn; s.Main.seen = true; s.mainFromZM = true })},
		{name: "main zone off", line: "ZMOFF", zone: equipment.MainZone, field: powerField,
			want: foldedInto(func(s *denonState) { s.Main.Power = equipment.PowerStandby; s.Main.seen = true; s.mainFromZM = true })},
		{name: "a whole volume", line: "MV50", zone: equipment.MainZone, field: volumeField,
			want: foldedInto(func(s *denonState) { s.Main.Volume = 100; s.Main.seen = true })},
		{name: "a half volume", line: "MV505", zone: equipment.MainZone, field: volumeField,
			want: foldedInto(func(s *denonState) { s.Main.Volume = 101; s.Main.seen = true })},
		{name: "the volume limit", line: "MVMAX 695", zone: equipment.MainZone, field: volumeMaxField,
			want: foldedInto(func(s *denonState) { s.Main.VolumeMax = 139; s.Main.seen = true })},
		{name: "mute on", line: "MUON", zone: equipment.MainZone, field: muteField,
			want: foldedInto(func(s *denonState) { s.Main.Mute = true; s.Main.seen = true })},
		{name: "mute off", line: "MUOFF", zone: equipment.MainZone, field: muteField,
			want: foldedInto(func(s *denonState) { s.Main.Mute = false; s.Main.seen = true })},
		{name: "an input", line: "SIMPLAY", zone: equipment.MainZone, field: inputField,
			want: foldedInto(func(s *denonState) { s.Main.Input = "MPLAY"; s.Main.seen = true })},
		{name: "an input with a slash", line: "SICBL/SAT", zone: equipment.MainZone, field: inputField,
			want: foldedInto(func(s *denonState) { s.Main.Input = "CBL/SAT"; s.Main.seen = true })},
		{name: "a sound mode with a space", line: "MSMULTI CH IN", zone: equipment.MainZone, field: soundModeField,
			want: foldedInto(func(s *denonState) { s.Main.SoundMode = "MULTI CH IN"; s.Main.seen = true })},
		{name: "a sound mode", line: "MSSTEREO", zone: equipment.MainZone, field: soundModeField,
			want: foldedInto(func(s *denonState) { s.Main.SoundMode = "STEREO"; s.Main.seen = true })},
		{name: "a quick select", line: "MSQUICK2", zone: equipment.MainZone, field: quickField,
			want: foldedInto(func(s *denonState) { s.Main.Quick = 2; s.Main.seen = true })},
		{name: "a sleep timer", line: "SLP030", zone: equipment.MainZone, field: sleepField,
			want: foldedInto(func(s *denonState) { s.Main.Sleep = 30; s.Main.seen = true })},
		{name: "the second zone on", line: "Z2ON", zone: zone2, field: powerField,
			want: foldedInto(func(s *denonState) { s.Zone2.Power = equipment.PowerOn; s.Zone2.seen = true })},
		{name: "the second zone volume", line: "Z290", zone: zone2, field: volumeField,
			want: foldedInto(func(s *denonState) { s.Zone2.Volume = 180; s.Zone2.seen = true })},
		{name: "the second zone input", line: "Z2PHONO", zone: zone2, field: inputField,
			want: foldedInto(func(s *denonState) { s.Zone2.Input = "PHONO"; s.Zone2.seen = true })},
		{name: "the second zone mute", line: "Z2MUOFF", zone: zone2, field: muteField,
			want: foldedInto(func(s *denonState) { s.Zone2.Mute = false; s.Zone2.seen = true })},
		{name: "the eco mode", line: "ECOAUTO", zone: "", field: ecoField,
			want: foldedInto(func(s *denonState) { s.System.Eco = "auto" })},
		{name: "the dimmer", line: "DIM BRI", zone: "", field: dimmerField,
			want: foldedInto(func(s *denonState) { s.System.Dimmer = "bright" })},
		{name: "the auto standby timer", line: "STBY2H", zone: "", field: standbyField,
			want: foldedInto(func(s *denonState) { s.System.AutoStandby = "2h" })},
		{name: "the speaker preset", line: "SPPR 1", zone: "", field: speakerPresetField,
			want: foldedInto(func(s *denonState) { s.System.SpeakerPreset = 1 })},
		{name: "the audio input mode", line: "SDHDMI", zone: "", field: inputModeField,
			want: foldedInto(func(s *denonState) { s.System.AudioInputMode = "hdmi" })},
		{name: "the video select", line: "SVOFF", zone: "", field: videoSelectField,
			want: foldedInto(func(s *denonState) { s.System.VideoSelect = "off" })},
		{name: "the bluetooth transmitter off", line: "BTTX OFF", zone: "", field: bluetoothField,
			want: foldedInto(func(s *denonState) { s.System.BluetoothTransmitter = "off" })},
		{name: "the bluetooth output", line: "BTTX SP", zone: "", field: bluetoothField,
			want: foldedInto(func(s *denonState) { s.System.BluetoothOutput = "speakers" })},
		{name: "a channel volume at zero", line: "CVFL 50", zone: "", field: channelField,
			want: foldedInto(func(s *denonState) { s.Channels["FL"] = 0 })},
		{name: "a channel volume above zero", line: "CVSW 505", zone: "", field: channelField,
			want: foldedInto(func(s *denonState) { s.Channels["SW"] = 0.5 })},
		{name: "the Audyssey mode", line: "PSMULTEQ:AUDYSSEY", zone: "", field: multeqField,
			want: foldedInto(func(s *denonState) { s.Audyssey.Multeq = "reference" })},
		{name: "the dynamic EQ", line: "PSDYNEQ ON", zone: "", field: dynamicEqField,
			want: foldedInto(func(s *denonState) { s.Audyssey.DynamicEq = true })},
		{name: "the reference level", line: "PSREFLEV 10", zone: "", field: referenceLevelField,
			want: foldedInto(func(s *denonState) { s.Audyssey.ReferenceLevelOffset = 10 })},
		{name: "the dynamic volume", line: "PSDYNVOL OFF", zone: "", field: dynamicVolumeField,
			want: foldedInto(func(s *denonState) { s.Audyssey.DynamicVolume = "off" })},
		{name: "the loudness management", line: "PSLOM ON", zone: "", field: loudnessField,
			want: foldedInto(func(s *denonState) { s.Audyssey.LoudnessManagement = true })},
		{name: "the dynamic range", line: "PSDRC OFF", zone: "", field: drcField,
			want: foldedInto(func(s *denonState) { s.Audio.DRC = "off" })},
		{name: "the LFE level", line: "PSLFE 10", zone: "", field: lfeField,
			want: foldedInto(func(s *denonState) { s.Audio.LFE = -10 })},
		{name: "the effect level", line: "PSEFF 05", zone: "", field: effectField,
			want: foldedInto(func(s *denonState) { s.Audio.Effect = 5 })},
		{name: "the audio delay", line: "PSDELAY 200", zone: "", field: delayField,
			want: foldedInto(func(s *denonState) { s.Audio.AudioDelay = 200 })},
		{name: "the delay", line: "PSDEL 100", zone: "", field: delayField,
			want: foldedInto(func(s *denonState) { s.Audio.Delay = 100 })},
		{name: "the subwoofer", line: "PSSWR ON", zone: "", field: subwooferField,
			want: foldedInto(func(s *denonState) { s.Audio.Subwoofer = true })},
		{name: "the restorer", line: "PSRSTR LOW", zone: "", field: restorerField,
			want: foldedInto(func(s *denonState) { s.Audio.Restorer = "low" })},
		{name: "the graphic equalizer", line: "PSGEQ OFF", zone: "", field: graphicEqField,
			want: foldedInto(func(s *denonState) { s.Audio.GraphicEq = "off" })},
		{name: "the headphone equalizer", line: "PSHEQ OFF", zone: "", field: headphoneEqField,
			want: foldedInto(func(s *denonState) { s.Audio.HeadphoneEq = "off" })},
		{name: "the speaker virtualizer", line: "PSSPV ON", zone: "", field: virtualizerField,
			want: foldedInto(func(s *denonState) { s.Audio.SpeakerVirtualizer = true })},
		{name: "the dialog enhancer", line: "PSDEH OFF", zone: "", field: dialogEnhancerField,
			want: foldedInto(func(s *denonState) { s.Audio.DialogEnhancer = "off" })},
		{name: "the bass trim", line: "PSBAS 44", zone: "", field: bassField,
			want: foldedInto(func(s *denonState) { s.Tone.Bass = -6 })},
		{name: "the treble trim", line: "PSTRE 56", zone: "", field: trebleField,
			want: foldedInto(func(s *denonState) { s.Tone.Treble = 6 })},
		{name: "the tone control", line: "PSTONE CTRL OFF", zone: "", field: toneControlField,
			want: foldedInto(func(s *denonState) { s.Tone.Control = false })},
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

func TestApplyDenonLineIgnoresTheLinesTheDriverDoesNotRead(t *testing.T) {
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

// MVMAX carries the limit set in the receiver's own menu, and it
// arrives right behind the volume. Reading it as a volume would put the
// receiver's loudest setting on the bus.
func TestTheVolumeLimitIsNotTheVolume(t *testing.T) {
	state, _, _, _ := applyDenonLine(newDenonState(), "MV50")
	state, _, field, known := applyDenonLine(state, "MVMAX 695")

	mustMatch(t, known, true)
	mustMatch(t, field, volumeMaxField)
	mustMatch(t, state.Main.Volume, 100)
	mustMatch(t, state.Main.VolumeMax, 139)
}

// A model that reports main power through ZM keeps it there: a later
// PW line, which reports the whole unit, does not overwrite the main
// zone.
func TestTheMainZonePowerSurvivesAWholeUnitPowerLine(t *testing.T) {
	state, _, _, _ := applyDenonLine(newDenonState(), "ZMOFF")
	state, _, _, _ = applyDenonLine(state, "PWON")

	mustMatch(t, state.Main.Power, equipment.PowerStandby)
	mustMatch(t, state.System.Power, powerOn)
}

func TestTheVolumeCommandCarriesTheHalfSteps(t *testing.T) {
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

func TestTheMuteInputAndSoundModeCommands(t *testing.T) {
	mustMatch(t, MuteCommand(true), "MUON")
	mustMatch(t, MuteCommand(false), "MUOFF")
	mustMatch(t, InputCommand("MPLAY"), "SIMPLAY")
	mustMatch(t, InputCommand("CBL/SAT"), "SICBL/SAT")
	mustMatch(t, SoundModeCommand("STEREO"), "MSSTEREO")
	mustMatch(t, SoundModeCommand("MULTI CH IN"), "MSMULTI CH IN")
}

func TestDenonAddressNamesTheControlPort(t *testing.T) {
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

// equipmentField maps every internal field to the equipment contract's
// name, including the volume limit as a volume change. A field the
// contract does not name maps to nothing, which the listener ignores.
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
