// Package denon implements equipment.Driver for a Denon or Marantz A/V
// receiver. It sends commands in the receiver's ASCII control protocol
// on port 23, keeps the state that the receiver's lines report, and
// reconnects when the read deadline expires or a socket error ends the
// connection. Volume is counted in half steps, two for each display
// unit.
//
// The protocol reference is https://assets.denon.com/documentmaster/uk/
// avr1713_avr1613_protocol_v860.pdf. The lines this file parses were
// also read from a live AVR-X1700H.
package denon

import (
	"fmt"
	"strconv"
)

// Port is the receiver's control port. It is added to a declared
// address that has no port.
const Port = "23"

// Terminator is the carriage return that ends every command and every
// line from the receiver.
const Terminator = '\r'

// Queries are the status queries that the driver sends each time it
// connects. The list covers every model, and a model ignores the
// queries it does not support.
var Queries = []string{
	"PW?", "ZM?", "Z2?", "MV?", "MU?", "Z2MU?", "SI?", "MS?",
	"SLP?", "Z2SLP?", "ECO?", "DIM ?", "STBY?", "SPPR ?", "SD?", "SV?", "CV?",
	"PSMULTEQ: ?", "PSDYNEQ ?", "PSREFLEV ?", "PSDYNVOL ?", "PSTONE CTRL ?",
	"PSBAS ?", "PSTRE ?", "PSDRC ?", "PSLFE ?", "PSEFF ?", "PSDEL ?", "PSDELAY ?",
	"PSSWR ?", "PSRSTR ?", "PSLOM ?", "PSGEQ ?", "PSHEQ ?", "PSSPV ?", "PSDEH ?",
	"BTTX ?", hdmiAudioOutQuery, hdmiSetupQuery,
}

// Set commands, and the prefixes of the set commands that take a value.
const (
	PowerOnCommand      = "PWON"
	MuteOnCommand       = "MUON"
	MuteOffCommand      = "MUOFF"
	powerStandbyCommand = "PWSTANDBY"
	VolumePrefix        = "MV"
	InputPrefix         = "SI"
	SoundModePrefix     = "MS"
)

// The values of the power field.
const (
	powerOn      = "On"
	powerStandby = "Standby"
)

// The names of the state fields. applyDenonLine returns the name of
// the field that a line changed.
const (
	powerField     = "power"
	inputField     = "input"
	volumeField    = "volume"
	volumeMaxField = "volumeMax"
	muteField      = "mute"
	soundModeField = "soundMode"
)

// VolumeCommand returns the set command for a volume in half steps.
func VolumeCommand(halves int) string {
	return VolumePrefix + HalfStepDigits(halves)
}

// MuteCommand returns the set command that mutes or unmutes.
func MuteCommand(muted bool) string {
	if muted {
		return MuteOnCommand
	}
	return MuteOffCommand
}

// InputCommand returns the set command that selects an input, by the
// name the receiver uses for it.
func InputCommand(input string) string {
	return InputPrefix + input
}

// SoundModeCommand returns the set command that selects a sound mode,
// by the name the receiver uses for it.
func SoundModeCommand(mode string) string {
	return SoundModePrefix + mode
}

// The set commands below write the fields of Settings. Each takes the
// value in the form that the status reports, and returns an error for a
// value that the receiver does not accept.

// EcoCommand returns the set command for one eco mode.
func EcoCommand(mode string) (string, error) {
	word, err := ecoWire(mode)
	if err != nil {
		return "", err
	}
	return "ECO" + word, nil
}

// DimmerCommand returns the set command for one dimmer level.
func DimmerCommand(mode string) (string, error) {
	word, err := dimmerWire(mode)
	if err != nil {
		return "", err
	}
	return "DIM " + word, nil
}

// AutoStandbyCommand returns the set command for one auto-standby
// timer.
func AutoStandbyCommand(mode string) (string, error) {
	word, err := autoStandbyWire(mode)
	if err != nil {
		return "", err
	}
	return "STBY" + word, nil
}

