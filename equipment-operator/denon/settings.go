package denon

// Every optional setting is a pointer, so that nil means the field is
// not declared and a pointer to zero declares zero. ApplySettings sends
// only the declared fields.
//
// settingsTable has one row for each field, with its dotted id and the
// command that sets it. A new field also needs a line in ConfirmedBy and
// in deepCopySettings. A test in settings_test.go fails when ConfirmedBy
// does not compare a field.

import "fmt"

type Settings struct {
	System         SystemSettings     `json:"system"`
	Tone           ToneSettings       `json:"tone"`
	Audyssey       AudysseySettings   `json:"audyssey"`
	Audio          AudioSettings      `json:"audio"`
	HDMI           HDMISettings       `json:"hdmi"`
	ChannelVolumes map[string]float64 `json:"channelVolumes,omitempty"`
}

// settingSpec is one row of settingsTable. ID appears in error messages.
type settingSpec struct {
	ID string
	// command returns the wire command for the field, an error when the
	// declared value has no wire command, and whether s declares the
	// field at all.
	command func(s *Settings) (string, error, bool)
	// take copies the field from src into dst when src declares it. It
	// lets Pending build a Settings with only the fields to send.
	take func(dst, src *Settings)
}

// settingsTable joins the specs of every family. ChannelVolumes has no
// row, because its keys are channel names and not a fixed list.
// ApplySettings sends them with channelCommands.
var settingsTable = append(append(append(append(append(
	[]settingSpec{}, systemSettings...),
	toneSettings...),
	audysseySettings...),
	audioSettings...),
	hdmiSettings...)

// sameString returns true when both values are equal or either is nil,
// because ConfirmedBy does not compare a field that is not declared or
// that the receiver has not reported.
func sameString(a, b *string) bool { return a == nil || b == nil || *a == *b }

// sameInt is sameString for integers.
func sameInt(a, b *int) bool { return a == nil || b == nil || *a == *b }

// sameBool is sameString for booleans.
func sameBool(a, b *bool) bool { return a == nil || b == nil || *a == *b }

// ConfirmedBy returns true when observed reports the declared value of
// every declared field. It skips a field that observed does not report,
// because waiting for a value that never arrives would retry forever.
func (w Settings) ConfirmedBy(observed Settings) bool {
	if !sameString(w.System.Eco, observed.System.Eco) {
		return false
	}
	if !sameString(w.System.Dimmer, observed.System.Dimmer) {
		return false
	}
	if !sameString(w.System.AutoStandby, observed.System.AutoStandby) {
		return false
	}
	if !sameInt(w.System.SpeakerPreset, observed.System.SpeakerPreset) {
		return false
	}
	if !sameString(w.System.AudioInputMode, observed.System.AudioInputMode) {
		return false
	}
	if !sameString(w.System.VideoSelect, observed.System.VideoSelect) {
		return false
	}
	if !sameString(w.System.BluetoothTransmitter, observed.System.BluetoothTransmitter) {
		return false
	}
	if !sameString(w.System.BluetoothOutput, observed.System.BluetoothOutput) {
		return false
	}
	if !sameBool(w.Tone.Control, observed.Tone.Control) {
		return false
	}
	if !sameInt(w.Tone.Bass, observed.Tone.Bass) {
		return false
	}
	if !sameInt(w.Tone.Treble, observed.Tone.Treble) {
		return false
	}
	if !sameString(w.Audyssey.Multeq, observed.Audyssey.Multeq) {
		return false
	}
	if !sameBool(w.Audyssey.DynamicEq, observed.Audyssey.DynamicEq) {
		return false
	}
	if !sameInt(w.Audyssey.ReferenceLevelOffset, observed.Audyssey.ReferenceLevelOffset) {
		return false
	}
	if !sameString(w.Audyssey.DynamicVolume, observed.Audyssey.DynamicVolume) {
		return false
	}
	if !sameBool(w.Audyssey.LoudnessManagement, observed.Audyssey.LoudnessManagement) {
		return false
	}
	if !sameString(w.Audio.DRC, observed.Audio.DRC) {
		return false
	}
	if !sameInt(w.Audio.LFE, observed.Audio.LFE) {
		return false
	}
	if !sameInt(w.Audio.Effect, observed.Audio.Effect) {
		return false
	}
	if !sameInt(w.Audio.Delay, observed.Audio.Delay) {
		return false
	}
	if !sameInt(w.Audio.AudioDelay, observed.Audio.AudioDelay) {
		return false
	}
	if !sameBool(w.Audio.Subwoofer, observed.Audio.Subwoofer) {
		return false
	}
	if !sameString(w.Audio.Restorer, observed.Audio.Restorer) {
		return false
	}
	if !sameString(w.Audio.GraphicEq, observed.Audio.GraphicEq) {
		return false
	}
	if !sameString(w.Audio.HeadphoneEq, observed.Audio.HeadphoneEq) {
		return false
	}
	if !sameBool(w.Audio.SpeakerVirtualizer, observed.Audio.SpeakerVirtualizer) {
		return false
	}
	if !sameString(w.Audio.DialogEnhancer, observed.Audio.DialogEnhancer) {
		return false
	}
	if !w.HDMI.confirmedBy(observed.HDMI) {
		return false
	}
	for channel, want := range w.ChannelVolumes {
		got, held := observed.ChannelVolumes[channel]
		if held && want != got {
			return false
		}
	}
	return true
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

// Settings returns a copy of every setting the receiver has reported.
// The copy shares no pointer or map with the client's state, because
// the goroutine that reads from the receiver keeps updating that state.
func (d *Client) Settings() Settings {
	d.mutex.Lock()
	s := d.state.Settings
	deepCopySettings(&s)
	d.mutex.Unlock()
	return s
}

// deepCopySettings replaces every pointer and the channel map of s with
// copies.
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

	copyString(&s.HDMI.AudioOut)
	copyBool(&s.HDMI.PassThrough)
	copyString(&s.HDMI.PassThroughSource)
	copyString(&s.HDMI.RCSourceSelect)
	copyBool(&s.HDMI.Control)
	copyBool(&s.HDMI.ARC)
	copyBool(&s.HDMI.TVAudioSwitching)
	copyString(&s.HDMI.PowerOffControl)
	copyBool(&s.HDMI.PowerSaving)

	if s.ChannelVolumes != nil {
		copied := make(map[string]float64, len(s.ChannelVolumes))
		for channel, value := range s.ChannelVolumes {
			copied[channel] = value
		}
		s.ChannelVolumes = copied
	}
}

