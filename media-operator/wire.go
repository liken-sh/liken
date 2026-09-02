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

	// Ended says the run is over. The sidecar sets it in each of the three
	// endings, and it stays set in every later report of the same run. The
	// pod takes seconds to terminate, so an ending read from the pod alone
	// would leave a dead film on the screen for those seconds; the operator
	// reads this mark and reveals the idle screen in bus time. The mark says
	// nothing about the Play's phase, which keeps deriving from the pod.
	Ended bool `json:"ended,omitempty"`
}

// Environment variable names the operator sets on the pods it creates.
// The playback pod's sidecar and the standing remote pod read them.
// They are constants here so the modes of this binary cannot drift.
const (
	// The playback pod's identity, which its sidecar turns into the
	// status and availability topics it publishes.
	playNamespaceVariable = "MEDIA_PLAY_NAMESPACE"
	playNameVariable      = "MEDIA_PLAY_NAME"

	// playerNameVariable carries a Player's metadata.name, the value every
	// focus mark holds. In a playback pod it is the Play's own unit,
	// spec.players[0], and the command sidecar gates the mark against it.
	// In an idle pod it is the idle Player itself, and the client gates
	// the same way. It is the object name, never the friendly name
	// IDLE_PLAYER_NAME carries, because the operator writes marks from
	// metadata.name.
	playerNameVariable = "MEDIA_PLAYER_NAME"

	// playStartVariable carries spec.start when the Play declares one.
	// The player shim turns it into mpv's --start, so the run begins
	// where the spec says instead of at zero.
	playStartVariable = "MEDIA_PLAY_START"

	// The newline-joined mpv preference flags the operator resolved. The player
	// shim splits them and appends each to mpv's argv.
	playerOptionsVariable = "MEDIA_PLAYER_OPTIONS"

	// TZ is the standard name, not a MEDIA_ variable. The playback pod
	// and the idle pod both set it, and each clock reads it against its
	// own image's tz database to show the household's wall-clock zone.
	timeZoneVariable = "TZ"

	// The seconds a client waits for its window before it exits
	// non-zero. It arms the watchdog, so a pod that expects no window
	// sets it nowhere and nothing exits for a missing one. The operator
	// sets it on the idle container alone, because the idle client holds
	// a window for its whole life and a playback pod may be audio-only.
	idleWindowGraceVariable = "IDLE_WINDOW_GRACE_SECONDS"

	// The Player's friendly name and its parts, which the idle screen draws
	// in the bottom-left. The operator sets the name always, resolved from
	// spec.displayName or the object name, and sets the parts only when the
	// Player lists any. The parts join with newlines, and the idle client
	// splits them, the same shape the player options travel in.
	idlePlayerNameVariable       = "IDLE_PLAYER_NAME"
	idlePlayerComponentsVariable = "IDLE_PLAYER_COMPONENTS"

	// The fade window in seconds, on the idle client pod. Zero means
	// the screen never fades on its own. The client holds the timer,
	// through the media-screen crate, so the operator settles the
	// policy and the client runs it.
	idleFadeAfterSecondsVariable = "IDLE_FADE_AFTER_SECONDS"

	// The off window in seconds, on the idle client pod. Zero leaves
	// the panel lit. It is set on every client for the reason the fade
	// window is: the operator settles it, and an absent variable is not
	// a policy a client can read.
	idleOffAfterSecondsVariable = "IDLE_OFF_AFTER_SECONDS"

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

	// The discovery mode, set on the standing pod only where the
	// Remote's spec asks for it, so a pod that reads nothing here runs
	// the ordinary selection rule.
	remoteDiscoveryVariable = "MEDIA_REMOTE_DISCOVERY"
	discoveryOn             = "true"

	// The two lists the operator hands the playback pod's command
	// sidecar and the idle client pod, newline-joined and aligned by
	// position: each controller's events topic and the focus topic that
	// carries its mark. Each reader builds the cycle topic from the
	// focus topic. The playback pod lists the Play's remotes by name.
	// The idle client lists them in spec.remotes order, because that
	// position is the index a focus moment carries. A Player with no
	// Remotes carries neither variable.
	remoteEventsTopicsVariable = "MEDIA_REMOTE_EVENTS_TOPICS"
	remoteFocusTopicsVariable  = "MEDIA_REMOTE_FOCUS_TOPICS"

	// The player-commands topic, on the idle client pod. The operator
	// builds it from the Player's identity and passes it whole, the way
	// it hands the playback pod its focus topics, so the client
	// subscribes to one exact topic and parses nothing. It carries the
	// operator's re-present alone.
	playerCommandsTopicVariable = "MEDIA_PLAYER_COMMANDS_TOPIC"

	// The player-status topic the playback pod's command sidecar reads the
	// unit's presentable state from, and the idle client draws it from. The
	// operator builds it the same way and the topic is retained, so a
	// freshly started idle pod reads the current state on subscribe and
	// asks for nothing.
	playerStatusTopicVariable = "MEDIA_PLAYER_STATUS_TOPIC"

	// The topic the idle client states the panel desire on. The
	// operator builds it whole, the way it builds the commands and
	// status topics, so the client parses no topic. The client holds no
	// API credentials, so the operator reads the desire off the bus and
	// overrides the screen's Display.
	playerPanelTopicVariable = "MEDIA_PLAYER_PANEL_TOPIC"

	// The unit's volume topic, on the playback pod's command sidecar and
	// on the idle client. It is the speaker
	// gate as well as the address: the operator sets it only for a Player
	// that states sinks, so a container that reads nothing here
	// subscribes to no level, draws none, and publishes none.
	playerVolumeTopicVariable = "MEDIA_PLAYER_VOLUME_TOPIC"
)
