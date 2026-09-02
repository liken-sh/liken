package main

// The playback pod's table from key names to what they do during a
// film, and the documentation of that pod's controls. This is the
// consumer's half of the two layers: the pod binds the kernel's names
// the way Kodi and mpv bind them, and the amounts here are this
// consumer's defaults, stated in no Keymap. Four keys act on a repeat
// as well as a press, because they are the four a person holds: a
// seek, a chapter step, a volume step, and an arrow.

// The amounts. They are this pod's own defaults. A cluster that wants
// other numbers gets them from a later preferences tier, and no
// per-controller table states them.
const (
	seekSeconds = 10
	volumeStep  = 5
	chapterStep = 1
)

// A keyBinding is one row: the command the key means, and whether a
// held key repeats it. A key that does not repeat acts on the press
// alone, so a held pause toggles once.
type keyBinding struct {
	command mediaCommand
	repeats bool
}

// The table. Each group of names is one command: the four transport
// names a keyboard and a media remote report for play and pause, the
// four names a shell sends for OK, and the three it sends for back.
// KEY_CYCLEWINDOWS asks the operator to move the focus mark and
// reaches no player program. The reserved keys, KEY_HOMEPAGE,
// KEY_WWW, KEY_POWER, and BTN_MODE, are absent on purpose: they belong
// to a home surface this operator does not own.
var playbackKeys = map[string]keyBinding{
	"KEY_PLAYPAUSE": {command: mediaCommand{Action: actionPause}},
	"KEY_PLAY":      {command: mediaCommand{Action: actionPause}},
	"KEY_PAUSE":     {command: mediaCommand{Action: actionPause}},
	"KEY_PLAYCD":    {command: mediaCommand{Action: actionPause}},

	"KEY_REWIND":      {command: mediaCommand{Action: actionSeek, Amount: -seekSeconds}, repeats: true},
	"KEY_FASTFORWARD": {command: mediaCommand{Action: actionSeek, Amount: seekSeconds}, repeats: true},

	"KEY_PREVIOUSSONG": {command: mediaCommand{Action: actionChapter, Amount: -chapterStep}, repeats: true},
	"KEY_NEXTSONG":     {command: mediaCommand{Action: actionChapter, Amount: chapterStep}, repeats: true},

	"KEY_VOLUMEUP":   {command: mediaCommand{Action: actionVolume, Amount: volumeStep}, repeats: true},
	"KEY_VOLUMEDOWN": {command: mediaCommand{Action: actionVolume, Amount: -volumeStep}, repeats: true},
	"KEY_MUTE":       {command: mediaCommand{Action: actionMute}},

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

	"KEY_CYCLEWINDOWS": {command: mediaCommand{Action: actionCycleFocus}},
}

// commandForKey reads one key event as this pod's command. Value 0
// does nothing, because the release of a key this pod acted on has no
// meaning here. Value 2 acts only for the keys that repeat. A key with
// no row, which is most of a keyboard, does nothing.
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