// ApplySettings sends the command of each declared field, then the
// channel volumes. The caller passes the result of Pending. The receiver
// does not echo an HDMI command, so after the field commands
// ApplySettings sends queries that read the HDMI settings back. It
// stops at the first error, so an earlier command may already have
// reached the receiver.
func (d *Client) ApplySettings(want Settings) error {
	var sent []string
	for _, spec := range settingsTable {
		if command, err, declared := spec.command(&want); declared {
			if err != nil {
				return fmt.Errorf("building the command of setting %s: %w", spec.ID, err)
			}
			if err := d.send(command); err != nil {
				return err
			}
			sent = append(sent, command)
		}
	}
	if err := d.sendReadBacks(sent); err != nil {
		return err
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

// wordSetting builds the settingsTable row of a setting whose value is a
// word.
func wordSetting(id string, build func(string) (string, error), set func(*Settings, string), get func(*Settings) *string) settingSpec {
	return settingSpec{
		ID: id,
		command: func(s *Settings) (string, error, bool) {
			value := get(s)
			if value == nil {
				return "", nil, false
			}
			command, err := build(*value)
			return command, err, true
		},
		take: func(dst, src *Settings) {
			if value := get(src); value != nil {
				set(dst, *value)
			}
		},
	}
}

// numberSetting builds the settingsTable row of a setting whose value is
// an integer.
func numberSetting(id string, build func(int) (string, error), set func(*Settings, int), get func(*Settings) *int) settingSpec {
	return settingSpec{
		ID: id,
		command: func(s *Settings) (string, error, bool) {
			value := get(s)
			if value == nil {
				return "", nil, false
			}
			command, err := build(*value)
			return command, err, true
		},
		take: func(dst, src *Settings) {
			if value := get(src); value != nil {
				set(dst, *value)
			}
		},
	}
}

// boolSetting builds the settingsTable row of a setting whose value is a
// boolean.
func boolSetting(id string, build func(bool) (string, error), set func(*Settings, bool), get func(*Settings) *bool) settingSpec {
	return settingSpec{
		ID: id,
		command: func(s *Settings) (string, error, bool) {
			value := get(s)
			if value == nil {
				return "", nil, false
			}
			command, err := build(*value)
			return command, err, true
		},
		take: func(dst, src *Settings) {
			if value := get(src); value != nil {
				set(dst, *value)
			}
		},
	}
}