// SpeakerPresetCommand returns the set command for a speaker preset.
// The receiver has presets 1 through 4.
func SpeakerPresetCommand(preset int) (string, error) {
	if preset < 1 || preset > 4 {
		return "", fmt.Errorf("speaker preset %d is outside 1 through 4", preset)
	}
	return "SPPR " + strconv.Itoa(preset), nil
}

// AudioInputModeCommand returns the set command for one audio input
// mode.
func AudioInputModeCommand(mode string) (string, error) {
	word, err := inputModeWire(mode)
	if err != nil {
		return "", err
	}
	return "SD" + word, nil
}

// VideoSelectCommand returns the set command for the video select
// source. The source is an input name that the receiver reports, or
// "off".
func VideoSelectCommand(sel string) (string, error) {
	if sel == "off" {
		return "SVOFF", nil
	}
	if sel == "" {
		return "", fmt.Errorf("the video select source is empty, want an input name or off")
	}
	return "SV" + sel, nil
}

// BluetoothTransmitterCommand returns the set command for the
// transmitter.
func BluetoothTransmitterCommand(mode string) (string, error) {
	word, err := bluetoothModeWire(mode)
	if err != nil {
		return "", err
	}
	return "BTTX " + word, nil
}

// BluetoothOutputCommand returns the set command for the transmitter
// output.
func BluetoothOutputCommand(mode string) (string, error) {
	word, err := bluetoothOutputWire(mode)
	if err != nil {
		return "", err
	}
	return "BTTX " + word, nil
}

// ToneControlCommand returns the set command for the tone control
// switch.
func ToneControlCommand(on bool) (string, error) {
	return "PSTONE CTRL " + onOffWire(on), nil
}

// bassTrebleLimit is the largest bass or treble trim in either
// direction, in dB.
const bassTrebleLimit = 12

// BassCommand returns the set command for a bass trim in dB. The wire
// value is 50 for 0dB, so a trim of -12 to 12 is sent as 38 to 62.
func BassCommand(display int) (string, error) {
	if display < -bassTrebleLimit || display > bassTrebleLimit {
		return "", fmt.Errorf("bass trim %ddB is outside -%d to %ddB", display, bassTrebleLimit, bassTrebleLimit)
	}
	return fmt.Sprintf("PSBAS %02d", display+50), nil
}

// TrebleCommand returns the set command for a treble trim in dB. The
// wire value is 50 for 0dB, as for bass.
func TrebleCommand(display int) (string, error) {
	if display < -bassTrebleLimit || display > bassTrebleLimit {
		return "", fmt.Errorf("treble trim %ddB is outside -%d to %ddB", display, bassTrebleLimit, bassTrebleLimit)
	}
	return fmt.Sprintf("PSTRE %02d", display+50), nil
}

// MultEqCommand returns the set command for one Audyssey MultEQ mode.
func MultEqCommand(mode string) (string, error) {
	word, err := multEqWire(mode)
	if err != nil {
		return "", err
	}
	return "PSMULTEQ:" + word, nil
}

// DynamicEqCommand returns the set command for the Dynamic EQ switch.
func DynamicEqCommand(on bool) (string, error) {
	return "PSDYNEQ " + onOffWire(on), nil
}

// ReferenceLevelOffsetCommand returns the set command for a reference
// level offset. The receiver accepts only 0, 5, 10, and 15dB.
func ReferenceLevelOffsetCommand(offset int) (string, error) {
	switch offset {
	case 0, 5, 10, 15:
		return "PSREFLEV " + strconv.Itoa(offset), nil
	}
	return "", fmt.Errorf("reference level offset %ddB is not one of 0, 5, 10, 15", offset)
}

// DynamicVolumeCommand returns the set command for one Dynamic
// Volume mode.
func DynamicVolumeCommand(mode string) (string, error) {
	word, err := dynamicVolumeWire(mode)
	if err != nil {
		return "", err
	}
	return "PSDYNVOL " + word, nil
}

// LoudnessManagementCommand returns the set command for the Loudness
// Management switch.
func LoudnessManagementCommand(on bool) (string, error) {
	return "PSLOM " + onOffWire(on), nil
}

