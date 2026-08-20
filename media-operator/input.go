package main

// The input contract between the operator and the remote sidecar.
// The operator compiles a Keymap into the bindings below and passes
// them to the sidecar in one environment variable, the same way the
// play's identity travels in wire.go. The sidecar matches raw evdev
// events against the bindings and turns each action into an mpv
// command. The split keeps each vocabulary on its own side: only the
// operator reads the Keymap resource, and only the pod knows what an
// action means to mpv.

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
}

// keymapVariable carries the compiled bindings, as a JSON array,
// into the sidecar's environment. An environment variable rather
// than a mounted ConfigMap, because the map must be as immutable as
// the container set around it: a Keymap edited mid-run changes the
// next Play, not this one.
const keymapVariable = "MEDIA_KEYMAP"

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
// emptyDir the playback pod always carries, rather than in the
// player container's private /tmp, because the remote sidecar drives
// the same socket and a volume is the only thing two containers
// share. The volume is unconditional so the supervisor needs no
// second path for a pod with no remotes.
const (
	ipcVolumeName = "ipc"
	ipcMountPath  = "/ipc"
	ipcSocketPath = "/ipc/mpv.sock"
)
