package main

// The command sidecar sets mpv's sid property with the rule of
// subtitletracks.go each time mpv sends file-loaded. mpv sends that
// event after it has loaded the external subtitle files and selected
// the audio track, so the track list and the audio language are
// complete. The sidecar does not act when the current tracks change,
// because a person who selects a subtitle track by hand changes them
// too, and that choice must stay until the next file.
//
// mpv can load the first file before dialMPV connects, and then the
// sidecar never receives that file-loaded. So the sidecar also chooses
// once when it connects. A sidecar that restarts during an item chooses
// again for that item.

import (
	"encoding/json"
	"fmt"
)

// What triggered a choice, in the words of the log line.
const (
	triggerConnected = "the sidecar connected to mpv's socket"
	triggerLoaded    = "mpv loaded a file"
)

// parseSubtitlePreferences returns nil for an empty or malformed value.
// The sidecar then sets no sid, and mpv keeps its own choice.
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

func (c *commander) serveLoads(loads <-chan struct{}) {
	c.chooseSubtitles(triggerConnected)
	for range loads {
		c.chooseSubtitles(triggerLoaded)
	}
}

// chooseSubtitles sets nothing when a read fails, and mpv keeps the
// track that the flags of mpvPreferenceOptions made it choose.
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

// readProperty logs a failed read or decode and returns false.
func (c *commander) readProperty(trigger, property string, value any) bool {
	data, err := c.query(property)
	if err == nil {
		err = json.Unmarshal([]byte(data), value)
	}
	if err != nil {
		logLine(c.log, "command: %s, but the subtitle track stays mpv's own choice, because reading %s from mpv returned: %v",
			trigger, property, err)
		return false
	}
	return true
}

// describeChosenTrack describes a track for the log with the details a
// person compares with mpv's track list.
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
