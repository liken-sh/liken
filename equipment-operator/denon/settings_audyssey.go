package denon

// AudysseySettings holds the Audyssey room correction settings.

// AudysseySettings holds the Audyssey room correction settings.
type AudysseySettings struct {
	Multeq               *string `json:"multeq,omitempty"`
	DynamicEq            *bool   `json:"dynamicEq,omitempty"`
	ReferenceLevelOffset *int    `json:"referenceLevelOffset,omitempty"`
	DynamicVolume        *string `json:"dynamicVolume,omitempty"`
	LoudnessManagement   *bool   `json:"loudnessManagement,omitempty"`
}

// audysseySettings is the Audyssey family's rows of the id table.
var audysseySettings = []settingSpec{
	wordSetting("audyssey.multeq", MultEqCommand,
		func(s *Settings, v string) { s.Audyssey.Multeq = strPtr(v) },
		func(s *Settings) *string { return s.Audyssey.Multeq }),
	boolSetting("audyssey.dynamicEq", DynamicEqCommand,
		func(s *Settings, v bool) { s.Audyssey.DynamicEq = boolPtr(v) },
		func(s *Settings) *bool { return s.Audyssey.DynamicEq }),
	numberSetting("audyssey.referenceLevelOffset", ReferenceLevelOffsetCommand,
		func(s *Settings, v int) { s.Audyssey.ReferenceLevelOffset = intPtr(v) },
		func(s *Settings) *int { return s.Audyssey.ReferenceLevelOffset }),
	wordSetting("audyssey.dynamicVolume", DynamicVolumeCommand,
		func(s *Settings, v string) { s.Audyssey.DynamicVolume = strPtr(v) },
		func(s *Settings) *string { return s.Audyssey.DynamicVolume }),
	boolSetting("audyssey.loudnessManagement", LoudnessManagementCommand,
		func(s *Settings, v bool) { s.Audyssey.LoudnessManagement = boolPtr(v) },
		func(s *Settings) *bool { return s.Audyssey.LoudnessManagement }),
}