// DynamicRangeCommand returns the set command for one Dynamic Range
// mode.
func DynamicRangeCommand(mode string) (string, error) {
	word, err := drcWire(mode)
	if err != nil {
		return "", err
	}
	return "PSDRC " + word, nil
}

// LFECommand returns the set command for an LFE level in dB, from -10
// to 0. The receiver can only reduce the level, so the wire value is
// the number of dB of reduction and is never negative.
func LFECommand(display int) (string, error) {
	if display > 0 || display < -10 {
		return "", fmt.Errorf("LFE level %ddB is outside -10 to 0dB", display)
	}
	return fmt.Sprintf("PSLFE %02d", -display), nil
}

// EffectCommand returns the set command for one effect level.
func EffectCommand(value int) (string, error) {
	return fmt.Sprintf("PSEFF %02d", value), nil
}

// delayLimit is the longest delay time that the receiver accepts, in
// milliseconds.
const delayLimit = 999

// DelayCommand returns the set command for one delay time.
func DelayCommand(value int) (string, error) {
	if value < 0 || value > delayLimit {
		return "", fmt.Errorf("delay %dms is outside 0 to %dms", value, delayLimit)
	}
	return fmt.Sprintf("PSDEL %03d", value), nil
}

// AudioDelayCommand returns the set command for one audio delay.
func AudioDelayCommand(value int) (string, error) {
	if value < 0 || value > delayLimit {
		return "", fmt.Errorf("audio delay %dms is outside 0 to %dms", value, delayLimit)
	}
	return fmt.Sprintf("PSDELAY %03d", value), nil
}

// SubwooferCommand returns the set command for the subwoofer switch.
func SubwooferCommand(on bool) (string, error) {
	return "PSSWR " + onOffWire(on), nil
}

// RestorerCommand returns the set command for one Audio Restorer mode.
func RestorerCommand(mode string) (string, error) {
	word, err := restorerWire(mode)
	if err != nil {
		return "", err
	}
	return "PSRSTR " + word, nil
}

// GraphicEqCommand returns the set command for the Graphic EQ switch.
func GraphicEqCommand(mode string) (string, error) {
	word, err := onOffModeWire(mode)
	if err != nil {
		return "", err
	}
	return "PSGEQ " + word, nil
}

// HeadphoneEqCommand returns the set command for the Headphone EQ
// switch.
func HeadphoneEqCommand(mode string) (string, error) {
	word, err := onOffModeWire(mode)
	if err != nil {
		return "", err
	}
	return "PSHEQ " + word, nil
}

// SpeakerVirtualizerCommand returns the set command for the Speaker
// Virtualizer switch.
func SpeakerVirtualizerCommand(on bool) (string, error) {
	return "PSSPV " + onOffWire(on), nil
}

// DialogEnhancerCommand returns the set command for one Dialog Enhancer
// level.
func DialogEnhancerCommand(mode string) (string, error) {
	word, err := dialogEnhancerWire(mode)
	if err != nil {
		return "", err
	}
	return "PSDEH " + word, nil
}

// ChannelVolumeCommand returns the set command for the trim of one
// channel, in dB. The wire value is the trim plus 50, written in the
// same digits as a volume, so a trim can be a half step.
func ChannelVolumeCommand(channel string, display float64) (string, error) {
	if channel == "" {
		return "", fmt.Errorf("the channel name for a channel volume is empty")
	}
	halves := int((display + 50) * 2)
	return "CV" + channel + " " + HalfStepDigits(halves), nil
}

// The functions below translate a value in the form that the status
// reports into the word that the set command sends. Each returns an
// error for a value that the receiver does not accept.

func ecoWire(mode string) (string, error) {
	switch mode {
	case "on":
		return "ON", nil
	case "off":
		return "OFF", nil
	case "auto":
		return "AUTO", nil
	}
	return "", fmt.Errorf("eco mode %q is not one of on, off, auto", mode)
}

