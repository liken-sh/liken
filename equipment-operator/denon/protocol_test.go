// The Denon control protocol as the AVR-X1700H speaks it.

package denon

import (
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

// foldedInto is the state before the receiver has said anything, with
// the one field a line changes set to what that line produces.
func foldedInto(change func(*denonState)) denonState {
	state := newDenonState()
	change(&state)
	return state
}

func TestApplyDenonLineFoldsEveryLineTheReceiverSends(t *testing.T) {
	cases := []struct {
		line  string
		field string
		want  denonState
	}{
		{line: "PWON", field: powerField, want: foldedInto(func(s *denonState) { s.Power = powerOn })},
		{line: "PWSTANDBY", field: powerField, want: foldedInto(func(s *denonState) { s.Power = powerStandby })},
		{line: "MV50", field: volumeField, want: foldedInto(func(s *denonState) { s.Volume = 100 })},
		{line: "MV505", field: volumeField, want: foldedInto(func(s *denonState) { s.Volume = 101 })},
		{line: "MVMAX 695", field: volumeMaxField, want: foldedInto(func(s *denonState) { s.VolumeMax = 139 })},
		{line: "MUON", field: muteField, want: foldedInto(func(s *denonState) { s.Mute = true })},
		{line: "MUOFF", field: muteField, want: foldedInto(func(s *denonState) { s.Mute = false })},
		{line: "SIMPLAY", field: inputField, want: foldedInto(func(s *denonState) { s.Input = "MPLAY" })},
		{line: "SICBL/SAT", field: inputField, want: foldedInto(func(s *denonState) { s.Input = "CBL/SAT" })},
		{line: "MSMULTI CH IN", field: soundModeField, want: foldedInto(func(s *denonState) { s.SoundMode = "MULTI CH IN" })},
		{line: "MSSTEREO", field: soundModeField, want: foldedInto(func(s *denonState) { s.SoundMode = "STEREO" })},
	}
	for _, test := range cases {
		t.Run(test.line, func(t *testing.T) {
			state, field, known := applyDenonLine(newDenonState(), test.line)
			mustMatch(t, known, true)
			mustMatch(t, field, test.field)
			mustMatch(t, state, test.want)
		})
	}
}

func TestApplyDenonLineIgnoresTheLinesTheOperatorDoesNotRead(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "the video select setting", line: "SVOFF"},
		{name: "the dynamic range setting", line: "PSDRC OFF"},
		{name: "the low frequency setting", line: "PSLFE 00"},
		{name: "a volume that is not digits", line: "MVFOO"},
		{name: "an input with no name", line: "SI"},
		{name: "a sound mode with no name", line: "MS"},
		{name: "an empty line", line: ""},
		{name: "the second zone", line: "Z2ON"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state, field, known := applyDenonLine(newDenonState(), test.line)
			mustMatch(t, known, false)
			mustMatch(t, field, "")
			mustMatch(t, state, newDenonState())
		})
	}
}

// MVMAX carries the limit set in the receiver's own menu, and it
// arrives right behind the volume. Reading it as a volume would put the
// receiver's loudest setting on the bus.
func TestTheVolumeLimitIsNotTheVolume(t *testing.T) {
	state, _, _ := applyDenonLine(newDenonState(), "MV50")
	state, field, known := applyDenonLine(state, "MVMAX 695")

	mustMatch(t, known, true)
	mustMatch(t, field, volumeMaxField)
	mustMatch(t, state.Volume, 100)
	mustMatch(t, state.VolumeMax, 139)
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
// name, including the volume limit as a volume change.
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
		{field: "unknown", want: ""},
	}
	for _, test := range cases {
		t.Run(test.field, func(t *testing.T) {
			mustMatch(t, equipmentField(test.field), test.want)
		})
	}
}
