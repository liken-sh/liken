package denon

// The receiver's own state: one entry per zone, the system settings,
// the tone and Audyssey settings, the audio settings, and the channel
// volumes. The state is in the receiver's units, and the driver
// translates it into the equipment contract and into the protocol
// snapshot the status carries.

import (
	"strconv"
	"strings"

	"github.com/liken-sh/equipment-operator/equipment"
)

// The zone names beyond the main zone. A line names one with its
// prefix, and the state records one only once a line has named it.
const (
	zone2 = "zone2"
	zone3 = "zone3"
)

// zoneState is one zone's last reported values, in the receiver's own
// units. seen records that the receiver has named the zone at all, so
// a single-zone model never reports an empty second zone.
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

// systemState is the unit-wide settings.
type systemState struct {
	Power                string `json:"power,omitempty"`
	Eco                  string `json:"eco,omitempty"`
	Dimmer               string `json:"dimmer,omitempty"`
	AutoStandby          string `json:"autoStandby,omitempty"`
	SpeakerPreset        int    `json:"speakerPreset,omitempty"`
	AudioInputMode       string `json:"audioInputMode,omitempty"`
	VideoSelect          string `json:"videoSelect,omitempty"`
	BluetoothTransmitter string `json:"bluetoothTransmitter,omitempty"`
	BluetoothOutput      string `json:"bluetoothOutput,omitempty"`
}

// toneState holds the tone control and the two trims in display units,
// where the wire's 50 is 0dB.
type toneState struct {
	Control bool `json:"control"`
	Bass    int  `json:"bass"`
	Treble  int  `json:"treble"`
}

// audysseyState holds the Audyssey room correction settings.
type audysseyState struct {
	Multeq               string `json:"multeq,omitempty"`
	DynamicEq            bool   `json:"dynamicEq"`
	ReferenceLevelOffset int    `json:"referenceLevelOffset"`
	DynamicVolume        string `json:"dynamicVolume,omitempty"`
	LoudnessManagement   bool   `json:"loudnessManagement"`
}

// audioState holds the audio processing settings.
type audioState struct {
	DRC                string `json:"drc,omitempty"`
	LFE                int    `json:"lfe"`
	Effect             int    `json:"effect"`
	Delay              int    `json:"delay"`
	AudioDelay         int    `json:"audioDelay"`
	Subwoofer          bool   `json:"subwoofer"`
	Restorer           string `json:"restorer,omitempty"`
	GraphicEq          string `json:"graphicEq,omitempty"`
	HeadphoneEq        string `json:"headphoneEq,omitempty"`
	SpeakerVirtualizer bool   `json:"speakerVirtualizer"`
	DialogEnhancer     string `json:"dialogEnhancer,omitempty"`
}

// denonState is everything one receiver has said. mainFromZM records
// that the model reports main-zone power through ZM, so the older PW
// lines stop standing in for it.
type denonState struct {
	Reachable equipment.ConditionStatus
	System    systemState
	Main      zoneState
	Zone2     zoneState
	Zone3     zoneState
	Tone      toneState
	Audyssey  audysseyState
	Audio     audioState
	Channels  map[string]float64

	mainFromZM bool
}

// newDenonState is the state before the receiver has said anything.
func newDenonState() denonState {
	return denonState{
		Reachable: equipment.ConditionUnknown,
		Main:      zoneState{Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves},
		Zone2:     zoneState{Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves},
		Zone3:     zoneState{Volume: unknownHalves, VolumeMax: unknownHalves, Sleep: unknownHalves, Quick: unknownHalves},
		Channels:  map[string]float64{},
	}
}

// The field names a folded line can carry. The session cares about
// volume and mute, and the status writer is poked for every line
// whether or not it names a field.
const (
	sleepField = "sleep"
	quickField = "quick"
)

