package main

// How a key press on a controller reaches mpv. The operator compiles
// each Remote's key table, which is the base table with the Remote's
// Keymap applied over it, and publishes the table retained on the
// Remote's keys topic. The Remote's pod looks up each raw evdev event in
// the table and publishes the kernel key name on the Remote's events
// topic. Each consumer, such as the playback pod, has its own table from
// key names to what each key does. So only the operator reads Keymap
// resources, and each consumer decides what a key means to it.

import (
	"encoding/json"
	"io"
)

// These are the values of mediaCommand.Action. The playback pod maps a
// key name to one of them, and commandFor maps it to an mpv command. A
// Keymap and a keyEvent never contain one. The playback pod handles
// cycle-focus itself and sends nothing to mpv for it.
//
// pause toggles. play and hold set the state they name, so a second
// press does not undo the first. HDMI-CEC 1.3a (CEC 13.13.3) defines the
// Play Function and the Pause-Play Function that way, and the kernel's
// rc-cec keymap maps them to KEY_PLAYCD and KEY_PAUSECD.
//
// There is no volume action. The operator reads the volume keys from
// each Remote's events topic and sets the volume on the devices that
// play the Player's sound (volumeengine.go). A program that is not a Remote requests a volume
// on the Player's volume/commands topic.
const (
	actionPause      = "pause"
	actionPlay       = "play"
	actionHold       = "hold"
	actionSeek       = "seek"
	actionChapter    = "chapter"
	actionSubtitles  = "subtitles"
	actionAudio      = "audio"
	actionInfo       = "info"
	actionCycleFocus = "cycle-focus"

	// The playback pod sends these to the display. keybindings.go maps
	// key names to them.
	actionUp     = "up"
	actionDown   = "down"
	actionLeft   = "left"
	actionRight  = "right"
	actionSelect = "select"
	actionBack   = "back"
)

// actionPlayNext is the action that the command sidecar publishes on
// the Player's commands topic when a person accepts the up-next offer.
// The program that created the Play reads it and creates the next Play.
// mpv has no use for it, so commandFor has no case for it.
const actionPlayNext = "play-next"

// A compiledBinding is one row of a Remote's key table. It maps an
// evdev type, code, and value, which are what the kernel reports, to the
// key name that the Remote's pod publishes. The operator converts the
// repeat durations to milliseconds, so the pod does not parse duration
// strings.
type compiledBinding struct {
	EventType uint16 `json:"type"`
	Code      uint16 `json:"code"`
	Value     int32  `json:"value"`
	Key       string `json:"key"`

	// When RepeatInterval is above zero, the Remote's pod generates
	// repeat events (value 2) while the control is held down. A keyboard
	// needs no repeat, because the kernel autorepeats its keys.
	RepeatDelay    int `json:"repeatDelay,omitempty"`
	RepeatInterval int `json:"repeatInterval,omitempty"`
}

// keyNone is the key name that makes the Remote's pod publish nothing
// for a control. The base table publishes every KEY_* code under its
// own name, so a Keymap silences a control with a row for this key.
const keyNone = "none"

// keyEvent is the payload of a Remote's events topic. Value is the
// evdev value: 0 for a release, 1 for a press, and 2 for a repeat.
type keyEvent struct {
	Key   string `json:"key"`
	Value int32  `json:"value"`
}

// mediaCommand is the JSON payload of a commands topic. Any program,
// such as a phone app or a Home Assistant integration, can publish one
// on a Play's commands topic, and the playback pod also uses it between
// a key name and an mpv command. On a Player's commands topic, the
// playback pod publishes one for what follows the run, such as home.
//
// Remote is set only on a home command that the playback pod publishes
// on the Player's commands topic, to the namespace/name of the Remote
// whose press caused it. The operator has already acted on that press
// from the Remote's events topic, so it skips a home command with Remote
// set (showForHomeAsk). A command from any other program has no Remote.
type mediaCommand struct {
	Action string `json:"action"`
	Amount int    `json:"amount,omitempty"`
	Remote string `json:"remote,omitempty"`
}

// The evdev event types that a Keymap can bind. A button reports EV_KEY
// events. A gamepad's d-pad can report EV_ABS events on a hat axis,
// where -1 and 1 are the two directions and 0 is the release.
const (
	evKey uint16 = 0x01
	evAbs uint16 = 0x03
)

// keyCode is one row of the kernel's EV_KEY table. keycodes.go has the
// whole table, generated from input-event-codes.h. A Keymap names a
// button by its kernel name, such as BTN_SOUTH, which a Linux gamepad
// driver reports for the bottom face button whatever its label.
type keyCode struct {
	Name string
	Code uint16
}

var buttonCodes = codesByName(keyCodes)

// keyCodeNames maps a code back to its name, for reports of what a
// controller has.
var keyCodeNames = namesByCode(keyCodes)

