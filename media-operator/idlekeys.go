package main

// The idle command pod's table from key names to what they do while
// nothing plays, beside the playback pod's table in keybindings.go.
// The two tables share the volume keys and the cycle key. They part on
// navigation: this pod forwards the navigation keys to a delegate's
// client rather than to a display script, and back brings the shade
// down only under this operator's own client.

// The key that asks the operator to move the focus mark to the next
// unit. It is the same name during a film and between films.
const keyCycleWindows = "KEY_CYCLEWINDOWS"

// The navigation keys a delegate's client answers: the arrows, the
// select synonyms, and the back synonyms. Back is one of them, so back
// does not sleep the screen under a delegate. Only the client knows
// whether back has anywhere to go.
var idleNavigationKeys = map[string]bool{
	"KEY_UP":      true,
	"KEY_DOWN":    true,
	"KEY_LEFT":    true,
	"KEY_RIGHT":   true,
	"KEY_ENTER":   true,
	"KEY_OK":      true,
	"KEY_SELECT":  true,
	"KEY_KPENTER": true,
	"KEY_BACK":    true,
	"KEY_ESC":     true,
	"KEY_EXIT":    true,
}

// The three keys that bring the shade down under this operator's own
// client. They are the same three the playback pod reads as back, and
// a shell sends whichever one it was built with.
var idleBackKeys = map[string]bool{
	"KEY_BACK": true,
	"KEY_ESC":  true,
	"KEY_EXIT": true,
}

// The level keys. The step is this consumer's default, the same five
// the playback pod steps by, so a level moves the same amount whether
// or not a film plays.
var idleVolumeKeys = map[string]mediaCommand{
	"KEY_VOLUMEUP":   {Action: actionVolume, Amount: volumeStep},
	"KEY_VOLUMEDOWN": {Action: actionVolume, Amount: -volumeStep},
	"KEY_MUTE":       {Action: actionMute},
}

// idleVolumeFor reads one key event as a level press. The two steps
// act on the press and on the repeat, because a person ramps a level
// by holding the key. Mute acts on the press alone.
func idleVolumeFor(event keyEvent) (mediaCommand, bool) {
	command, bound := idleVolumeKeys[event.Key]
	if !bound {
		return mediaCommand{}, false
	}
	if command.Action == actionMute && event.Value != 1 {
		return mediaCommand{}, false
	}
	return command, true
}