// applyDenonLine folds one line into the state and answers the zone and
// field it named. A line this driver does not know is ignored, because
// a Denon volunteers dozens of settings nothing here reads.
func applyDenonLine(state denonState, line string) (denonState, string, string, bool) {
	switch {
	case line == PowerOnCommand:
		state.System.Power = powerOn
		if !state.mainFromZM {
			state.Main.Power = equipment.PowerOn
			state.Main.seen = true
		}
		return state, equipment.MainZone, powerField, true
	case line == powerStandbyCommand:
		state.System.Power = powerStandby
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
		state.Main.SoundMode = line[2:]
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
		state.System.Eco = "on"
		return state, "", ecoField, true
	case line == "ECOOFF":
		state.System.Eco = "off"
		return state, "", ecoField, true
	case line == "ECOAUTO":
		state.System.Eco = "auto"
		return state, "", ecoField, true
	case strings.HasPrefix(line, "DIM "):
		dimmer, ok := dimmerWord(line[len("DIM "):])
		if !ok {
			return state, "", "", false
		}
		state.System.Dimmer = dimmer
		return state, "", dimmerField, true
	case strings.HasPrefix(line, "STBY"):
		standby, ok := autoStandbyWord(line[len("STBY"):])
		if !ok {
			return state, "", "", false
		}
		state.System.AutoStandby = standby
		return state, "", standbyField, true
	case strings.HasPrefix(line, "SPPR "):
		preset, err := strconv.Atoi(strings.TrimSpace(line[len("SPPR "):]))
		if err != nil {
			return state, "", "", false
		}
		state.System.SpeakerPreset = preset
		return state, "", speakerPresetField, true
	case strings.HasPrefix(line, "SD"):
		mode, ok := inputModeWord(line[2:])
		if !ok {
			return state, "", "", false
		}
		state.System.AudioInputMode = mode
		return state, "", inputModeField, true
	case strings.HasPrefix(line, "SV"):
		state.System.VideoSelect = videoSelect(line[2:])
		return state, "", videoSelectField, true
	case strings.HasPrefix(line, "BTTX "):
		mode, output, ok := bluetoothWord(line[len("BTTX "):])
		if !ok {
			return state, "", "", false
		}
		if mode != "" {
			state.System.BluetoothTransmitter = mode
		}
		if output != "" {
			state.System.BluetoothOutput = output
		}
		return state, "", bluetoothField, true
	case strings.HasPrefix(line, "CV"):
		channel, value, ok := parseChannelVolume(line)
		if !ok {
			return state, "", "", false
		}
		state.Channels[channel] = value
		return state, "", channelField, true
	case strings.HasPrefix(line, "PS"):
		field, ok := applyParameterLine(&state, line)
		return state, "", field, ok
	}
	return state, "", "", false
}

// applyZoneLine folds the body of a Z2 or Z3 line into one zone. The
// body is the line without its zone prefix, so "ON", "MUOFF", "SLP010",
// "90", or an input name.
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

