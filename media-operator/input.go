package main

// The input contract across the operator, the standing remote pod, the
// translator sidecar, and the command sidecar. The standing remote pod
// reads a controller and publishes each raw evdev event to the Remote's
// events topic. The operator compiles each Keymap into the bindings
// below and publishes the table on the retained keymap topic. A
// translator subscribes to both, matches events against the held table,
// and publishes a named mediaCommand on the Play's commands topic. The
// command sidecar subscribes to that topic and turns each named command
// into an mpv command. The split keeps each vocabulary on its own side:
// only the operator reads the Keymap resource, and only the command
// sidecar turns a command into mpv's own words.

import (
	"encoding/json"
	"io"
)

// The action vocabulary a Keymap's right side may use. These are named
// for what a person means, not for what mpv runs, so a different player
// program can implement them later. Most are media commands. The
// navigation actions below drive the on-screen display, and cycle-focus
// switches which unit a shared controller drives; neither reaches a
// player program. Three of the media commands take an amount; the rest
// are complete alone.
const (
	actionPause      = "pause"
	actionMute       = "mute"
	actionSeek       = "seek"
	actionVolume     = "volume"
	actionChapter    = "chapter"
	actionSubtitles  = "subtitles"
	actionAudio      = "audio"
	actionInfo       = "info"
	actionCycleFocus = "cycle-focus"

	// The navigation actions, named for what a person means, so a different
	// player program can implement them later. A Keymap binds buttons to them
	// the way it binds play-pause.
	actionUp     = "up"
	actionDown   = "down"
	actionLeft   = "left"
	actionRight  = "right"
	actionSelect = "select"
	actionBack   = "back"
)

// actionRePresent is display plumbing, not part of the media vocabulary
// above. The operator publishes it to a Player's commands topic when a
// Play ends, and the idle command sidecar recreates the idle mpv's
// surface so a seatless kiosk shell shows the clock again. A controller
// never sends it, so commandFor holds no case for it and it reaches no
// player program.
const actionRePresent = "re-present"

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
	actionPause:      true,
	actionMute:       true,
	actionSubtitles:  true,
	actionAudio:      true,
	actionInfo:       true,
	actionCycleFocus: true,
	actionUp:         true,
	actionDown:       true,
	actionLeft:       true,
	actionRight:      true,
	actionSelect:     true,
	actionBack:       true,
}

// A compiledBinding is one row of the table a translator matches events
// against: an evdev event type, code, and value on the left, an action
// and its amount on the right. The operator compiles the Keymap's names
// down to numbers so a translator never carries a name table, and it
// publishes the whole table as JSON on the retained keymap topic.
type compiledBinding struct {
	EventType uint16 `json:"type"`
	Code      uint16 `json:"code"`
	Value     int32  `json:"value"`
	Action    string `json:"action"`
	Amount    int    `json:"amount,omitempty"`

	// RepeatDelay and RepeatInterval are milliseconds. A RepeatInterval
	// above zero makes the binding repeat while the control is held: the
	// translator publishes the command on the press, waits RepeatDelay,
	// then re-publishes every RepeatInterval until the release. Both are
	// zero on a binding that fires once. The operator compiles the
	// Keymap's durations to these milliseconds, so the translator parses
	// nothing.
	RepeatDelay    int `json:"repeatDelay,omitempty"`
	RepeatInterval int `json:"repeatInterval,omitempty"`
}

// remoteEvent is one controller event on the bus: the evdev type, code,
// and value the standing remote pod read from the node. The standing pod
// publishes it and a translator decodes it, so the keymap stays off the
// wire and one Remote can feed two players that map it differently.
type remoteEvent struct {
	Type  uint16 `json:"type"`
	Code  uint16 `json:"code"`
	Value int32  `json:"value"`
}

