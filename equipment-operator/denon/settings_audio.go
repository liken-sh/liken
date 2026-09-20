package denon

// AudioSettings holds the audio processing settings.

// AudioSettings holds the audio processing settings.
type AudioSettings struct {
	DRC                *string `json:"drc,omitempty"`
	LFE                *int    `json:"lfe,omitempty"`
	Effect             *int    `json:"effect,omitempty"`
	Delay              *int    `json:"delay,omitempty"`
	AudioDelay         *int    `json:"audioDelay,omitempty"`
	Subwoofer          *bool   `json:"subwoofer,omitempty"`
	Restorer           *string `json:"restorer,omitempty"`
	GraphicEq          *string `json:"graphicEq,omitempty"`
	HeadphoneEq        *string `json:"headphoneEq,omitempty"`
	SpeakerVirtualizer *bool   `json:"speakerVirtualizer,omitempty"`
	DialogEnhancer     *string `json:"dialogEnhancer,omitempty"`
}

// audioSettings is the audio family's rows of the id table.
var audioSettings = []settingSpec{
	wordSetting("audio.drc", DynamicRangeCommand,
		func(s *Settings, v string) { s.Audio.DRC = strPtr(v) },
		func(s *Settings) *string { return s.Audio.DRC }),
	numberSetting("audio.lfe", LFECommand,
		func(s *Settings, v int) { s.Audio.LFE = intPtr(v) },
		func(s *Settings) *int { return s.Audio.LFE }),
	numberSetting("audio.effect", EffectCommand,
		func(s *Settings, v int) { s.Audio.Effect = intPtr(v) },
		func(s *Settings) *int { return s.Audio.Effect }),
	numberSetting("audio.delay", DelayCommand,
		func(s *Settings, v int) { s.Audio.Delay = intPtr(v) },
		func(s *Settings) *int { return s.Audio.Delay }),
	numberSetting("audio.audioDelay", AudioDelayCommand,
		func(s *Settings, v int) { s.Audio.AudioDelay = intPtr(v) },
		func(s *Settings) *int { return s.Audio.AudioDelay }),
	boolSetting("audio.subwoofer", SubwooferCommand,
		func(s *Settings, v bool) { s.Audio.Subwoofer = boolPtr(v) },
		func(s *Settings) *bool { return s.Audio.Subwoofer }),
	wordSetting("audio.restorer", RestorerCommand,
		func(s *Settings, v string) { s.Audio.Restorer = strPtr(v) },
		func(s *Settings) *string { return s.Audio.Restorer }),
	wordSetting("audio.graphicEq", GraphicEqCommand,
		func(s *Settings, v string) { s.Audio.GraphicEq = strPtr(v) },
		func(s *Settings) *string { return s.Audio.GraphicEq }),
	wordSetting("audio.headphoneEq", HeadphoneEqCommand,
		func(s *Settings, v string) { s.Audio.HeadphoneEq = strPtr(v) },
		func(s *Settings) *string { return s.Audio.HeadphoneEq }),
	boolSetting("audio.speakerVirtualizer", SpeakerVirtualizerCommand,
		func(s *Settings, v bool) { s.Audio.SpeakerVirtualizer = boolPtr(v) },
		func(s *Settings) *bool { return s.Audio.SpeakerVirtualizer }),
	wordSetting("audio.dialogEnhancer", DialogEnhancerCommand,
		func(s *Settings, v string) { s.Audio.DialogEnhancer = strPtr(v) },
		func(s *Settings) *string { return s.Audio.DialogEnhancer }),
}
