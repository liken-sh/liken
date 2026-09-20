package denon

// Settings is the receiver's settings vocabulary, one struct per family
// the driver parses. Every optional scalar is a pointer: nil means the
// key is not declared, and a present zero means the key is set to zero.
// That is what lets ApplySettings apply only the declared fields.
//
// The id table maps a stable bus id to the field it names and the wire
// command that sets it. A new family is a struct, a file of specs, and
// a line in the assembly below.

import (
	"fmt"
	"math"
	"strings"

	"github.com/liken-sh/equipment-operator/equipment"
)

// Settings holds the receiver's settings across the families the driver
// parses.
type Settings struct {
	System         SystemSettings     `json:"system"`
	Tone           ToneSettings       `json:"tone"`
	Audyssey       AudysseySettings   `json:"audyssey"`
	Audio          AudioSettings      `json:"audio"`
	ChannelVolumes map[string]float64 `json:"channelVolumes,omitempty"`
}

// settingSpec is one row of the id table: a stable bus id, how to fold
// one bus value into the settings and answer the wire command, and how
// to answer the wire command for a declared field.
type settingSpec struct {
	ID string
	// parse folds one bus value into the settings and answers the wire
	// command.
	parse func(s *Settings, v equipment.SettingValue) (string, error)
	// command answers the wire command a declared field carries, the
	// error a declared value that no command can carry returns, and
	// whether the settings declare the field at all.
	command func(s *Settings) (string, error, bool)
}

// settingsTable is the assembled id table. The channel volumes are not
// here, because their ids name a channel and so cannot be enumerated.
var settingsTable = append(append(append(append(
	[]settingSpec{}, systemSettings...),
	toneSettings...),
	audysseySettings...),
	audioSettings...)

// settingsById is the same table as a map for a bus write that names
// one id.
var settingsById = func() map[string]settingSpec {
	table := make(map[string]settingSpec, len(settingsTable))
	for _, spec := range settingsTable {
		table[spec.ID] = spec
	}
	return table
}()

// The small pointer builders the families share. A pointer field is nil
// when the receiver has not reported the value, which is how the status
// tells a key the receiver never declared from one set to zero.
func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

// Settings returns every setting the receiver has reported. Every
// pointer and the channel map are copied, so the returned value never
// aliases the live state a reader goroutine keeps folding into.
func (d *Client) Settings() Settings {
	d.mutex.Lock()
	s := d.state.Settings
	deepCopySettings(&s)
	d.mutex.Unlock()
	return s
}

// deepCopySettings copies every pointer pointee and the channel map in
// place, so a caller mutating a returned Settings cannot change what
// the next Settings() reads.
func deepCopySettings(s *Settings) {
	copyString := func(p **string) {
		if *p != nil {
			v := **p
			*p = &v
		}
	}
	copyInt := func(p **int) {
		if *p != nil {
			v := **p
			*p = &v
		}
	}
	copyBool := func(p **bool) {
		if *p != nil {
			v := **p
			*p = &v
		}
	}

	copyString(&s.System.Eco)
	copyString(&s.System.Dimmer)
	copyString(&s.System.AutoStandby)
	copyInt(&s.System.SpeakerPreset)
	copyString(&s.System.AudioInputMode)
	copyString(&s.System.VideoSelect)
	copyString(&s.System.BluetoothTransmitter)
	copyString(&s.System.BluetoothOutput)

	copyBool(&s.Tone.Control)
	copyInt(&s.Tone.Bass)
	copyInt(&s.Tone.Treble)

	copyString(&s.Audyssey.Multeq)
	copyBool(&s.Audyssey.DynamicEq)
	copyInt(&s.Audyssey.ReferenceLevelOffset)
	copyString(&s.Audyssey.DynamicVolume)
	copyBool(&s.Audyssey.LoudnessManagement)

	copyString(&s.Audio.DRC)
	copyInt(&s.Audio.LFE)
	copyInt(&s.Audio.Effect)
	copyInt(&s.Audio.Delay)
	copyInt(&s.Audio.AudioDelay)
	copyBool(&s.Audio.Subwoofer)
	copyString(&s.Audio.Restorer)
	copyString(&s.Audio.GraphicEq)
	copyString(&s.Audio.HeadphoneEq)
	copyBool(&s.Audio.SpeakerVirtualizer)
	copyString(&s.Audio.DialogEnhancer)

	if s.ChannelVolumes != nil {
		copied := make(map[string]float64, len(s.ChannelVolumes))
		for channel, value := range s.ChannelVolumes {
			copied[channel] = value
		}
		s.ChannelVolumes = copied
	}
}

