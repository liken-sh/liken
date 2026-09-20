package denon

// SystemSettings is the unit-wide settings. Power is reported but never
// declared, because the controller owns power and the other settings
// here are the ones a keyed write can set.

// SystemSettings holds the unit-wide settings.
type SystemSettings struct {
	Power                string  `json:"power,omitempty"`
	Eco                  *string `json:"eco,omitempty"`
	Dimmer               *string `json:"dimmer,omitempty"`
	AutoStandby          *string `json:"autoStandby,omitempty"`
	SpeakerPreset        *int    `json:"speakerPreset,omitempty"`
	AudioInputMode       *string `json:"audioInputMode,omitempty"`
	VideoSelect          *string `json:"videoSelect,omitempty"`
	BluetoothTransmitter *string `json:"bluetoothTransmitter,omitempty"`
	BluetoothOutput      *string `json:"bluetoothOutput,omitempty"`
}

// systemSettings is the system family's rows of the id table.
var systemSettings = []settingSpec{
	wordSetting("system.eco", EcoCommand,
		func(s *Settings, v string) { s.System.Eco = strPtr(v) },
		func(s *Settings) *string { return s.System.Eco }),
	wordSetting("system.dimmer", DimmerCommand,
		func(s *Settings, v string) { s.System.Dimmer = strPtr(v) },
		func(s *Settings) *string { return s.System.Dimmer }),
	wordSetting("system.autoStandby", AutoStandbyCommand,
		func(s *Settings, v string) { s.System.AutoStandby = strPtr(v) },
		func(s *Settings) *string { return s.System.AutoStandby }),
	numberSetting("system.speakerPreset", SpeakerPresetCommand,
		func(s *Settings, v int) { s.System.SpeakerPreset = intPtr(v) },
		func(s *Settings) *int { return s.System.SpeakerPreset }),
	wordSetting("system.audioInputMode", AudioInputModeCommand,
		func(s *Settings, v string) { s.System.AudioInputMode = strPtr(v) },
		func(s *Settings) *string { return s.System.AudioInputMode }),
	wordSetting("system.videoSelect", VideoSelectCommand,
		func(s *Settings, v string) { s.System.VideoSelect = strPtr(v) },
		func(s *Settings) *string { return s.System.VideoSelect }),
	wordSetting("system.bluetoothTransmitter", BluetoothTransmitterCommand,
		func(s *Settings, v string) { s.System.BluetoothTransmitter = strPtr(v) },
		func(s *Settings) *string { return s.System.BluetoothTransmitter }),
	wordSetting("system.bluetoothOutput", BluetoothOutputCommand,
		func(s *Settings, v string) { s.System.BluetoothOutput = strPtr(v) },
		func(s *Settings) *string { return s.System.BluetoothOutput }),
}
