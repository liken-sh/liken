package main

// watched.go states the one rule that says a person watched a work. The
// jellyfin role writes that state to a Jellyfin server, and the media
// browser draws it. media-browser/src/catalog/progress.rs states the same
// rule. The up-next card in media-operator's display/upnext.lua rises on a
// rule of its own, later in the work than this one.

// The two amounts of a work that may remain when it counts as watched: a
// share of its length, and a number of seconds. The rule takes whichever
// leaves less time, so the seconds apply only to a work over 100 minutes.
// Credits run past the story, so a play that stopped in them counts as
// watched.
const (
	watchedPercent = 5
	watchedSeconds = 300
)

// Whether a person watched this work. A work with no duration is never
// watched, because nothing says how long it is.
func watched(position, duration int) bool {
	if duration <= 0 {
		return false
	}
	return position >= duration-min(duration*watchedPercent/100, watchedSeconds)
}