// mediaCommand is the JSON that travels on a Play's commands topic, and
// the generic surface any program publishes to. Action names a word from
// the vocabulary above, and Amount carries the step for the three
// actions that move and is omitted for the rest. The command sidecar
// turns it into an mpv command. A translator builds one from a matched
// binding, but a phone that publishes {"action":"pause"} reaches mpv the
// same way.
type mediaCommand struct {
	Action string `json:"action"`
	Amount int    `json:"amount,omitempty"`
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

// The IPC socket's home. mpv serves its JSON IPC socket on an emptyDir
// the playback pod always carries, rather than in the player container's
// private /tmp, because the command sidecar drives the same socket and a
// volume is the only thing two containers share. The volume is
// unconditional, so mpv serves its socket at one path whether or not the
// play binds a remote.
const (
	ipcVolumeName = "ipc"
	ipcMountPath  = "/ipc"
	ipcSocketPath = "/ipc/mpv.sock"
)

// The decoded-art volume the command sidecar and mpv share. The bridge writes
// each logo as raw bgra here, and mpv reads it back by path through
// overlay-add. It is disk-backed, so the art never counts against the pod's
// memory, and its sizeLimit caps how much the bridge can write.
const (
	artVolumeName = "art"
	artMountPath  = "/art"
	artSizeLimit  = "16Mi"
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

// The mpv client name the display script registers under. It is the target of
// every script-message-to that carries a navigation action to the display.
const displayClientName = "display"

// The script-message name the sidecar and the display agree on for a
// presentation block. It sits beside the six navigation actions the
// display already answers.
const presentationMessage = "presentation"

// displaySummonMessage is the script-message the sidecar sends to summon the
// display after a seek or a chapter jump, so the scrubber shows the new
// position. It sits beside the navigation actions the display already answers.
const displaySummonMessage = "summon"

// The two script-message names the display and the bridge agree on for art.
// The display broadcasts artRequestMessage to ask for a decoded blob at a
// pixel size. The bridge answers with artReplyMessage, addressed to the
// display, carrying the ready blob. artKindLogo is the one art kind today.
const (
	artRequestMessage = "liken-art-request"
	artReplyMessage   = "liken-art"
	artKindLogo       = "logo"
	artKindTrickplay  = "trickplay"
)

// exitMessage is the script-message the display broadcasts when a person
// presses back at the bare video. The command sidecar answers it: it
// publishes the ending to the bus and then quits mpv. The display does not
// quit mpv itself, because the ending must reach the bus before mpv starts
// to tear down the film's surface.
const exitMessage = "liken-exit"

// isExitMessage reads a client-message as the display's exit press. The
// first argument names the request, the same shape an art request takes,
// so another script's broadcast is not an ending.
func isExitMessage(args []string) bool {
	return len(args) > 0 && args[0] == exitMessage
}

// exitCommand is mpv's own word for the ending. The exit code is zero, so
// the pod ends Completed, the outcome a film that ran to its end gives,
// and not Error.
func exitCommand() []any {
	return []any{"quit", "0"}
}

// The interval one trickplay tile covers, used when a Play sets no
// trickplayInterval. Jellyfin generates the sheets at this spacing by
// default, and the library keeps that default.
const defaultTrickplayInterval = "10s"

// An item with no presentation, and an index the sidecar holds no block
// for, forward this empty object. The display reads it as no declared
// fields and falls back to what mpv reads from the file.
const emptyPresentation = "{}"

// presentationCommand hands the display one item's block. The block is
// one string argument, so the display reads it as text and needs no
// decode.
func presentationCommand(block json.RawMessage) []any {
	return []any{"script-message-to", displayClientName, presentationMessage, string(block)}
}

// commandFor is where the action vocabulary becomes mpv's words, and the
// only place in the system that holds both. Seek, chapter, and pause carry
// no-osd, because the liken display draws their feedback and mpv's own
// overlay would draw a second time over it. The rest carry osd-auto, so mpv
// shows a short line, such as the volume or the track name, that the display
// does not yet draw. An action this build has no case for sends nothing, so a
// command from a newer program has no effect rather than a crash.
func commandFor(command mediaCommand) []any {
	switch command.Action {
	case actionPause:
		return []any{"no-osd", "cycle", "pause"}
	case actionMute:
		return []any{"osd-auto", "cycle", "mute"}
	case actionSeek:
		return []any{"no-osd", "seek", command.Amount}
	case actionVolume:
		return []any{"osd-auto", "add", "volume", command.Amount}
	case actionChapter:
		return []any{"no-osd", "add", "chapter", command.Amount}
	case actionSubtitles:
		return []any{"osd-auto", "cycle", "sub"}
	case actionAudio:
		return []any{"osd-auto", "cycle", "audio"}
	case actionInfo:
		return []any{"expand-properties", "show-text", "${filename}\n${time-pos} / ${duration}", 4000}
	case actionUp, actionDown, actionLeft, actionRight, actionSelect, actionBack:
		// A navigation action reaches the display script over script-message-to,
		// and the display draws its own feedback, so it carries no osd-auto prefix
		// and becomes no native mpv command.
		return []any{"script-message-to", displayClientName, command.Action}
	}
	return nil
}

// feedbackFor returns the follow-up the sidecar sends after an mpv command,
// or nil when none is needed. A seek and a chapter jump move the playhead with
// no on-screen mark of their own, so the sidecar summons the display and its
// scrubber shows the new position. mpv's pause observer summons the display on
// its own, so pause needs no follow-up here.
func feedbackFor(command mediaCommand) []any {
	switch command.Action {
	case actionSeek, actionChapter:
		return []any{"script-message-to", displayClientName, displaySummonMessage}
	}
	return nil
}

// sendCommand writes one newline-delimited JSON command, the shape
// mpv's IPC socket accepts.
func sendCommand(writer io.Writer, command []any) error {
	return json.NewEncoder(writer).Encode(mpvCommand{Command: command})
}