// ApplySettings sends the wire command for every declared field. The
// controller calls it only when the spec changed, so it does not
// re-assert by itself, and an undeclared field sends nothing.
func (d *Client) ApplySettings(want Settings) error {
	for _, spec := range settingsTable {
		if command, err, declared := spec.command(&want); declared {
			if err != nil {
				return fmt.Errorf("setting %s: %w", spec.ID, err)
			}
			if err := d.send(command); err != nil {
				return err
			}
		}
	}
	commands, err := channelCommands(&want)
	if err != nil {
		return err
	}
	for _, command := range commands {
		if err := d.send(command); err != nil {
			return err
		}
	}
	return nil
}

// Set builds the wire command for one keyed write from the bus. It
// folds the value into a scratch Settings and not into the live state:
// the status reports what the receiver said, so a command that never
// reached the receiver must not leave a value the status would claim.
// The receiver's own echo is what moves the live setting. An unknown id
// is an error that names the id.
func (d *Client) Set(id string, value equipment.SettingValue) error {
	var scratch Settings
	command, err := applySetting(&scratch, id, value)
	if err != nil {
		return err
	}
	return d.send(command)
}

// applySetting folds one bus value into the settings and answers the
// wire command, routing a channel id to the channel family and any
// other id to the fixed table.
func applySetting(s *Settings, id string, value equipment.SettingValue) (string, error) {
	if strings.HasPrefix(id, channelPrefix) {
		return setChannel(s, strings.TrimPrefix(id, channelPrefix), value)
	}
	spec, held := settingsById[id]
	if !held {
		return "", fmt.Errorf("unknown setting %q", id)
	}
	return spec.parse(s, value)
}

// Do runs one one-shot action from the bus. Phase 1 defines no actions,
// so every id is an error that names it.
func (d *Client) Do(id string, args map[string]equipment.SettingValue) error {
	return fmt.Errorf("no actions yet: %s", id)
}

// settingTypeError is the error a setting that received the wrong kind
// of value carries.
func settingTypeError(id, kind string) error {
	return fmt.Errorf("setting %s needs %s", id, kind)
}

// wordSetting builds a table row for a setting that carries a word: it
// reads the string, builds the command, and stores the value in the
// settings.
func wordSetting(id string, build func(string) (string, error), set func(*Settings, string), get func(*Settings) *string) settingSpec {
	return settingSpec{
		ID: id,
		parse: func(s *Settings, v equipment.SettingValue) (string, error) {
			value, ok := v.String()
			if !ok {
				return "", settingTypeError(id, "a string")
			}
			command, err := build(value)
			if err != nil {
				return "", err
			}
			set(s, value)
			return command, nil
		},
		command: func(s *Settings) (string, error, bool) {
			value := get(s)
			if value == nil {
				return "", nil, false
			}
			command, err := build(*value)
			return command, err, true
		},
	}
}

// numberSetting builds a table row for a setting that carries an
// integer.
func numberSetting(id string, build func(int) (string, error), set func(*Settings, int), get func(*Settings) *int) settingSpec {
	return settingSpec{
		ID: id,
		parse: func(s *Settings, v equipment.SettingValue) (string, error) {
			value, ok := v.Number()
			if !ok {
				return "", settingTypeError(id, "a number")
			}
			if value != math.Trunc(value) {
				return "", fmt.Errorf("setting %s needs a whole number", id)
			}
			whole := int(value)
			command, err := build(whole)
			if err != nil {
				return "", err
			}
			set(s, whole)
			return command, nil
		},
		command: func(s *Settings) (string, error, bool) {
			value := get(s)
			if value == nil {
				return "", nil, false
			}
			command, err := build(*value)
			return command, err, true
		},
	}
}

// boolSetting builds a table row for a setting that carries a switch.
func boolSetting(id string, build func(bool) (string, error), set func(*Settings, bool), get func(*Settings) *bool) settingSpec {
	return settingSpec{
		ID: id,
		parse: func(s *Settings, v equipment.SettingValue) (string, error) {
			value, ok := v.Bool()
			if !ok {
				return "", settingTypeError(id, "a boolean")
			}
			command, err := build(value)
			if err != nil {
				return "", err
			}
			set(s, value)
			return command, nil
		},
		command: func(s *Settings) (string, error, bool) {
			value := get(s)
			if value == nil {
				return "", nil, false
			}
			command, err := build(*value)
			return command, err, true
		},
	}
}
