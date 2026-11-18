package main

// This file maps the kernel key names that controllers publish to the
// commands that the playback pod runs during a film.
//
// The volume and power keys have no row. The operator reads them from
// each controller's events topic, so a press does the same thing during
// a film and between films. volumeengine.go handles the volume keys, and
// powerkeys.go writes the Player's spec.power for the power keys.

// No Keymap changes these steps.
const (
	seekSeconds = 10
	chapterStep = 1
)

// A keyBinding is the command a key runs, and whether holding the key
// repeats it. A key that does not repeat acts on the press alone, so a
// held pause toggles once.
type keyBinding struct {
	command mediaCommand
	repeats bool
}

// playbackKeys maps a key name to its binding. Seeks, chapter steps,
// and the arrows repeat, because a person holds those keys.
//
// KEY_PLAYPAUSE, KEY_PLAY, and KEY_PAUSE toggle pause. KEY_PLAYCD plays,
// KEY_PAUSECD pauses, and KEY_STOPCD ends the run, so a second press of
// one of them changes nothing. The kernel's rc-cec keymap reports the
// CEC Play and Play Function as KEY_PLAYCD, Pause as KEY_PAUSECD, and
// Stop as KEY_STOPCD, and HDMI-CEC 1.3a, section CEC 13.13.3, defines
// each of those functions to set a state. rc-cec reports the
// Pause-Play Function as KEY_PLAYPAUSE, which toggles. A
// Keymap row on the Remote of the CEC adapter can map that button to
// KEY_PAUSECD instead.
//
// KEY_CYCLEWINDOWS does not reach mpv. The command sidecar publishes a
// cycle request on the controller's focus topic, and the operator moves
// the focus mark to the next Player. BTN_MODE has no row because it is
// reserved for a home surface that this operator does not own.
//
// KEY_HOMEPAGE and KEY_WWW end the run and publish a home command on
// the Player's commands topic (home.go). ensure.go lists the same names
// as homeKeys.
var playbackKeys = map[string]keyBinding{
	"KEY_PLAYPAUSE": {command: mediaCommand{Action: actionPause}},
	"KEY_PLAY":      {command: mediaCommand{Action: actionPause}},
	"KEY_PAUSE":     {command: mediaCommand{Action: actionPause}},

	"KEY_PLAYCD":  {command: mediaCommand{Action: actionPlay}},
	"KEY_PAUSECD": {command: mediaCommand{Action: actionHold}},
	"KEY_STOPCD":  {command: mediaCommand{Action: actionStop}},

	"KEY_REWIND":      {command: mediaCommand{Action: actionSeek, Amount: -seekSeconds}, repeats: true},
	"KEY_FASTFORWARD": {command: mediaCommand{Action: actionSeek, Amount: seekSeconds}, repeats: true},

	"KEY_PREVIOUSSONG": {command: mediaCommand{Action: actionChapter, Amount: -chapterStep}, repeats: true},
	"KEY_NEXTSONG":     {command: mediaCommand{Action: actionChapter, Amount: chapterStep}, repeats: true},

	"KEY_SUBTITLE": {command: mediaCommand{Action: actionSubtitles}},
	"KEY_AUDIO":    {command: mediaCommand{Action: actionAudio}},
	"KEY_INFO":     {command: mediaCommand{Action: actionInfo}},

	"KEY_UP":    {command: mediaCommand{Action: actionUp}, repeats: true},
	"KEY_DOWN":  {command: mediaCommand{Action: actionDown}, repeats: true},
	"KEY_LEFT":  {command: mediaCommand{Action: actionLeft}, repeats: true},
	"KEY_RIGHT": {command: mediaCommand{Action: actionRight}, repeats: true},

	"KEY_ENTER":   {command: mediaCommand{Action: actionSelect}},
	"KEY_OK":      {command: mediaCommand{Action: actionSelect}},
	"KEY_SELECT":  {command: mediaCommand{Action: actionSelect}},
	"KEY_KPENTER": {command: mediaCommand{Action: actionSelect}},

	"KEY_BACK": {command: mediaCommand{Action: actionBack}},
	"KEY_ESC":  {command: mediaCommand{Action: actionBack}},
	"KEY_EXIT": {command: mediaCommand{Action: actionBack}},

	"KEY_HOMEPAGE": {command: mediaCommand{Action: actionHome}},
	"KEY_WWW":      {command: mediaCommand{Action: actionHome}},

	"KEY_CYCLEWINDOWS": {command: mediaCommand{Action: actionCycleFocus}},
}

// commandForKey returns the command for a key event, and false when the
// event runs none. The kernel reports value 1 for a press, 2 for a
// repeat, and 0 for a release. A repeat runs only for a key that
// repeats, and a release runs nothing.
func commandForKey(event keyEvent) (mediaCommand, bool) {
	binding, bound := playbackKeys[event.Key]
	if !bound {
		return mediaCommand{}, false
	}
	switch event.Value {
	case 1:
		return binding.command, true
	case 2:
		if binding.repeats {
			return binding.command, true
		}
	}
	return mediaCommand{}, false
}
