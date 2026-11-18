package denon

// The receiver's Video > HDMI Setup menu. HDMI Audio Out is the
// VSAUDIO command, which Denon's protocol documents list. The other
// fields are the SSHOS command family, which no Denon document lists.
// An AVR-X1700H answers "SSHOS ?" with one line per key and then
// "SSHOS END". It sent no line after an SSHOS set that left the value
// unchanged, and whether it echoes a change is not known, so the driver
// queries the family after every set. denon/AGENTS.md records the
// source of each wire word.

import (
	"fmt"
	"strings"
)

// HDMISettings is the HDMI setup and the HDMI-CEC switches under it.
type HDMISettings struct {
	AudioOut          *string `json:"audioOut,omitempty"`
	PassThrough       *bool   `json:"passThrough,omitempty"`
	PassThroughSource *string `json:"passThroughSource,omitempty"`
	RCSourceSelect    *string `json:"rcSourceSelect,omitempty"`
	Control           *bool   `json:"control,omitempty"`
	ARC               *bool   `json:"arc,omitempty"`
	TVAudioSwitching  *bool   `json:"tvAudioSwitching,omitempty"`
	PowerOffControl   *string `json:"powerOffControl,omitempty"`
	PowerSaving       *bool   `json:"powerSaving,omitempty"`
}

// hdmiSettings are the HDMI rows of settingsTable.
var hdmiSettings = []settingSpec{
	wordSetting("hdmi.audioOut", HDMIAudioOutCommand,
		func(s *Settings, v string) { s.HDMI.AudioOut = strPtr(v) },
		func(s *Settings) *string { return s.HDMI.AudioOut }),
	boolSetting("hdmi.passThrough", HDMIPassThroughCommand,
		func(s *Settings, v bool) { s.HDMI.PassThrough = boolPtr(v) },
		func(s *Settings) *bool { return s.HDMI.PassThrough }),
	wordSetting("hdmi.passThroughSource", HDMIPassThroughSourceCommand,
		func(s *Settings, v string) { s.HDMI.PassThroughSource = strPtr(v) },
		func(s *Settings) *string { return s.HDMI.PassThroughSource }),
	wordSetting("hdmi.rcSourceSelect", HDMIRCSourceSelectCommand,
		func(s *Settings, v string) { s.HDMI.RCSourceSelect = strPtr(v) },
		func(s *Settings) *string { return s.HDMI.RCSourceSelect }),
	boolSetting("hdmi.control", HDMIControlCommand,
		func(s *Settings, v bool) { s.HDMI.Control = boolPtr(v) },
		func(s *Settings) *bool { return s.HDMI.Control }),
	boolSetting("hdmi.arc", HDMIARCCommand,
		func(s *Settings, v bool) { s.HDMI.ARC = boolPtr(v) },
		func(s *Settings) *bool { return s.HDMI.ARC }),
	boolSetting("hdmi.tvAudioSwitching", HDMITVAudioSwitchingCommand,
		func(s *Settings, v bool) { s.HDMI.TVAudioSwitching = boolPtr(v) },
		func(s *Settings) *bool { return s.HDMI.TVAudioSwitching }),
	wordSetting("hdmi.powerOffControl", HDMIPowerOffControlCommand,
		func(s *Settings, v string) { s.HDMI.PowerOffControl = strPtr(v) },
		func(s *Settings) *string { return s.HDMI.PowerOffControl }),
	boolSetting("hdmi.powerSaving", HDMIPowerSavingCommand,
		func(s *Settings, v bool) { s.HDMI.PowerSaving = boolPtr(v) },
		func(s *Settings) *bool { return s.HDMI.PowerSaving }),
}

