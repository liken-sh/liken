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

import (
	"fmt"
	"strconv"
)

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

// The set commands for the settings a receiver declares. Each one
// carries the display value the status reports, and each returns the
// error that a value outside the receiver's vocabulary carries.

// EcoCommand is the set command for one eco mode.
func EcoCommand(mode string) (string, error) {
	word, err := ecoWire(mode)
	if err != nil {
		return "", err
	}
	return "ECO" + word, nil
}

// DimmerCommand is the set command for one dimmer level.
func DimmerCommand(mode string) (string, error) {
	word, err := dimmerWire(mode)
	if err != nil {
		return "", err
	}
	return "DIM " + word, nil
}

// AutoStandbyCommand is the set command for one auto-standby timer.
func AutoStandbyCommand(mode string) (string, error) {
	word, err := autoStandbyWire(mode)
	if err != nil {
		return "", err
	}
	return "STBY" + word, nil
}

// SpeakerPresetCommand is the set command for one speaker preset. The
// receiver carries presets 1 through 4.
func SpeakerPresetCommand(preset int) (string, error) {
	if preset < 1 || preset > 4 {
		return "", fmt.Errorf("speaker preset %d", preset)
	}
	return "SPPR " + strconv.Itoa(preset), nil
}

// AudioInputModeCommand is the set command for one audio input mode.
func AudioInputModeCommand(mode string) (string, error) {
	word, err := inputModeWire(mode)
	if err != nil {
		return "", err
	}
	return "SD" + word, nil
}

// VideoSelectCommand is the set command for one video select source.
// The source is a name the receiver already knows, or off.
func VideoSelectCommand(sel string) (string, error) {
	if sel == "off" {
		return "SVOFF", nil
	}
	if sel == "" {
		return "", fmt.Errorf("video select names no source")
	}
	return "SV" + sel, nil
}

// BluetoothTransmitterCommand is the set command for the transmitter.
func BluetoothTransmitterCommand(mode string) (string, error) {
	word, err := bluetoothModeWire(mode)
	if err != nil {
		return "", err
	}
	return "BTTX " + word, nil
}

// BluetoothOutputCommand is the set command for the transmitter output.
func BluetoothOutputCommand(mode string) (string, error) {
	word, err := bluetoothOutputWire(mode)
	if err != nil {
		return "", err
	}
	return "BTTX " + word, nil
}

// ToneControlCommand is the set command for the tone control switch.
func ToneControlCommand(on bool) (string, error) {
	return "PSTONE CTRL " + onOffWire(on), nil
}

// bassTrebleLimit is one side of the tone trim band the receiver
// carries, 12dB on each side of the 50 that reads as 0dB.
const bassTrebleLimit = 12

// BassCommand is the set command for one bass trim, in display units.
// The receiver's band is 12dB on each side of the 50 that reads as 0dB.
func BassCommand(display int) (string, error) {
	if display < -bassTrebleLimit || display > bassTrebleLimit {
		return "", fmt.Errorf("bass trim %ddB", display)
	}
	return fmt.Sprintf("PSBAS %02d", display+50), nil
}

// TrebleCommand is the set command for one treble trim, in display
// units. The receiver's band is 12dB on each side of the 50 that reads
// as 0dB.
func TrebleCommand(display int) (string, error) {
	if display < -bassTrebleLimit || display > bassTrebleLimit {
		return "", fmt.Errorf("treble trim %ddB", display)
	}
	return fmt.Sprintf("PSTRE %02d", display+50), nil
}

// MultEqCommand is the set command for one Audyssey MultEQ mode.
func MultEqCommand(mode string) (string, error) {
	word, err := multEqWire(mode)
	if err != nil {
		return "", err
	}
	return "PSMULTEQ:" + word, nil
}

// DynamicEqCommand is the set command for the Dynamic EQ switch.
func DynamicEqCommand(on bool) (string, error) {
	return "PSDYNEQ " + onOffWire(on), nil
}

// ReferenceLevelOffsetCommand is the set command for one reference
// level offset. The receiver carries only 0, 5, 10, and 15dB.
func ReferenceLevelOffsetCommand(offset int) (string, error) {
	switch offset {
	case 0, 5, 10, 15:
		return "PSREFLEV " + strconv.Itoa(offset), nil
	}
	return "", fmt.Errorf("reference level offset %ddB", offset)
}

// DynamicVolumeCommand is the set command for one Dynamic Volume mode.
func DynamicVolumeCommand(mode string) (string, error) {
	word, err := dynamicVolumeWire(mode)
	if err != nil {
		return "", err
	}
	return "PSDYNVOL " + word, nil
}

// LoudnessManagementCommand is the set command for the Loudness
// Management switch.
func LoudnessManagementCommand(on bool) (string, error) {
	return "PSLOM " + onOffWire(on), nil
}

