package main

// The input contract across the operator, the standing remote pod,
// and the consumers. The operator compiles each Remote's table over
// the base and publishes it retained on that Remote's keys topic. The
// standing remote pod folds every raw evdev event through the table
// and publishes kernel key names on the events topic. Each consumer
// holds its own table from key names to what it does there. The split
// keeps each vocabulary on its own side: only the operator reads the
// Keymap resource, and only a consumer knows what a key means to it.

import (
	"encoding/json"
	"io"
)

// The action words. They are no longer an API vocabulary: no Keymap
// and no controller payload carries one. They are the playback pod's
// internal step between a key name and mpv's own words, and the two
// display verbs, re-present and sleep, that travel on a Player's
// commands topic. cycle-focus never reaches a player program.
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

	// The navigation words are what the playback pod sends the display
	// script. The key table in keybindings.go is what reaches them.
	actionUp     = "up"
	actionDown   = "down"
	actionLeft   = "left"
	actionRight  = "right"
	actionSelect = "select"
	actionBack   = "back"
)

// actionRePresent is display plumbing, not part of the media vocabulary
// above. The operator publishes it to a Player's commands topic when a
// Play ends. The idle command pod reads it and states the present
// moment, and the idle client maps a fresh surface, so a seatless kiosk
// shell shows the clock again. A controller never sends it, so commandFor
// holds no case for it and it reaches no player program.
const actionRePresent = "re-present"

// actionSleep is display plumbing beside re-present. A delegate's
// client publishes it on the Player's commands topic when back has no
// level left to climb, and the idle command pod brings the shade down.
// A controller never sends it, and the stock client never needs it,
// because the command pod sleeps that client on back directly.
const actionSleep = "sleep"

// A compiledBinding is one row of a Remote's key table: an evdev
// type, code, and value on the left, the kernel key name the pod
// publishes on the right. The names are the API and the numbers are
// the wire, because the pod matches what the kernel gives it. The
// operator compiles the durations to milliseconds so the pod parses
// nothing.
type compiledBinding struct {
	EventType uint16 `json:"type"`
	Code      uint16 `json:"code"`
	Value     int32  `json:"value"`
	Key       string `json:"key"`

	// RepeatDelay and RepeatInterval are milliseconds. An interval
	// above zero makes the standing pod synthesise the value 2 stream
	// while the control is held. A keyboard needs no row, because the
	// kernel autorepeats for it.
	RepeatDelay    int `json:"repeatDelay,omitempty"`
	RepeatInterval int `json:"repeatInterval,omitempty"`
}

// keyNone is the right side that drops a control. The base passes
// every KEY_* code, so silencing one is a row and not an omission.
const keyNone = "none"

// keyEvent is what a Remote's events topic carries: the kernel's name
// for the control and the kernel's value, 0 release, 1 press, 2
// repeat. A consumer holds no table of numbers.
type keyEvent struct {
	Key   string `json:"key"`
	Value int32  `json:"value"`
}

// mediaCommand is the JSON that travels on a commands topic. On a
// Play's topic it is the surface any program publishes to, a phone or
// a Home Assistant integration alike, and the playback pod's own step
// from a key name to mpv's words. On a Player's topic it is the
// operator's re-present and a delegate client's sleep. No controller
// payload carries one.
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

// keyCode is one row of the kernel's EV_KEY table, which keycodes.go
// holds whole, generated from input-event-codes.h. The names are the
// API and the numbers are the wire: a Keymap says BTN_SOUTH because
// every Linux controller driver reports the south face button under
// that code, whatever the glyph on the plastic.
type keyCode struct {
	Name string
	Code uint16
}

// buttonCodes are the evdev key names a Keymap's buttons may use,
// which is the whole EV_KEY name space, so a gamepad's face buttons
// and a media remote's transport keys are alike bindable.
var buttonCodes = codesByName(keyCodes)

// keyCodeNames names one code back, for a report of what a controller
// declares. Where the kernel gives one code several names, the first
// wins.
var keyCodeNames = namesByCode(keyCodes)

// axisCodes are the hat axes a Keymap's axes may name. The analog
// sticks are deliberately absent: a resting thumb reports at 250Hz,
// and no key name carries an analog value.
var axisCodes = map[string]uint16{
	"ABS_HAT0X": 0x10,
	"ABS_HAT0Y": 0x11,
}

// axisCodeNames names one hat axis back, the way keyCodeNames names a
// key code back.
var axisCodeNames = namesByCode([]keyCode{
	{Name: "ABS_HAT0X", Code: 0x10},
	{Name: "ABS_HAT0Y", Code: 0x11},
})

// codesByName builds the name-to-code lookup a compile reads. Every
// alias resolves to the code it names.
func codesByName(table []keyCode) map[string]uint16 {
	codes := make(map[string]uint16, len(table))
	for _, each := range table {
		codes[each.Name] = each.Code
	}
	return codes
}

// namesByCode builds the code-to-name lookup a report reads. The first
// name wins, because the table is in the header's order and an alias
// follows the name it aliases.
func namesByCode(table []keyCode) map[uint16]string {
	names := make(map[uint16]string, len(table))
	for _, each := range table {
		if _, named := names[each.Code]; named {
			continue
		}
		names[each.Code] = each.Name
	}
	return names
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
// display, carrying the ready blob.
//
// The three art kinds: the film's logo, the scrub tile, and the playing
// album's cover.
const (
	artRequestMessage = "liken-art-request"
	artReplyMessage   = "liken-art"
	artKindLogo       = "logo"
	artKindTrickplay  = "trickplay"
	artKindAlbum      = "album"
)

// exitMessage is the script-message the display broadcasts when a person
// presses back at the bare video. The command sidecar answers it: it
// publishes the ending to the bus and then quits mpv. The display does not
// quit mpv itself, because the ending must reach the bus before mpv starts
// to tear down the film's surface.
const exitMessage = "liken-exit"

// presentationRequestMessage is the script-message the display broadcasts
// once, when it loads. The sidecar sends each item's block the moment the
// playlist reaches it, and a block sent before the script registered
// reaches nobody. The display carries no block of its own until one
// arrives, so it asks for the current one as soon as it can answer, and
// the sidecar replays it.
const presentationRequestMessage = "liken-presentation-request"

// isExitMessage reads a client-message as the display's exit press. The
// first argument names the request, the same shape an art request takes,
// so another script's broadcast is not an ending.
func isExitMessage(args []string) bool {
	return len(args) > 0 && args[0] == exitMessage
}

// isPresentationRequest reads a client-message as the display asking for the
// current item's block. It takes no arguments, so the name is the whole of it.
func isPresentationRequest(args []string) bool {
	return len(args) > 0 && args[0] == presentationRequestMessage
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

// commandFor is where the action vocabulary becomes the words mpv
// takes on a press. Seek, chapter, and pause carry no-osd, because
// the liken display draws their feedback and mpv's own overlay would
// draw a second time over it. The rest carry osd-auto, so mpv shows
// a short line, such as the track name, that the display does not
// yet draw. Volume and mute are absent on purpose: a press of either
// publishes the unit's next state on the volume topic, and the
// subscription applies it, so neither action becomes a command a
// sidecar sends on the press. An action this build has no case for
// sends nothing, so a command from a newer program has no effect
// rather than a crash.
func commandFor(command mediaCommand) []any {
	switch command.Action {
	case actionPause:
		return []any{"no-osd", "cycle", "pause"}
	case actionSeek:
		return []any{"no-osd", "seek", command.Amount}
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