// confirmedBy is the HDMI part of Settings.ConfirmedBy. A field that w
// does not declare, or that observed does not report, is not compared.
func (w HDMISettings) confirmedBy(observed HDMISettings) bool {
	return sameString(w.AudioOut, observed.AudioOut) &&
		sameBool(w.PassThrough, observed.PassThrough) &&
		sameString(w.PassThroughSource, observed.PassThroughSource) &&
		sameString(w.RCSourceSelect, observed.RCSourceSelect) &&
		sameBool(w.Control, observed.Control) &&
		sameBool(w.ARC, observed.ARC) &&
		sameBool(w.TVAudioSwitching, observed.TVAudioSwitching) &&
		sameString(w.PowerOffControl, observed.PowerOffControl) &&
		sameBool(w.PowerSaving, observed.PowerSaving)
}

// A set command is the key, a space, and the word, and the receiver
// reports a value in a line of the same form.
const (
	hdmiAudioOutKey          = "VSAUDIO"
	hdmiPassThroughKey       = "SSHOSPAS"
	hdmiPassThroughSourceKey = "SSHOSCONSTS"
	hdmiRCSourceSelectKey    = "SSHOSRSS"
	hdmiControlKey           = "SSHOSCON"
	hdmiARCKey               = "SSHOSCONARC"
	hdmiTVAudioSwitchingKey  = "SSHOSTAS"
	hdmiPowerOffControlKey   = "SSHOSCONPOF"
	hdmiPowerSavingKey       = "SSHOSCONPSV"

	// hdmiFamilyEnd is the last line of the reply to "SSHOS ?".
	hdmiFamilyEnd = "SSHOS END"
)

const (
	hdmiAudioOutQuery = "VSAUDIO ?"
	hdmiSetupQuery    = "SSHOS ?"
)

// Each map takes the value that the status reports to its wire word.
var (
	hdmiAudioOutWords = map[string]string{"avr": "AMP", "tv": "TV"}

	hdmiPassThroughSourceWords = map[string]string{
		"last": "LAS", "hdmi1": "HD1", "hdmi2": "HD2", "hdmi3": "HD3",
		"hdmi4": "HD4", "hdmi5": "HD5", "hdmi6": "HD6", "hdmi7": "HD7",
	}

	hdmiRCSourceSelectWords = map[string]string{
		"powerOnAndSource": "POS", "sourceSelectOnly": "SSO",
	}

	hdmiPowerOffControlWords = map[string]string{
		"all": "ALL", "video": "VID", "off": "OFF",
	}
)

// hdmiWordCommand returns an error naming the setting and the value
// when the value has no wire word.
func hdmiWordCommand(key, setting string, words map[string]string, value string) (string, error) {
	word, known := words[value]
	if !known {
		return "", fmt.Errorf("%q is not a value of %s", value, setting)
	}
	return key + " " + word, nil
}

// HDMIAudioOutCommand returns the set command that plays HDMI audio
// through the receiver's amplifier ("avr") or the TV ("tv").
func HDMIAudioOutCommand(value string) (string, error) {
	return hdmiWordCommand(hdmiAudioOutKey, "HDMI audio out", hdmiAudioOutWords, value)
}

// HDMIPassThroughCommand returns the set command for HDMI Pass Through,
// which sends a source to the TV while the receiver is in standby.
func HDMIPassThroughCommand(on bool) (string, error) {
	return hdmiPassThroughKey + " " + onOffWire(on), nil
}

// HDMIPassThroughSourceCommand returns the set command for the source
// that passes through in standby: "last" for the last one selected, or
// "hdmi1" to "hdmi7".
func HDMIPassThroughSourceCommand(value string) (string, error) {
	return hdmiWordCommand(hdmiPassThroughSourceKey, "pass through source", hdmiPassThroughSourceWords, value)
}

// HDMIRCSourceSelectCommand returns the set command for what a source
// button on the remote does while the receiver is in standby.
func HDMIRCSourceSelectCommand(value string) (string, error) {
	return hdmiWordCommand(hdmiRCSourceSelectKey, "RC source select", hdmiRCSourceSelectWords, value)
}

// HDMIControlCommand returns the set command for HDMI Control, the
// receiver's HDMI-CEC switch.
func HDMIControlCommand(on bool) (string, error) {
	return hdmiControlKey + " " + onOffWire(on), nil
}

