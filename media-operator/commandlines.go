package main

// The words the command sidecar's lines use for what a person asked
// for and what mpv reports back. They name the operation the way a
// person reads it on the screen, and the exact mpv words go beside them
// in each line.

import "fmt"

// describeCommand names what a command asks for in a person's words,
// for the line it earns.
func describeCommand(command mediaCommand) string {
	switch command.Action {
	case actionPause:
		return "pause or resume"
	case actionSeek:
		return "seek " + signed(command.Amount) + " s"
	case actionChapter:
		return "chapter " + signed(command.Amount)
	case actionVolume:
		return "volume " + signed(command.Amount)
	case actionMute:
		return "mute or unmute"
	case actionSubtitles:
		return "next subtitle track"
	case actionAudio:
		return "next audio track"
	}
	return command.Action
}

// describeChange names what one property change moved, in the words a
// person reads on the screen, or nothing for a change a person does not
// see. The first item is the start of the run, which the line names as
// such.
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

// languageName quotes a track's language, and names a track that states
// none.
func languageName(language string) string {
	if language == "" {
		return "not stated"
	}
	return fmt.Sprintf("%q", language)
}

// signed writes a step with its sign, so a line reads +10 and -10 alike.
func signed(amount int) string {
	return fmt.Sprintf("%+d", amount)
}
