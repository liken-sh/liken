package main

// The command sidecar's log lines describe each command and playback
// change in plain words, beside mpv's own command and property names.

import "fmt"

func describeCommand(command mediaCommand) string {
	switch command.Action {
	case actionPause:
		return "pause or resume"
	case actionPlay:
		return "play"
	case actionHold:
		return "pause"
	case actionSeek:
		return "seek " + signed(command.Amount) + " s"
	case actionChapter:
		return "chapter " + signed(command.Amount)
	case actionSubtitles:
		return "next subtitle track"
	case actionAudio:
		return "next audio track"
	}
	return command.Action
}

// describeChange returns an empty string for a change a viewer cannot
// see.
func describeChange(before, after playbackState) string {
	at := after.position
	if at == "" {
		at = "0:00:00"
	}
	switch {
	case after.item != before.item && before.item < 1:
		return fmt.Sprintf("the run started at item %d", after.item)
	case after.item != before.item:
		return fmt.Sprintf("item %d plays, after item %d", after.item, before.item)
	case after.paused != before.paused && after.paused:
		return "paused at " + at
	case after.paused != before.paused:
		return "playing at " + at
	case after.audioLanguage != before.audioLanguage:
		return "audio language " + languageName(after.audioLanguage)
	case after.subtitleLanguage != before.subtitleLanguage:
		return "subtitle language " + languageName(after.subtitleLanguage)
	}
	return ""
}

func languageName(language string) string {
	if language == "" {
		return "not stated"
	}
	return fmt.Sprintf("%q", language)
}

func signed(amount int) string {
	return fmt.Sprintf("%+d", amount)
}
