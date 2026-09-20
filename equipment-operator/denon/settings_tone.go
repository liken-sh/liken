package denon

// ToneSettings holds the tone control and the two trims in display
// units, where the wire's 50 is 0dB.

// ToneSettings holds the tone control switch and the two trims.
type ToneSettings struct {
	Control *bool `json:"control,omitempty"`
	Bass    *int  `json:"bass,omitempty"`
	Treble  *int  `json:"treble,omitempty"`
}

// toneSettings is the tone family's rows of the id table.
var toneSettings = []settingSpec{
	boolSetting("tone.control", ToneControlCommand,
		func(s *Settings, v bool) { s.Tone.Control = boolPtr(v) },
		func(s *Settings) *bool { return s.Tone.Control }),
	numberSetting("tone.bass", BassCommand,
		func(s *Settings, v int) { s.Tone.Bass = intPtr(v) },
		func(s *Settings) *int { return s.Tone.Bass }),
	numberSetting("tone.treble", TrebleCommand,
		func(s *Settings, v int) { s.Tone.Treble = intPtr(v) },
		func(s *Settings) *int { return s.Tone.Treble }),
}
