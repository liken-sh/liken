package main

// The command sidecar applies the subtitle rule of subtitletracks.go to
// each file mpv loads. mpv sends the file-loaded event after it has opened
// the file, loaded the subtitle files beside it, and selected the audio
// track, so the track list is complete and the audio language is known at
// that moment. The sidecar then reads the playlist position and the track
// list, and sets sid.
//
// The sidecar acts on file-loaded and not on a change of the observed
// current tracks, because a person who selects a subtitle track by hand
// changes the current tracks too, and the choice is for the moment a file
// starts. Each item of a Play is its own file load, so each item gets its
// own choice from its own presentation.
//
// The sidecar dials mpv's socket once a second, so mpv can load the first
// file before the sidecar connects, and that event reaches nobody. So the
// sidecar also chooses once when it connects, for the file mpv has open. A
// sidecar that restarts during an item chooses again for that item,
// because it keeps no record of its earlier choice.

import (
	"encoding/json"
	"fmt"
)

// The triggers of a choice, for its log line.
const (
	triggerConnected = "the sidecar connected to mpv's socket"
	triggerLoaded    = "mpv loaded a file"
)

// parseSubtitlePreferences reads the preferences the operator wrote into the
// pod. An unset or malformed value is a pod whose sidecar chooses nothing,
// so mpv keeps its own choice.
func parseSubtitlePreferences(value string) *subtitlePreferences {
	if value == "" {
		return nil
	}
	var prefs subtitlePreferences
	if err := json.Unmarshal([]byte(value), &prefs); err != nil {
		return nil
	}
	return &prefs
}

// serveLoads chooses the subtitle track once for the file mpv has open, and
// then once for each file mpv loads, until the socket closes.
func (c *commander) serveLoads(loads <-chan struct{}) {
	c.chooseSubtitles(triggerConnected)
	for range loads {
		c.chooseSubtitles(triggerLoaded)
	}
}

// chooseSubtitles reads what mpv has open and sets sid to the track the rule
// chooses. A read that fails leaves mpv's own choice, which the flags of
// mpvPreferenceOptions keep sensible.
func (c *commander) chooseSubtitles(trigger string) {
	if c.subtitles == nil {
		return
	}
	var position int
	if !c.readProperty(trigger, "playlist-pos", &position) || position < 0 {
		return
	}
	var tracks []mpvTrack
	if !c.readProperty(trigger, "track-list", &tracks) {
		return
	}
	item := position + 1
	counts := presentationAt(c.presentations, position).Subtitles
	classified := classifySubtitleTracks(tracks, counts)
	if len(classified) == 0 {
		return
	}
	choice := chooseSubtitle(classified, selectedAudioLanguage(tracks), *c.subtitles)
	if choice.keep {
		logLine(c.log, "command: %s, but the subtitle track of item %d stays mpv's own choice, because %s",
			trigger, item, choice.reason)
		return
	}
	var sid any = "no"
	chosen := "no subtitle track"
	if choice.track != nil {
		sid = choice.track.ID
		chosen = describeChosenTrack(classified, choice.track.ID)
	}
	c.request(fmt.Sprintf("command: %s, so the sidecar chose %s for item %d, because %s",
		trigger, chosen, item, choice.reason), []any{"set_property", "sid", sid})
}

// readProperty reads one property into value, and logs why when the read
// fails.
func (c *commander) readProperty(trigger, property string, value any) bool {
	data, err := c.query(property)
	if err == nil {
		err = json.Unmarshal([]byte(data), value)
	}
	if err != nil {
		logLine(c.log, "command: %s, but the subtitle track stays mpv's own choice, because the read of %s failed: %v",
			trigger, property, err)
		return false
	}
	return true
}

// describeChosenTrack names a track the way a person checks it against
// mpv's track list: its id, its language, its role, and its count.
func describeChosenTrack(tracks []subtitleTrack, id int) string {
	for _, one := range tracks {
		if one.track.ID != id {
			continue
		}
		lang := one.track.Lang
		if lang == "" {
			lang = "no language"
		}
		events := "no count"
		if one.events != nil {
			events = fmt.Sprintf("%d events", *one.events)
		}
		return fmt.Sprintf("subtitle track %d (%s, %s, %s)", id, lang, one.role, events)
	}
	return fmt.Sprintf("subtitle track %d", id)
}