// HDMIARCCommand returns the set command for the Audio Return Channel.
func HDMIARCCommand(on bool) (string, error) {
	return hdmiARCKey + " " + onOffWire(on), nil
}

// HDMITVAudioSwitchingCommand returns the set command for TV Audio
// Switching, which selects the TV input on the receiver when the TV
// plays its own sound.
func HDMITVAudioSwitchingCommand(on bool) (string, error) {
	return hdmiTVAudioSwitchingKey + " " + onOffWire(on), nil
}

// HDMIPowerOffControlCommand returns the set command for which devices
// turn off when the TV turns off.
func HDMIPowerOffControlCommand(value string) (string, error) {
	return hdmiWordCommand(hdmiPowerOffControlKey, "power off control", hdmiPowerOffControlWords, value)
}

// HDMIPowerSavingCommand returns the set command for the power saving
// switch of HDMI Control.
func HDMIPowerSavingCommand(on bool) (string, error) {
	return hdmiPowerSavingKey + " " + onOffWire(on), nil
}

// applyHDMILine stores the value of one VSAUDIO or SSHOS line in hdmi
// and returns hdmiField and true, or "" and false when it does not
// recognize the line. A word that no command builder produces, and a
// key the driver does not read, change no field, because the operator
// could not send such a value back.
func applyHDMILine(hdmi *HDMISettings, line string) (string, bool) {
	if line == hdmiFamilyEnd {
		return hdmiField, true
	}
	key, word, held := strings.Cut(line, " ")
	if !held {
		return "", false
	}
	switch key {
	case hdmiAudioOutKey:
		return foldHDMIWord(&hdmi.AudioOut, hdmiAudioOutWords, word)
	case hdmiPassThroughSourceKey:
		return foldHDMIWord(&hdmi.PassThroughSource, hdmiPassThroughSourceWords, word)
	case hdmiRCSourceSelectKey:
		return foldHDMIWord(&hdmi.RCSourceSelect, hdmiRCSourceSelectWords, word)
	case hdmiPowerOffControlKey:
		return foldHDMIWord(&hdmi.PowerOffControl, hdmiPowerOffControlWords, word)
	case hdmiPassThroughKey:
		return foldHDMISwitch(&hdmi.PassThrough, word)
	case hdmiControlKey:
		return foldHDMISwitch(&hdmi.Control, word)
	case hdmiARCKey:
		return foldHDMISwitch(&hdmi.ARC, word)
	case hdmiTVAudioSwitchingKey:
		return foldHDMISwitch(&hdmi.TVAudioSwitching, word)
	case hdmiPowerSavingKey:
		return foldHDMISwitch(&hdmi.PowerSaving, word)
	}
	return "", false
}

func foldHDMIWord(field **string, words map[string]string, word string) (string, bool) {
	for value, wire := range words {
		if wire == word {
			*field = strPtr(value)
			return hdmiField, true
		}
	}
	return "", false
}

func foldHDMISwitch(field **bool, word string) (string, bool) {
	on, ok := onOffWord(word)
	if !ok {
		return "", false
	}
	*field = boolPtr(on)
	return hdmiField, true
}

// hdmiField is the field name that applyHDMILine returns for every HDMI
// line. equipmentField maps it to no equipment event.
const hdmiField = "hdmi"

// hdmiReadBacks returns the queries that read back the HDMI commands
// among sent: at most one "VSAUDIO ?" and then at most one "SSHOS ?".
func hdmiReadBacks(sent []string) []string {
	var audioOut, setup bool
	for _, command := range sent {
		audioOut = audioOut || strings.HasPrefix(command, hdmiAudioOutKey+" ")
		setup = setup || strings.HasPrefix(command, "SSHOS")
	}
	var queries []string
	if audioOut {
		queries = append(queries, hdmiAudioOutQuery)
	}
	if setup {
		queries = append(queries, hdmiSetupQuery)
	}
	return queries
}

func (d *Client) sendReadBacks(sent []string) error {
	for _, query := range hdmiReadBacks(sent) {
		if err := d.send(query); err != nil {
			return err
		}
	}
	return nil
}
