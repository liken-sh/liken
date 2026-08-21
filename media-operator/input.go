package main

// The input contract between the operator, the standing remote pod,
// and the playback pod's sidecar. The standing remote pod reads a
// controller and publishes each raw evdev event to the Remote's events
// topic. The operator compiles each bound Keymap into the bindings
// below and passes the set to the playback pod's sidecar in one
// environment variable. The sidecar subscribes to each remote's events
// topic, matches events against that remote's bindings, and turns each
// action into an mpv command. The split keeps each vocabulary on its
// own side: only the operator reads the Keymap resource, and only the
// sidecar knows what an action means to mpv.

import (
	"encoding/json"
	"io"
)

// The action vocabulary. These are the words a Keymap's right side
// may use, and they are named for what a person means, not for what
// mpv runs, so a different player program can implement them later.
// Three of them take an amount; the rest are complete alone.
const (
	actionPause     = "pause"
	actionMute      = "mute"
	actionSeek      = "seek"
	actionVolume    = "volume"
	actionChapter   = "chapter"
	actionSubtitles = "subtitles"
	actionAudio     = "audio"
	actionInfo      = "info"
)

// amountActions are the actions that move by an amount: seconds for
// seek, a step for volume and chapter. The sign is the direction, so
// one action serves both bumpers.
var amountActions = map[string]bool{
	actionSeek:    true,
	actionVolume:  true,
	actionChapter: true,
}

// wordActions are the actions that are complete without an amount.
var wordActions = map[string]bool{
	actionPause:     true,
	actionMute:      true,
	actionSubtitles: true,
	actionAudio:     true,
	actionInfo:      true,
}

// A compiledBinding is one row of the table the sidecar matches
// events against: an evdev event type, code, and value on the left,
// an action and its amount on the right. The operator compiles the
// Keymap's names down to numbers so the sidecar never carries a name
// table, and a keymap the operator could not compile never reaches a
// pod.
type compiledBinding struct {
	EventType uint16 `json:"type"`
	Code      uint16 `json:"code"`
	Value     int32  `json:"value"`
	Action    string `json:"action"`
	Amount    int    `json:"amount,omitempty"`

	// RepeatDelay and RepeatInterval are milliseconds. A RepeatInterval
	// above zero makes the binding repeat while the control is held: the
	// bridge fires the action on the press, waits RepeatDelay, then
	// re-fires every RepeatInterval until the release. Both are zero on a
	// binding that fires once. The operator compiles the Keymap's
	// durations to these milliseconds, so the bridge parses nothing.
	RepeatDelay    int `json:"repeatDelay,omitempty"`
	RepeatInterval int `json:"repeatInterval,omitempty"`
}

// remoteBindings is one bound Remote as the playback pod's sidecar
// needs it: the events topic to subscribe to, and the compiled table
// to match its events against. The operator builds one per bound
// Remote and passes the set in remotesVariable, so the map is as
// immutable as the container set around it and the pod reads nothing
// from the API server.
type remoteBindings struct {
	EventsTopic string            `json:"events"`
	Bindings    []compiledBinding `json:"bindings"`
}

// remoteEvent is one controller event on the bus: the evdev type,
// code, and value the standing remote pod read from the node. The
// standing pod publishes it and the playback pod's sidecar decodes it,
// so the keymap stays off the wire and one Remote can feed two players
// that map it differently.
type remoteEvent struct {
	Type  uint16 `json:"type"`
	Code  uint16 `json:"code"`
	Value int32  `json:"value"`
}

// The two evdev event types a keymap can bind. Buttons arrive as
// EV_KEY presses with value 1; the d-pad on a gamepad arrives not as
// buttons but as the EV_ABS hat axes, with -1 and 1 as the presses
// and 0 as the release.
const (
	evKey uint16 = 0x01
	evAbs uint16 = 0x03
)

// buttonCodes are the evdev key names a Keymap's buttons may use,
// with the codes the kernel gives them in input-event-codes.h. The
// names are the API and the numbers are the wire: a Keymap says
// BTN_SOUTH because every Linux controller driver reports the south
// face button under that code, whatever the glyph on the plastic.
var buttonCodes = map[string]uint16{
	"BTN_SOUTH":  0x130,
	"BTN_EAST":   0x131,
	"BTN_C":      0x132,
	"BTN_NORTH":  0x133,
	"BTN_WEST":   0x134,
	"BTN_Z":      0x135,
	"BTN_TL":     0x136,
	"BTN_TR":     0x137,
	"BTN_TL2":    0x138,
	"BTN_TR2":    0x139,
	"BTN_SELECT": 0x13a,
	"BTN_START":  0x13b,
	"BTN_MODE":   0x13c,
	"BTN_THUMBL": 0x13d,
	"BTN_THUMBR": 0x13e,
}

// axisCodes are the hat axes a Keymap's axes may name. The analog
// sticks are deliberately absent: a resting thumb reports at 250Hz,
// and nothing in the action vocabulary wants an analog value.
var axisCodes = map[string]uint16{
	"ABS_HAT0X": 0x10,
	"ABS_HAT0Y": 0x11,
}

// The IPC socket's home. mpv serves its JSON IPC socket on an
// emptyDir the playback pod always carries, rather than in the player
// container's private /tmp, because the bridge sidecar drives the same
// socket and a volume is the only thing two containers share. The
// volume is unconditional, so mpv serves its socket at one path
// whether or not the play binds a remote.
const (
	ipcVolumeName = "ipc"
	ipcMountPath  = "/ipc"
	ipcSocketPath = "/ipc/mpv.sock"
)

// matchBinding compares type, code, and value, all three exactly. The
// exactness is the debounce: a key's autorepeat arrives as value 2 and
// its release as 0, a hat's return to center as 0, and none of them
// equals the 1, -1, or 1 a binding states, so a held button fires
// once.
func matchBinding(bindings []compiledBinding, event inputEvent) (compiledBinding, bool) {
	for _, binding := range bindings {
		if binding.EventType == event.Type && binding.Code == event.Code && binding.Value == event.Value {
			return binding, true
		}
	}
	return compiledBinding{}, false
}

// commandFor is where the action vocabulary becomes mpv's words, and
// the only place in the system that holds both. The osd-auto prefix
// makes mpv show each press on the screen, which is the viewer's proof
// the controller works. An action this build has no case for sends
// nothing, so a newer operator's keymap degrades to fewer buttons
// rather than a crash.
func commandFor(binding compiledBinding) []any {
	switch binding.Action {
	case actionPause:
		return []any{"osd-auto", "cycle", "pause"}
	case actionMute:
		return []any{"osd-auto", "cycle", "mute"}
	case actionSeek:
		return []any{"osd-auto", "seek", binding.Amount}
	case actionVolume:
		return []any{"osd-auto", "add", "volume", binding.Amount}
	case actionChapter:
		return []any{"osd-auto", "add", "chapter", binding.Amount}
	case actionSubtitles:
		return []any{"osd-auto", "cycle", "sub"}
	case actionAudio:
		return []any{"osd-auto", "cycle", "audio"}
	case actionInfo:
		return []any{"expand-properties", "show-text", "${filename}\n${time-pos} / ${duration}", 4000}
	}
	return nil
}

// sendCommand writes one newline-delimited JSON command, the shape
// mpv's IPC socket accepts.
func sendCommand(writer io.Writer, command []any) error {
	return json.NewEncoder(writer).Encode(mpvCommand{Command: command})
}