// DynamicRangeCommand is the set command for one Dynamic Range mode.
func DynamicRangeCommand(mode string) (string, error) {
	word, err := drcWire(mode)
	if err != nil {
		return "", err
	}
	return "PSDRC " + word, nil
}

// LFECommand is the set command for one LFE level, in display units
// where the receiver's positive wire value is negative decibels. The
// receiver cuts only, from 0dB down to 10dB, so the wire value is never
// negative.
func LFECommand(display int) (string, error) {
	if display > 0 || display < -10 {
		return "", fmt.Errorf("LFE level %ddB", display)
	}
	return fmt.Sprintf("PSLFE %02d", -display), nil
}

// EffectCommand is the set command for one effect level.
func EffectCommand(value int) (string, error) {
	return fmt.Sprintf("PSEFF %02d", value), nil
}

// delayLimit is the longest delay time, in milliseconds, the receiver
// carries.
const delayLimit = 999

// DelayCommand is the set command for one delay time.
func DelayCommand(value int) (string, error) {
	if value < 0 || value > delayLimit {
		return "", fmt.Errorf("delay %dms", value)
	}
	return fmt.Sprintf("PSDEL %03d", value), nil
}

// AudioDelayCommand is the set command for one audio delay.
func AudioDelayCommand(value int) (string, error) {
	if value < 0 || value > delayLimit {
		return "", fmt.Errorf("audio delay %dms", value)
	}
	return fmt.Sprintf("PSDELAY %03d", value), nil
}

// SubwooferCommand is the set command for the subwoofer switch.
func SubwooferCommand(on bool) (string, error) {
	return "PSSWR " + onOffWire(on), nil
}

// RestorerCommand is the set command for one Audio Restorer mode.
func RestorerCommand(mode string) (string, error) {
	word, err := restorerWire(mode)
	if err != nil {
		return "", err
	}
	return "PSRSTR " + word, nil
}

// GraphicEqCommand is the set command for the Graphic EQ switch.
func GraphicEqCommand(mode string) (string, error) {
	word, err := onOffModeWire(mode)
	if err != nil {
		return "", err
	}
	return "PSGEQ " + word, nil
}

// HeadphoneEqCommand is the set command for the Headphone EQ switch.
func HeadphoneEqCommand(mode string) (string, error) {
	word, err := onOffModeWire(mode)
	if err != nil {
		return "", err
	}
	return "PSHEQ " + word, nil
}

// SpeakerVirtualizerCommand is the set command for the Speaker
// Virtualizer switch.
func SpeakerVirtualizerCommand(on bool) (string, error) {
	return "PSSPV " + onOffWire(on), nil
}

// DialogEnhancerCommand is the set command for one Dialog Enhancer
// level.
func DialogEnhancerCommand(mode string) (string, error) {
	word, err := dialogEnhancerWire(mode)
	if err != nil {
		return "", err
	}
	return "PSDEH " + word, nil
}

// ChannelVolumeCommand is the set command for one channel's trim, in
// display units.
func ChannelVolumeCommand(channel string, display float64) (string, error) {
	if channel == "" {
		return "", fmt.Errorf("channel volume names no channel")
	}
	halves := int((display + 50) * 2)
	return "CV" + channel + " " + HalfStepDigits(halves), nil
}

// The wire words the set commands carry, written from the display
// value the status reports. Each one answers the error that a word the
// receiver does not know carries.
func ecoWire(mode string) (string, error) {
	switch mode {
	case "on":
		return "ON", nil
	case "off":
		return "OFF", nil
	case "auto":
		return "AUTO", nil
	}
	return "", fmt.Errorf("eco mode %q", mode)
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
	return "", fmt.Errorf("dimmer %q", mode)
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
	return "", fmt.Errorf("auto standby %q", mode)
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
	return "", fmt.Errorf("audio input mode %q", mode)
}

func bluetoothModeWire(mode string) (string, error) {
	switch mode {
	case "on":
		return "ON", nil
	case "off":
		return "OFF", nil
	}
	return "", fmt.Errorf("bluetooth transmitter %q", mode)
}

func bluetoothOutputWire(mode string) (string, error) {
	switch mode {
	case "speakers":
		return "SP", nil
	case "bluetooth":
		return "BT", nil
	}
	return "", fmt.Errorf("bluetooth output %q", mode)
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
	return "", fmt.Errorf("multEQ mode %q", mode)
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
	return "", fmt.Errorf("dynamic volume %q", mode)
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
	return "", fmt.Errorf("audio restorer %q", mode)
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
	return "", fmt.Errorf("dynamic range %q", mode)
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
	return "", fmt.Errorf("dialog enhancer %q", mode)
}

// onOffWire writes a switch the way a set command carries it.
func onOffWire(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// onOffModeWire writes a switch word, on or off, the way a set command
// carries it.
func onOffModeWire(mode string) (string, error) {
	switch mode {
	case "on":
		return "ON", nil
	case "off":
		return "OFF", nil
	}
	return "", fmt.Errorf("switch %q", mode)
}
