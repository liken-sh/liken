package denon

// The receiver's state as its protocol lines report it. Zone values
// stay in the receiver's units, and the driver converts them for the
// equipment contract. Settings are converted to the units of
// status.denon as each line is read.

import (
	"strconv"
	"strings"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// The names of the zones other than the main zone. A line for one of
// them starts with Z2 or Z3.
const (
	zone2 = "zone2"
	zone3 = "zone3"
)

// zoneState is the last values the receiver reported for one zone, in
// the receiver's units. seen is true once any line has reported the
// zone, so a model with one zone never publishes an empty second zone.
type zoneState struct {
	Power     equipment.Power
	Input     string
	SoundMode string
	Mute      bool
	Volume    int // half steps
	VolumeMax int // half steps
	Sleep     int // minutes
	Quick     int // quick select 1..5, 0 none
	seen      bool
}

// denonState is everything one receiver has reported. mainFromZM is
// true once the receiver sends ZMON or ZMOFF. From then on the main
// zone's power comes from those lines, and PWON and PWSTANDBY change
// only the system power.
type denonState struct {
	Reachable equipment.ConditionStatus
	Settings  Settings
	Main      zoneState
	Zone2     zoneState
	Zone3     zoneState

	// model and manufacturer come from the receiver's UPnP description
	// (description.go). They are empty until the client has read it.
	model        string
	manufacturer string

	mainFromZM bool
}

func newDenonState() denonState {
	return denonState{
		Reachable: equipment.ConditionUnknown,
		Main:      zoneState{Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves},
		Zone2:     zoneState{Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves},
		Zone3:     zoneState{Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves},
		Settings:  Settings{ChannelVolumes: map[string]float64{}},
	}
}

// The names of the fields that a zone's sleep and quick select lines
// change.
const (
	sleepField = "sleep"
	quickField = "quick"
)

// applyDenonLine applies one line from the receiver to the state. It
// returns the new state, the zone of the line, the name of the field it
// changed, and whether it recognized the line. The zone is empty for a
// setting of the whole unit. The receiver also sends lines that the
// driver does not parse, so the caller ignores a line that is not
// recognized.
func applyDenonLine(state denonState, line string) (denonState, string, string, bool) {
	switch {
	case line == PowerOnCommand:
		state.Settings.System.Power = powerOn
		if !state.mainFromZM {
			state.Main.Power = equipment.PowerOn
			state.Main.seen = true
		}
		return state, equipment.MainZone, powerField, true
	case line == powerStandbyCommand:
		state.Settings.System.Power = powerStandby
		if !state.mainFromZM {
			state.Main.Power = equipment.PowerStandby
			state.Main.seen = true
		}
		return state, equipment.MainZone, powerField, true
	case line == "ZMON":
		state.Main.Power = equipment.PowerOn
		state.Main.seen = true
		state.mainFromZM = true
		return state, equipment.MainZone, powerField, true
	case line == "ZMOFF":
		state.Main.Power = equipment.PowerStandby
		state.Main.seen = true
		state.mainFromZM = true
		return state, equipment.MainZone, powerField, true
	case line == "MUON":
		state.Main.Mute = true
		state.Main.seen = true
		return state, equipment.MainZone, muteField, true
	case line == MuteOffCommand:
		state.Main.Mute = false
		state.Main.seen = true
		return state, equipment.MainZone, muteField, true
	case strings.HasPrefix(line, "MVMAX "):
		halves, ok := ParseHalfSteps(strings.TrimSpace(line[len("MVMAX "):]))
		if !ok {
			return state, "", "", false
		}
		state.Main.VolumeMax = halves
		state.Main.seen = true
		return state, equipment.MainZone, volumeMaxField, true
	case strings.HasPrefix(line, VolumePrefix):
		halves, ok := ParseHalfSteps(line[len(VolumePrefix):])
		if !ok {
			return state, "", "", false
		}
		state.Main.Volume = halves
		state.Main.seen = true
		return state, equipment.MainZone, volumeField, true
	case strings.HasPrefix(line, InputPrefix) && len(line) > 2:
		state.Main.Input = line[2:]
		state.Main.seen = true
		return state, equipment.MainZone, inputField, true
	case strings.HasPrefix(line, "MSQUICK"):
		quick, ok := parseQuick(line)
		if !ok {
			return state, "", "", false
		}
		state.Main.Quick = quick
		state.Main.seen = true
		return state, equipment.MainZone, quickField, true
	case strings.HasPrefix(line, SoundModePrefix) && len(line) > 2:
		// The receiver pads some sound mode lines with trailing spaces.
		state.Main.SoundMode = strings.TrimSpace(line[2:])
		state.Main.seen = true
		return state, equipment.MainZone, soundModeField, true
	case strings.HasPrefix(line, "SLP"):
		minutes, ok := parseSleep(line[len("SLP"):])
		if !ok {
			return state, "", "", false
		}
		state.Main.Sleep = minutes
		state.Main.seen = true
		return state, equipment.MainZone, sleepField, true
	case strings.HasPrefix(line, "Z2"):
		zone, field, known := applyZoneLine(state.Zone2, line[2:])
		state.Zone2 = zone
		return state, zone2, field, known
	case strings.HasPrefix(line, "Z3"):
		zone, field, known := applyZoneLine(state.Zone3, line[2:])
		state.Zone3 = zone
		return state, zone3, field, known
	}

	switch {
	case line == "ECOON":
		state.Settings.System.Eco = strPtr("on")
		return state, "", ecoField, true
	case line == "ECOOFF":
		state.Settings.System.Eco = strPtr("off")
		return state, "", ecoField, true
	case line == "ECOAUTO":
		state.Settings.System.Eco = strPtr("auto")
		return state, "", ecoField, true
	case strings.HasPrefix(line, "DIM "):
		dimmer, ok := dimmerWord(line[len("DIM "):])
		if !ok {
			return state, "", "", false
		}
		state.Settings.System.Dimmer = strPtr(dimmer)
		return state, "", dimmerField, true
	case strings.HasPrefix(line, "STBY"):
		standby, ok := autoStandbyWord(line[len("STBY"):])
		if !ok {
			return state, "", "", false
		}
		state.Settings.System.AutoStandby = strPtr(standby)
		return state, "", standbyField, true
	case strings.HasPrefix(line, "SPPR "):
		preset, err := strconv.Atoi(strings.TrimSpace(line[len("SPPR "):]))
		if err != nil {
			return state, "", "", false
		}
		state.Settings.System.SpeakerPreset = intPtr(preset)
		return state, "", speakerPresetField, true
	case strings.HasPrefix(line, hdmiAudioOutKey), strings.HasPrefix(line, "SSHOS"):
		field, ok := applyHDMILine(&state.Settings.HDMI, line)
		return state, "", field, ok
	case strings.HasPrefix(line, "SD"):
		mode, ok := inputModeWord(line[2:])
		if !ok {
			return state, "", "", false
		}
		state.Settings.System.AudioInputMode = strPtr(mode)
		return state, "", inputModeField, true
	case strings.HasPrefix(line, "SV"):
		state.Settings.System.VideoSelect = strPtr(videoSelect(line[2:]))
		return state, "", videoSelectField, true
	case strings.HasPrefix(line, "BTTX "):
		mode, output, ok := bluetoothWord(line[len("BTTX "):])
		if !ok {
			return state, "", "", false
		}
		if mode != "" {
			state.Settings.System.BluetoothTransmitter = strPtr(mode)
		}
		if output != "" {
			state.Settings.System.BluetoothOutput = strPtr(output)
		}
		return state, "", bluetoothField, true
	case strings.HasPrefix(line, "CV"):
		channel, value, ok := parseChannelVolume(line)
		if !ok {
			return state, "", "", false
		}
		state.Settings.ChannelVolumes[channel] = value
		return state, "", channelField, true
	case strings.HasPrefix(line, "PS"):
		field, ok := applyParameterLine(&state, line)
		return state, "", field, ok
	}
	return state, "", "", false
}

// applyZoneLine applies the body of a Z2 or Z3 line to one zone. The
// body is the line without the zone prefix, such as "ON", "MUOFF",
// "SLP010", "90", or an input name. It rejects CV, PS, HPF, and CS
// bodies, because the last case would read them as an input name.
func applyZoneLine(zone zoneState, body string) (zoneState, string, bool) {
	switch {
	case body == "ON":
		zone.Power = equipment.PowerOn
		zone.seen = true
		return zone, powerField, true
	case body == "OFF":
		zone.Power = equipment.PowerStandby
		zone.seen = true
		return zone, powerField, true
	case strings.HasPrefix(body, "MU"):
		if body == "MUON" {
			zone.Mute = true
		} else if body == MuteOffCommand {
			zone.Mute = false
		} else {
			return zone, "", false
		}
		zone.seen = true
		return zone, muteField, true
	case strings.HasPrefix(body, "SLP"):
		minutes, ok := parseSleep(body[len("SLP"):])
		if !ok {
			return zone, "", false
		}
		zone.Sleep = minutes
		zone.seen = true
		return zone, sleepField, true
	case strings.HasPrefix(body, "CV"), strings.HasPrefix(body, "PS"),
		strings.HasPrefix(body, "HPF"), strings.HasPrefix(body, "CS"):
		return zone, "", false
	}
	if halves, ok := ParseHalfSteps(body); ok {
		zone.Volume = halves
		zone.seen = true
		return zone, volumeField, true
	}
	if body == "" {
		return zone, "", false
	}
	zone.Input = body
	zone.seen = true
	return zone, inputField, true
}

// applyParameterLine applies one PS line, which reports a tone,
// Audyssey, or audio setting. Each word reader accepts only the words
// that its *Wire function in protocol.go encodes, so a word it does not
// know leaves the field nil, and the status never holds a value that
// the driver cannot send back.
func applyParameterLine(state *denonState, line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "PSMULTEQ:"):
		word, ok := multEqWord(strings.TrimSpace(line[len("PSMULTEQ:"):]))
		if !ok {
			return "", false
		}
		state.Settings.Audyssey.Multeq = strPtr(word)
		return multeqField, true
	case strings.HasPrefix(line, "PSDYNEQ "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSDYNEQ "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audyssey.DynamicEq = boolPtr(on)
		return dynamicEqField, true
	case strings.HasPrefix(line, "PSREFLEV "):
		offset, err := strconv.Atoi(strings.TrimSpace(line[len("PSREFLEV "):]))
		if err != nil {
			return "", false
		}
		state.Settings.Audyssey.ReferenceLevelOffset = intPtr(offset)
		return referenceLevelField, true
	case strings.HasPrefix(line, "PSDYNVOL "):
		word, ok := dynamicVolumeWord(strings.TrimSpace(line[len("PSDYNVOL "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audyssey.DynamicVolume = strPtr(word)
		return dynamicVolumeField, true
	case strings.HasPrefix(line, "PSLOM "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSLOM "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audyssey.LoudnessManagement = boolPtr(on)
		return loudnessField, true
	case strings.HasPrefix(line, "PSDRC "):
		word, ok := drcWord(strings.TrimSpace(line[len("PSDRC "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audio.DRC = strPtr(word)
		return drcField, true
	case strings.HasPrefix(line, "PSLFE "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSLFE "):]))
		if err != nil {
			return "", false
		}
		state.Settings.Audio.LFE = intPtr(-value)
		return lfeField, true
	case strings.HasPrefix(line, "PSEFF "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSEFF "):]))
		if err != nil {
			return "", false
		}
		state.Settings.Audio.Effect = intPtr(value)
		return effectField, true
	case strings.HasPrefix(line, "PSDELAY "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSDELAY "):]))
		if err != nil {
			return "", false
		}
		state.Settings.Audio.AudioDelay = intPtr(value)
		return delayField, true
	case strings.HasPrefix(line, "PSDEL "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSDEL "):]))
		if err != nil {
			return "", false
		}
		state.Settings.Audio.Delay = intPtr(value)
		return delayField, true
	case strings.HasPrefix(line, "PSSWR "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSSWR "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audio.Subwoofer = boolPtr(on)
		return subwooferField, true
	case strings.HasPrefix(line, "PSRSTR "):
		word, ok := restorerWord(strings.TrimSpace(line[len("PSRSTR "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audio.Restorer = strPtr(word)
		return restorerField, true
	case strings.HasPrefix(line, "PSGEQ "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSGEQ "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audio.GraphicEq = strPtr(onOffText(on))
		return graphicEqField, true
	case strings.HasPrefix(line, "PSHEQ "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSHEQ "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audio.HeadphoneEq = strPtr(onOffText(on))
		return headphoneEqField, true
	case strings.HasPrefix(line, "PSSPV "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSSPV "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audio.SpeakerVirtualizer = boolPtr(on)
		return virtualizerField, true
	case strings.HasPrefix(line, "PSDEH "):
		word, ok := dialogEnhancerWord(strings.TrimSpace(line[len("PSDEH "):]))
		if !ok {
			return "", false
		}
		state.Settings.Audio.DialogEnhancer = strPtr(word)
		return dialogEnhancerField, true
	case strings.HasPrefix(line, "PSBAS "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSBAS "):]))
		if err != nil {
			return "", false
		}
		state.Settings.Tone.Bass = intPtr(value - 50)
		return bassField, true
	case strings.HasPrefix(line, "PSTRE "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSTRE "):]))
		if err != nil {
			return "", false
		}
		state.Settings.Tone.Treble = intPtr(value - 50)
		return trebleField, true
	case strings.HasPrefix(line, "PSTONE CTRL "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSTONE CTRL "):]))
		if !ok {
			return "", false
		}
		state.Settings.Tone.Control = boolPtr(on)
		return toneControlField, true
	}
	return "", false
}

// The field names of the settings lines. equipmentField in client.go
// maps none of them to an equipment event.
const (
	ecoField            = "eco"
	dimmerField         = "dimmer"
	standbyField        = "autoStandby"
	speakerPresetField  = "speakerPreset"
	inputModeField      = "audioInputMode"
	videoSelectField    = "videoSelect"
	bluetoothField      = "bluetoothTransmitter"
	channelField        = "channelVolume"
	multeqField         = "multeq"
	dynamicEqField      = "dynamicEq"
	referenceLevelField = "referenceLevel"
	dynamicVolumeField  = "dynamicVolume"
	loudnessField       = "loudnessManagement"
	drcField            = "drc"
	lfeField            = "lfe"
	effectField         = "effect"
	delayField          = "delay"
	subwooferField      = "subwoofer"
	restorerField       = "restorer"
	graphicEqField      = "graphicEq"
	headphoneEqField    = "headphoneEq"
	virtualizerField    = "speakerVirtualizer"
	dialogEnhancerField = "dialogEnhancer"
	bassField           = "bass"
	trebleField         = "treble"
	toneControlField    = "toneControl"
)

// parseSleep reads a sleep timer body: OFF, which is 0, or minutes from
// 0 to 120.
func parseSleep(body string) (int, bool) {
	if body == "OFF" {
		return 0, true
	}
	minutes, err := strconv.Atoi(body)
	if err != nil || minutes < 0 || minutes > 120 {
		return unknownHalves, false
	}
	return minutes, true
}

// parseQuick reads the quick select number, from 0 to 5, from an
// MSQUICK line.
func parseQuick(line string) (int, bool) {
	slot, err := strconv.Atoi(strings.TrimSpace(line[len("MSQUICK"):]))
	if err != nil || slot < 0 || slot > 5 {
		return unknownHalves, false
	}
	return slot, true
}

// parseChannelVolume reads one CV line and returns the channel name and
// its trim in dB. The digits are the receiver's volume scale, where 50
// is 0 dB.
func parseChannelVolume(line string) (string, float64, bool) {
	body := line[len("CV"):]
	channel, digits, found := strings.Cut(body, " ")
	if !found || channel == "" {
		return "", 0, false
	}
	halves, ok := ParseHalfSteps(strings.TrimSpace(digits))
	if !ok {
		return "", 0, false
	}
	return channel, float64(halves)/2 - 50, true
}

func onOffWord(word string) (bool, bool) {
	switch word {
	case "ON":
		return true, true
	case "OFF":
		return false, true
	}
	return false, false
}

func onOffText(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func dimmerWord(word string) (string, bool) {
	switch word {
	case "OFF":
		return "off", true
	case "DIM":
		return "dim", true
	case "DAR":
		return "dark", true
	case "BRI":
		return "bright", true
	}
	return "", false
}

func autoStandbyWord(word string) (string, bool) {
	switch word {
	case "OFF":
		return "off", true
	case "15M":
		return "15m", true
	case "30M":
		return "30m", true
	case "60M":
		return "60m", true
	case "2H":
		return "2h", true
	case "4H":
		return "4h", true
	case "8H":
		return "8h", true
	}
	return "", false
}

func inputModeWord(word string) (string, bool) {
	switch word {
	case "AUTO":
		return "auto", true
	case "HDMI":
		return "hdmi", true
	case "DIGITAL":
		return "digital", true
	case "ANALOG":
		return "analog", true
	}
	return "", false
}

func videoSelect(body string) string {
	if body == "OFF" || body == "" {
		return "off"
	}
	return body
}

// bluetoothWord reads a BTTX body. It returns the transmitter mode or
// the output, and the other is empty, because the receiver reports them
// on separate lines.
func bluetoothWord(body string) (string, string, bool) {
	switch body {
	case "ON":
		return "on", "", true
	case "OFF":
		return "off", "", true
	case "SP":
		return "", "speakers", true
	case "BT":
		return "", "bluetooth", true
	}
	return "", "", false
}

func multEqWord(word string) (string, bool) {
	switch word {
	case "AUDYSSEY":
		return "reference", true
	case "BYP.LR":
		return "l/r bypass", true
	case "FLAT":
		return "flat", true
	case "MANUAL":
		return "manual", true
	case "OFF":
		return "off", true
	}
	return "", false
}

func dynamicVolumeWord(word string) (string, bool) {
	switch word {
	case "OFF":
		return "off", true
	case "LIT":
		return "light", true
	case "MED":
		return "medium", true
	case "HEV":
		return "heavy", true
	}
	return "", false
}

func restorerWord(word string) (string, bool) {
	switch word {
	case "OFF":
		return "off", true
	case "LOW":
		return "low", true
	case "MED":
		return "medium", true
	case "HI":
		return "high", true
	}
	return "", false
}

func drcWord(word string) (string, bool) {
	switch word {
	case "OFF":
		return "off", true
	case "LOW":
		return "low", true
	case "MID":
		return "mid", true
	case "HI":
		return "hi", true
	case "AUTO":
		return "auto", true
	}
	return "", false
}

func dialogEnhancerWord(word string) (string, bool) {
	switch word {
	case "OFF":
		return "off", true
	case "LOW":
		return "low", true
	case "MID":
		return "mid", true
	case "HIGH":
		return "high", true
	}
	return "", false
}