// applyParameterLine folds one PS line, which is where the tone, the
// Audyssey, and the audio settings arrive.
func applyParameterLine(state *denonState, line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "PSMULTEQ:"):
		state.Audyssey.Multeq = multEqWord(strings.TrimSpace(line[len("PSMULTEQ:"):]))
		return multeqField, true
	case strings.HasPrefix(line, "PSDYNEQ "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSDYNEQ "):]))
		if !ok {
			return "", false
		}
		state.Audyssey.DynamicEq = on
		return dynamicEqField, true
	case strings.HasPrefix(line, "PSREFLEV "):
		offset, err := strconv.Atoi(strings.TrimSpace(line[len("PSREFLEV "):]))
		if err != nil {
			return "", false
		}
		state.Audyssey.ReferenceLevelOffset = offset
		return referenceLevelField, true
	case strings.HasPrefix(line, "PSDYNVOL "):
		state.Audyssey.DynamicVolume = dynamicVolumeWord(strings.TrimSpace(line[len("PSDYNVOL "):]))
		return dynamicVolumeField, true
	case strings.HasPrefix(line, "PSLOM "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSLOM "):]))
		if !ok {
			return "", false
		}
		state.Audyssey.LoudnessManagement = on
		return loudnessField, true
	case strings.HasPrefix(line, "PSDRC "):
		state.Audio.DRC = strings.ToLower(strings.TrimSpace(line[len("PSDRC "):]))
		return drcField, true
	case strings.HasPrefix(line, "PSLFE "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSLFE "):]))
		if err != nil {
			return "", false
		}
		state.Audio.LFE = -value
		return lfeField, true
	case strings.HasPrefix(line, "PSEFF "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSEFF "):]))
		if err != nil {
			return "", false
		}
		state.Audio.Effect = value
		return effectField, true
	case strings.HasPrefix(line, "PSDELAY "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSDELAY "):]))
		if err != nil {
			return "", false
		}
		state.Audio.AudioDelay = value
		return delayField, true
	case strings.HasPrefix(line, "PSDEL "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSDEL "):]))
		if err != nil {
			return "", false
		}
		state.Audio.Delay = value
		return delayField, true
	case strings.HasPrefix(line, "PSSWR "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSSWR "):]))
		if !ok {
			return "", false
		}
		state.Audio.Subwoofer = on
		return subwooferField, true
	case strings.HasPrefix(line, "PSRSTR "):
		state.Audio.Restorer = restorerWord(strings.TrimSpace(line[len("PSRSTR "):]))
		return restorerField, true
	case strings.HasPrefix(line, "PSGEQ "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSGEQ "):]))
		if !ok {
			return "", false
		}
		state.Audio.GraphicEq = onOffText(on)
		return graphicEqField, true
	case strings.HasPrefix(line, "PSHEQ "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSHEQ "):]))
		if !ok {
			return "", false
		}
		state.Audio.HeadphoneEq = onOffText(on)
		return headphoneEqField, true
	case strings.HasPrefix(line, "PSSPV "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSSPV "):]))
		if !ok {
			return "", false
		}
		state.Audio.SpeakerVirtualizer = on
		return virtualizerField, true
	case strings.HasPrefix(line, "PSDEH "):
		state.Audio.DialogEnhancer = strings.ToLower(strings.TrimSpace(line[len("PSDEH "):]))
		return dialogEnhancerField, true
	case strings.HasPrefix(line, "PSBAS "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSBAS "):]))
		if err != nil {
			return "", false
		}
		state.Tone.Bass = value - 50
		return bassField, true
	case strings.HasPrefix(line, "PSTRE "):
		value, err := strconv.Atoi(strings.TrimSpace(line[len("PSTRE "):]))
		if err != nil {
			return "", false
		}
		state.Tone.Treble = value - 50
		return trebleField, true
	case strings.HasPrefix(line, "PSTONE CTRL "):
		on, ok := onOffWord(strings.TrimSpace(line[len("PSTONE CTRL "):]))
		if !ok {
			return "", false
		}
		state.Tone.Control = on
		return toneControlField, true
	}
	return "", false
}

// The field names the PS lines carry. None of them reaches the
// equipment contract, which names only the fields a session acts on.
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

// parseSleep reads a sleep timer body: OFF, or three digits of minutes.
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

// parseQuick reads a quick-select line and answers which slot it names.
func parseQuick(line string) (int, bool) {
	slot, err := strconv.Atoi(strings.TrimSpace(line[len("MSQUICK"):]))
	if err != nil || slot < 0 || slot > 5 {
		return unknownHalves, false
	}
	return slot, true
}

// parseChannelVolume reads one CV line and answers the channel and its
// trim in display units.
func parseChannelVolume(line string) (string, float64, bool) {
	body := line[len("CV"):]
	channel, digits, held := strings.Cut(body, " ")
	if !held || channel == "" {
		return "", 0, false
	}
	halves, ok := ParseHalfSteps(strings.TrimSpace(digits))
	if !ok {
		return "", 0, false
	}
	return channel, float64(halves)/2 - 50, true
}

// onOffWord reads ON or OFF.
func onOffWord(word string) (bool, bool) {
	switch word {
	case "ON":
		return true, true
	case "OFF":
		return false, true
	}
	return false, false
}

// onOffText writes a switch the way the status carries it.
func onOffText(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// dimmerWord reads the dimmer setting.
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

// autoStandbyWord reads the auto-standby timer.
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

// inputModeWord reads the audio input mode.
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

// videoSelect reads a video-select body, OFF or a source name.
func videoSelect(body string) string {
	if body == "OFF" || body == "" {
		return "off"
	}
	return body
}

// bluetoothWord reads a BTTX body and answers the mode, the output, or
// both. The receiver reports them as separate lines.
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

// multEqWord reads an Audyssey MultEQ mode.
func multEqWord(word string) string {
	switch word {
	case "AUDYSSEY":
		return "reference"
	case "BYP.LR":
		return "l/r bypass"
	case "FLAT":
		return "flat"
	case "MANUAL":
		return "manual"
	case "OFF":
		return "off"
	}
	return strings.ToLower(word)
}

// dynamicVolumeWord reads a Dynamic Volume setting.
func dynamicVolumeWord(word string) string {
	switch word {
	case "OFF":
		return "off"
	case "LIT":
		return "light"
	case "MED":
		return "medium"
	case "HEV":
		return "heavy"
	}
	return strings.ToLower(word)
}

// restorerWord reads an Audio Restorer setting.
func restorerWord(word string) string {
	switch word {
	case "OFF":
		return "off"
	case "LOW":
		return "low"
	case "MED":
		return "medium"
	case "HI":
		return "high"
	}
	return strings.ToLower(word)
}
