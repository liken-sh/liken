// Package denon is the protocol driver for a Denon or Marantz A/V
// receiver. It parses the Denon ASCII protocol on port 23, records the
// state each line reports, and reconnects when the read deadline or a
// socket error ends the connection. It implements equipment.Driver and
// reports volume in half steps, two per display unit.
//
// The protocol reference is https://assets.denon.com/documentmaster/uk/
// avr1713_avr1613_protocol_v860.pdf. The lines this file parses were
// also read from a live AVR-X1700H.
package denon

// Port is the control port a Denon answers on, used when the declared
// address names none.
const Port = "23"

// Terminator ends every command and every reply: an ASCII carriage
// return.
const Terminator = '\r'

// Queries are the status queries the driver sends on every connect,
// front-loaded with the zone and volume lines. A model that does not
// answer one simply ignores it, so the set is a union and not a
// requirement.
var Queries = []string{
	"PW?", "ZM?", "Z2?", "MV?", "MU?", "Z2MU?", "SI?", "MS?",
	"SLP?", "Z2SLP?", "ECO?", "DIM ?", "STBY?", "SPPR ?", "SD?", "SV?", "CV?",
	"PSMULTEQ: ?", "PSDYNEQ ?", "PSREFLEV ?", "PSDYNVOL ?", "PSTONE CTRL ?",
	"PSBAS ?", "PSTRE ?", "PSDRC ?", "PSLFE ?", "PSEFF ?", "PSDEL ?", "PSDELAY ?",
	"PSSWR ?", "PSRSTR ?", "PSLOM ?", "PSGEQ ?", "PSHEQ ?", "PSSPV ?", "PSDEH ?",
	"BTTX ?",
}

// The set commands this operator sends.
const (
	PowerOnCommand      = "PWON"
	MuteOnCommand       = "MUON"
	MuteOffCommand      = "MUOFF"
	powerStandbyCommand = "PWSTANDBY"
	VolumePrefix        = "MV"
	InputPrefix         = "SI"
	SoundModePrefix     = "MS"
)

// The two power words the status carries.
const (
	powerOn      = "on"
	powerStandby = "standby"
)

// The fields a line can name, which is what a listener switches on.
const (
	powerField     = "power"
	inputField     = "input"
	volumeField    = "volume"
	volumeMaxField = "volumeMax"
	muteField      = "mute"
	soundModeField = "soundMode"
)

// VolumeCommand is the set command for one half-step count.
func VolumeCommand(halves int) string {
	return VolumePrefix + HalfStepDigits(halves)
}

// MuteCommand is the set command for one mute state.
func MuteCommand(muted bool) string {
	if muted {
		return MuteOnCommand
	}
	return MuteOffCommand
}

// InputCommand selects one input by the name the receiver carries for
// it.
func InputCommand(input string) string {
	return InputPrefix + input
}

// SoundModeCommand selects one sound mode by the name the receiver
// carries for it.
func SoundModeCommand(mode string) string {
	return SoundModePrefix + mode
}
