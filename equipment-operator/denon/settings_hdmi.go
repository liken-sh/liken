package denon

// HDMISettings holds the receiver's HDMI setup, the Video > HDMI Setup
// menu. HDMI Audio Out is VSAUDIO, which Denon's protocol documents.
// The rest is the SSHOS family, which no Denon document lists: the
// AVR-X1700H answers SSHOS ? with one line per key and a closing
// SSHOS END. The AVR-X1700H sent no line after it took an SSHOS set
// command, so the driver sends the family's query after the sets and
// folds the answer. denon/AGENTS.md records which wire words were
// measured on the receiver and which come from other sources.

import (
	"fmt"
	"strings"
)

// HDMISettings holds the HDMI setup and the HDMI-CEC switches under it.
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

// hdmiSettings is the HDMI family's rows of the id table.
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

// confirmedBy answers whether the receiver reports every declared HDMI
// field at its declared value, the way Settings.ConfirmedBy judges the
// other families.
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

// The wire keys of the HDMI setup. Each set command is the key, a
// space, and the word; each report is the same line.
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

	// hdmiFamilyEnd closes the receiver's answer to SSHOS ?.
	hdmiFamilyEnd = "SSHOS END"
)

// The two read-back queries, one per family.
const (
	hdmiAudioOutQuery = "VSAUDIO ?"
	hdmiSetupQuery    = "SSHOS ?"
)

// The words each enumerated key carries, from the display value the
// status reports to the wire word.
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

// hdmiWordCommand builds the set command for one enumerated key, and
// the error that names the setting and the value when the word is not
// one the receiver carries.
func hdmiWordCommand(key, setting string, words map[string]string, value string) (string, error) {
	word, known := words[value]
	if !known {
		return "", fmt.Errorf("%s %q", setting, value)
	}
	return key + " " + word, nil
}

// HDMIAudioOutCommand is the set command for where HDMI audio plays:
// the receiver's own amplifier, or the TV.
func HDMIAudioOutCommand(value string) (string, error) {
	return hdmiWordCommand(hdmiAudioOutKey, "HDMI audio out", hdmiAudioOutWords, value)
}

// HDMIPassThroughCommand is the set command for HDMI Pass Through,
// which sends a source to the TV while the receiver is in standby.
func HDMIPassThroughCommand(on bool) (string, error) {
	return hdmiPassThroughKey + " " + onOffWire(on), nil
}

// HDMIPassThroughSourceCommand is the set command for the source that
// passes through in standby: the last one selected, or one HDMI jack.
func HDMIPassThroughSourceCommand(value string) (string, error) {
	return hdmiWordCommand(hdmiPassThroughSourceKey, "pass through source", hdmiPassThroughSourceWords, value)
}

// HDMIRCSourceSelectCommand is the set command for what a source button
// on the remote does while the receiver is in standby.
func HDMIRCSourceSelectCommand(value string) (string, error) {
	return hdmiWordCommand(hdmiRCSourceSelectKey, "RC source select", hdmiRCSourceSelectWords, value)
}

// HDMIControlCommand is the set command for HDMI Control, the
// receiver's HDMI-CEC switch.
func HDMIControlCommand(on bool) (string, error) {
	return hdmiControlKey + " " + onOffWire(on), nil
}

// HDMIARCCommand is the set command for the Audio Return Channel.
func HDMIARCCommand(on bool) (string, error) {
	return hdmiARCKey + " " + onOffWire(on), nil
}

// HDMITVAudioSwitchingCommand is the set command for TV Audio
// Switching, which selects the TV input when the TV plays its own
// sound.
func HDMITVAudioSwitchingCommand(on bool) (string, error) {
	return hdmiTVAudioSwitchingKey + " " + onOffWire(on), nil
}

// HDMIPowerOffControlCommand is the set command for which devices the
// TV's power off turns off with it.
func HDMIPowerOffControlCommand(value string) (string, error) {
	return hdmiWordCommand(hdmiPowerOffControlKey, "power off control", hdmiPowerOffControlWords, value)
}

// HDMIPowerSavingCommand is the set command for HDMI Control's power
// saving.
func HDMIPowerSavingCommand(on bool) (string, error) {
	return hdmiPowerSavingKey + " " + onOffWire(on), nil
}

// applyHDMILine folds one VSAUDIO or SSHOS line into the settings and
// answers the field it named. A word no builder accepts, and a key this
// driver does not read, are unknown, so the field stays nil rather than
// hold a value the operator could never send back.
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

// foldHDMIWord stores the display value whose wire word the line
// carries.
func foldHDMIWord(field **string, words map[string]string, word string) (string, bool) {
	for value, wire := range words {
		if wire == word {
			*field = strPtr(value)
			return hdmiField, true
		}
	}
	return "", false
}

// foldHDMISwitch stores the switch an ON or OFF line carries.
func foldHDMISwitch(field **bool, word string) (string, bool) {
	on, ok := onOffWord(word)
	if !ok {
		return "", false
	}
	*field = boolPtr(on)
	return hdmiField, true
}

// hdmiField is the field name every HDMI line carries. None of them
// reaches the equipment contract.
const hdmiField = "hdmi"

// hdmiReadBacks answers the queries that read back the HDMI commands
// among the sent ones, one per family and in a fixed order, because the
// receiver does not echo them.
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

// sendReadBacks sends the read-back queries for the sent commands.
func (d *Client) sendReadBacks(sent []string) error {
	for _, query := range hdmiReadBacks(sent) {
		if err := d.send(query); err != nil {
			return err
		}
	}
	return nil
}