// axisCodes are the hat axes that a Keymap's axes can name. The analog
// sticks are left out, because a key name cannot express an analog
// value.
var axisCodes = map[string]uint16{
	"ABS_HAT0X": 0x10,
	"ABS_HAT0Y": 0x11,
}

var axisCodeNames = namesByCode([]keyCode{
	{Name: "ABS_HAT0X", Code: 0x10},
	{Name: "ABS_HAT0Y", Code: 0x11},
})

func codesByName(table []keyCode) map[string]uint16 {
	codes := make(map[string]uint16, len(table))
	for _, each := range table {
		codes[each.Name] = each.Code
	}
	return codes
}

// namesByCode keeps the first name for each code. The table follows
// the order of input-event-codes.h, where an alias such as BTN_A comes
// after the name it aliases, BTN_SOUTH.
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

// mpv's JSON IPC socket is on an emptyDir volume, because the command
// sidecar connects to the same socket from another container. Every
// playback pod has the volume, whether or not the Play has a Remote, so
// the socket is always at the same path.
const (
	ipcVolumeName = "ipc"
	ipcMountPath  = "/ipc"
	ipcSocketPath = "/ipc/mpv.sock"
)

// presentationMessage is the script-message that sends the display one
// item's presentation block.
const presentationMessage = "presentation"

const displaySummonMessage = "summon"

// exitMessage is the script-message that the display broadcasts when
// back is pressed and the display has nothing left to close, or while
// the next Play starts after the up-next offer was accepted. The command
// sidecar publishes the ending to the bus and then quits mpv. The
// display does not quit mpv itself, because the ending must reach the
// bus before mpv closes the film's surface.
const exitMessage = "liken-exit"

// nextRequestMessage is the script-message that the display broadcasts
// when a person accepts the up-next offer. The sidecar then publishes
// actionPlayNext on the Player's commands topic.
const nextRequestMessage = "liken-next"

// nextMessage is the script-message that sends the display the Play's
// next block, which describes the up-next offer. The sidecar sends it
// after the first item's presentation block, and again when the display
// sends presentationRequestMessage. A Play with no next block sends
// none.
const nextMessage = "next"

// presentationRequestMessage is the script-message that the display
// broadcasts once, when it loads. The sidecar sends each item's block
// when mpv reaches that item, and the display does not receive a block
// sent before it connected to mpv. So the display requests the current block when it
// is ready.
const presentationRequestMessage = "liken-presentation-request"

func isExitMessage(args []string) bool {
	return len(args) > 0 && args[0] == exitMessage
}

func isNextRequest(args []string) bool {
	return len(args) > 0 && args[0] == nextRequestMessage
}

func isPresentationRequest(args []string) bool {
	return len(args) > 0 && args[0] == presentationRequestMessage
}

// exitCommand quits mpv with exit code 0, so the pod ends Completed, as
// it does when a film plays to its end, and not Error.
func exitCommand() []any {
	return []any{"quit", "0"}
}

// defaultTrickplayInterval is the time that one trickplay tile covers
// when a Play sets no trickplayInterval. It is Jellyfin's default
// interval for the trickplay images it generates.
const defaultTrickplayInterval = "10s"

// emptyPresentation is the block for an item that has none. The display
// then shows the metadata that mpv reads from the file.
const emptyPresentation = "{}"

// presentationCommand and nextCommand send a block as one string
// argument, because a script-message's arguments are strings.
func presentationCommand(block json.RawMessage) []any {
	return []any{"script-message", presentationMessage, string(block)}
}

func nextCommand(block json.RawMessage) []any {
	return []any{"script-message", nextMessage, string(block)}
}

// commandFor returns the mpv command for an action. Seek, chapter, and
// the pause actions use no-osd, because the display draws their feedback
// and mpv's own overlay would draw it a second time. subtitles and audio
// use osd-auto, so mpv shows the track name, which the display does not
// draw. An unknown action, such as one from a newer program, returns
// nil and is ignored.
func commandFor(command mediaCommand) []any {
	switch command.Action {
	case actionPause:
		return []any{"no-osd", "cycle", "pause"}
	case actionPlay:
		return []any{"no-osd", "set", "pause", "no"}
	case actionHold:
		return []any{"no-osd", "set", "pause", "yes"}
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
		// The display handles navigation, so these go to it as
		// script-messages.
		return []any{"script-message", command.Action}
	}
	return nil
}

// feedbackFor returns the command that the sidecar sends after an
// action's mpv command, or nil. After a seek or a chapter jump, it tells
// the display to show the scrubber at the new position. Pause needs
// none, because the display observes mpv's pause property and shows
// itself when the film pauses.
func feedbackFor(command mediaCommand) []any {
	switch command.Action {
	case actionSeek, actionChapter:
		return []any{"script-message", displaySummonMessage}
	}
	return nil
}

// sendCommand writes one command as a line of JSON, which is the format
// of mpv's IPC protocol.
func sendCommand(writer io.Writer, command []any) error {
	return json.NewEncoder(writer).Encode(mpvCommand{Command: command})
}
