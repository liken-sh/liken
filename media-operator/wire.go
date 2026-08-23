package main

// The report contract between the playback pod's sidecar and the
// operator. The sidecar publishes this JSON to the Play's status
// topic, retained, on every change and every few seconds while the
// position advances. The operator subscribes and folds each report
// into the Play's status. This is the whole of what the playback pod
// says to the control plane, and it carries no API object: the Play a
// report belongs to is named by the topic, not by the body, and the
// operator decides what any report means for the phase.

// playReport is one observation of the run, as the sidecar sees it
// through mpv's IPC socket. The Play's namespace and name are in the
// topic path, so the body carries only the playback numbers.
type playReport struct {
	// Paused is the player holding the current item still. The phase
	// stays Running.
	Paused bool `json:"paused"`

	// Item is the URI now playing, counting from 1 in spec order.
	Item int `json:"item"`

	// Position and Duration are the playhead and the length of the
	// current item, each as H:MM:SS. Duration is empty until the
	// player has read the item's header.
	Position string `json:"position"`
	Duration string `json:"duration"`

	// The language of the audio track and the subtitle track mpv selected, read
	// from current-tracks. Empty when none plays.
	AudioLanguage    string `json:"audioLanguage,omitempty"`
	SubtitleLanguage string `json:"subtitleLanguage,omitempty"`
}

// Environment variable names the operator sets on the pods it creates.
// The playback pod's sidecar and the standing remote pod read them.
// They are constants here so the modes of this binary cannot drift.
const (
	// The playback pod's identity, which its sidecar turns into the
	// status and availability topics it publishes.
	playNamespaceVariable = "MEDIA_PLAY_NAMESPACE"
	playNameVariable      = "MEDIA_PLAY_NAME"

	// playStartVariable carries spec.start when the Play declares one.
	// The player shim turns it into mpv's --start, so the run begins
	// where the spec says instead of at zero.
	playStartVariable = "MEDIA_PLAY_START"

	// The newline-joined mpv preference flags the operator resolved. The player
	// shim splits them and appends each to mpv's argv.
	playerOptionsVariable = "MEDIA_PLAYER_OPTIONS"

	// The command sidecar carries every item's presentation block, baked
	// into the pod when the operator creates it. The sidecar swaps to the
	// current item's block as the playlist advances, so no block travels
	// live while the film plays.
	presentationsVariable = "MEDIA_PRESENTATIONS"

	// Carries a Play's trickplayInterval to the bridge. The tile width, the
	// grid, and the tile height are on the sheets, so the bridge reads them.
	// The interval is not on the sheets, so the Play declares it and the pod
	// passes it here.
	trickplayIntervalVariable = "MEDIA_TRICKPLAY_INTERVAL"

	// The bus every pod connects to, and the base every topic extends.
	// The operator holds both and passes them down, because a pod
	// cannot read the address of the broker in front of it or the base
	// a cluster chose.
	busAddressVariable = "MEDIA_BUS_ADDRESS"
	topicBaseVariable  = "MEDIA_TOPIC_BASE"

	// The standing remote pod's identity, which it turns into the
	// events topic it publishes.
	remoteNamespaceVariable = "MEDIA_REMOTE_NAMESPACE"
	remoteNameVariable      = "MEDIA_REMOTE_NAME"

	// The three topics the operator hands each translator sidecar: the
	// controller's events topic it reads, the retained keymap topic it
	// reads the table from, and the focus topic it gates on. The translator
	// builds the cycle topic from its identity, and reuses
	// remoteNameVariable for its client id.
	remoteEventsVariable = "MEDIA_REMOTE_EVENTS"
	keymapTopicVariable  = "MEDIA_KEYMAP_TOPIC"
	focusTopicVariable   = "MEDIA_FOCUS_TOPIC"
)