func dimmerWire(mode string) (string, error) {
	switch mode {
	case "off":
		return "OFF", nil
	case "dim":
		return "DIM", nil
	case "dark":
		return "DAR", nil
	case "bright":
		return "BRI", nil
	}
	return "", fmt.Errorf("dimmer mode %q is not one of off, dim, dark, bright", mode)
}

func autoStandbyWire(mode string) (string, error) {
	switch mode {
	case "off":
		return "OFF", nil
	case "15m":
		return "15M", nil
	case "30m":
		return "30M", nil
	case "60m":
		return "60M", nil
	case "2h":
		return "2H", nil
	case "4h":
		return "4H", nil
	case "8h":
		return "8H", nil
	}
	return "", fmt.Errorf("auto standby mode %q is not one of off, 15m, 30m, 60m, 2h, 4h, 8h", mode)
}

func inputModeWire(mode string) (string, error) {
	switch mode {
	case "auto":
		return "AUTO", nil
	case "hdmi":
		return "HDMI", nil
	case "digital":
		return "DIGITAL", nil
	case "analog":
		return "ANALOG", nil
	}
	return "", fmt.Errorf("audio input mode %q is not one of auto, hdmi, digital, analog", mode)
}

func bluetoothModeWire(mode string) (string, error) {
	switch mode {
	case "on":
		return "ON", nil
	case "off":
		return "OFF", nil
	}
	return "", fmt.Errorf("bluetooth transmitter mode %q is not one of on, off", mode)
}

func bluetoothOutputWire(mode string) (string, error) {
	switch mode {
	case "speakers":
		return "SP", nil
	case "bluetooth":
		return "BT", nil
	}
	return "", fmt.Errorf("bluetooth output %q is not one of speakers, bluetooth", mode)
}

func multEqWire(mode string) (string, error) {
	switch mode {
	case "reference":
		return "AUDYSSEY", nil
	case "l/r bypass":
		return "BYP.LR", nil
	case "flat":
		return "FLAT", nil
	case "manual":
		return "MANUAL", nil
	case "off":
		return "OFF", nil
	}
	return "", fmt.Errorf("MultEQ mode %q is not one of reference, l/r bypass, flat, manual, off", mode)
}

func dynamicVolumeWire(mode string) (string, error) {
	switch mode {
	case "off":
		return "OFF", nil
	case "light":
		return "LIT", nil
	case "medium":
		return "MED", nil
	case "heavy":
		return "HEV", nil
	}
	return "", fmt.Errorf("dynamic volume mode %q is not one of off, light, medium, heavy", mode)
}

func restorerWire(mode string) (string, error) {
	switch mode {
	case "off":
		return "OFF", nil
	case "low":
		return "LOW", nil
	case "medium":
		return "MED", nil
	case "high":
		return "HI", nil
	}
	return "", fmt.Errorf("audio restorer mode %q is not one of off, low, medium, high", mode)
}

func drcWire(mode string) (string, error) {
	switch mode {
	case "off":
		return "OFF", nil
	case "low":
		return "LOW", nil
	case "mid":
		return "MID", nil
	case "hi":
		return "HI", nil
	case "auto":
		return "AUTO", nil
	}
	return "", fmt.Errorf("dynamic range mode %q is not one of off, low, mid, hi, auto", mode)
}

func dialogEnhancerWire(mode string) (string, error) {
	switch mode {
	case "off":
		return "OFF", nil
	case "low":
		return "LOW", nil
	case "mid":
		return "MID", nil
	case "high":
		return "HIGH", nil
	}
	return "", fmt.Errorf("dialog enhancer mode %q is not one of off, low, mid, high", mode)
}

// onOffWire returns the word that a set command uses for a switch.
func onOffWire(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// onOffModeWire is onOffWire for a switch that arrives as the string
// "on" or "off".
func onOffModeWire(mode string) (string, error) {
	switch mode {
	case "on":
		return "ON", nil
	case "off":
		return "OFF", nil
	}
	return "", fmt.Errorf("switch value %q is not one of on, off", mode)
}
