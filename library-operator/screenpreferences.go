package main

import "encoding/json"

// The variable that carries a Player's resolved language preferences into
// its browser, as JSON with media-operator's field names. The browser marks
// the audio and subtitle tracks that a play of a film will take on the
// film's page, and it reads no Kubernetes object, so the operator resolves
// the preferences and writes them here. A Player whose tiers state no
// preference carries no variable.
const mediaPreferencesVariable = "LIBRARY_MEDIA_PREFERENCES"

// screenPreferences resolves a Player's language preferences the way
// media-operator's resolvePreferences does for a Play: each field from the
// Player where the Player states it, and from the household's
// MediaPreferences where it does not. A Play can state its own preferences
// too, but the browser draws the page before any Play exists, so the
// resolution stops at the Player. The answer is the JSON the browser reads,
// or empty where no tier states a field.
//
// The variable is part of the pod's template, so a change to the
// preferences replaces the screen pod, the way a change to the zone does.
func screenPreferences(player *Player, household *MediaPreferencesSpec) string {
	resolved := player.Spec
	if household != nil {
		if resolved.AudioLanguages == nil {
			resolved.AudioLanguages = household.AudioLanguages
		}
		if resolved.SubtitleLanguages == nil {
			resolved.SubtitleLanguages = household.SubtitleLanguages
		}
		if resolved.Subtitles == "" {
			resolved.Subtitles = household.Subtitles
		}
		if resolved.SDH == "" {
			resolved.SDH = household.SDH
		}
	}
	encoded, _ := json.Marshal(resolved)
	if string(encoded) == "{}" {
		return ""
	}
	return string(encoded)
}
