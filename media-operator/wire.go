package main

// The playback pod's command sidecar publishes a playReport as JSON to
// the Play's status topic, as a retained message. It publishes at once
// for a pause, a resume, a new item, or a new track language, and at
// most once per reportInterval for a position change. The operator
// copies each report into the Play's status. The topic identifies the
// Play, so the report does not.

// playReport is the state of a run that the sidecar reads from mpv's
// IPC socket.
type playReport struct {
	// Paused is true while mpv is paused. The Play's phase stays
	// Running.
	Paused bool `json:"paused"`

	// Item is the index of the playing URI in the Play's spec, counting
	// from 1.
	Item int `json:"item"`

	// Position and Duration are H:MM:SS. Duration is empty until mpv has
	// read the item's header.
	Position string `json:"position"`
	Duration string `json:"duration"`

	// The languages of the audio and subtitle tracks that mpv selected,
	// from its current-tracks property. Each is empty when no such track
	// plays.
	AudioLanguage    string `json:"audioLanguage,omitempty"`
	SubtitleLanguage string `json:"subtitleLanguage,omitempty"`

	// Ended is true in every report after the run ends: on the exit
	// press, a stop or home command, the end of the playlist, or
	// SIGTERM. A pod takes seconds to terminate, so the operator shows
	// the idle screen when it reads Ended instead of waiting for the
	// pod to go. The Play's phase still comes from the pod.
	Ended bool `json:"ended,omitempty"`

	// Pod is the UID of the playback pod that sent the report. After the
	// operator replaces a pod, the old pod's sidecar can still publish
	// its ending on SIGTERM, and the operator drops a report whose UID
	// is not the current pod's. A report with no UID is accepted.
	Pod string `json:"pod,omitempty"`
}

// The environment variables that the operator sets on the pods it
// creates. The sidecar, the player shim, and the remote pod run from
// this binary and read these constants. The idle client and the display
// are separate programs and declare their own copies of the names.
const (
	// The Play of a playback pod. The sidecar builds its status and
	// availability topics from them.
	playNamespaceVariable = "MEDIA_PLAY_NAMESPACE"
	playNameVariable      = "MEDIA_PLAY_NAME"

	// podUIDVariable is the playback pod's own UID, from metadata.uid.
	// The sidecar copies it into playReport.Pod.
	podUIDVariable = "MEDIA_POD_UID"

	// playerNameVariable is the metadata.name of the Player that a focus
	// message must name before the pod acts on a press. A playback pod
	// gets spec.players[0] of its Play, and an idle pod gets its own
	// Player. The display name in IDLE_PLAYER_NAME can differ, and the
	// operator writes focus messages with metadata.name.
	playerNameVariable = "MEDIA_PLAYER_NAME"

	// playStartVariable is the Play's spec.start, set only when the Play
	// has one. The player shim passes it to mpv as --start.
	playStartVariable = "MEDIA_PLAY_START"

	// playerOptionsVariable is the mpv flags that the operator resolved
	// from the Player's preferences, one per line. The player shim
	// appends each to mpv's arguments.
	playerOptionsVariable = "MEDIA_PLAYER_OPTIONS"

	// playerVerboseVariable, when set, makes the player shim run mpv
	// without --quiet. The operator copies it from its own environment
	// onto every playback pod it creates.
	playerVerboseVariable = "MEDIA_PLAYER_VERBOSE"

	// timeZoneVariable is the standard TZ variable. The clock in the
	// playback pod and in the idle pod looks the zone up in its own
	// image's tz database.
	timeZoneVariable = "TZ"

	// idleWindowGraceVariable is the number of seconds a client waits
	// for its window before it exits non-zero. Without it, a client
	// never exits for a missing window. The operator sets it only on
	// the idle container, because a playback pod can be audio-only.
	idleWindowGraceVariable = "IDLE_WINDOW_GRACE_SECONDS"

	// The name that the idle screen draws in its bottom-left corner, from
	// spec.displayName or else the object name, and the Player's parts,
	// one per line. The operator sets the parts only when the Player
	// lists any.
	idlePlayerNameVariable       = "IDLE_PLAYER_NAME"
	idlePlayerComponentsVariable = "IDLE_PLAYER_COMPONENTS"

	// The seconds before the idle screen fades and before it turns the
	// panel off. Zero turns each off. The operator sets both on every
	// idle client.
	idleFadeAfterSecondsVariable = "IDLE_FADE_AFTER_SECONDS"
	idleOffAfterSecondsVariable  = "IDLE_OFF_AFTER_SECONDS"

	// presentationsVariable is the presentation block of every item in
	// the Play. The sidecar switches to the current item's block as the
	// playlist advances, so no block is sent over the bus during
	// playback.
	presentationsVariable = "MEDIA_PRESENTATIONS"

	// nextVariable is the Play's next block as JSON, with its art
	// rewritten to the path inside the pod. The operator sets it on
	// both containers only when the Play has a next block.
	nextVariable = "MEDIA_NEXT"

	// subtitlePreferencesVariable is the resolved subtitle preferences as
	// JSON, set only when the subtitle mode is on or auto. Without it,
	// the sidecar selects no subtitle track.
	subtitlePreferencesVariable = "MEDIA_SUBTITLE_PREFERENCES"

	// trickplayIntervalVariable is the Play's trickplayInterval, the time
	// that one tile of a trickplay sheet covers. The display needs it to
	// find the tile for a time, and the sheets do not record it.
	trickplayIntervalVariable = "MEDIA_TRICKPLAY_INTERVAL"

	// The broker's address and the prefix of every topic. A pod cannot
	// discover either, so the operator passes its own values down.
	busAddressVariable = "MEDIA_BUS_ADDRESS"
	topicBaseVariable  = "MEDIA_TOPIC_BASE"

	// The Remote of a remote pod. The pod builds its topics from them.
	remoteNamespaceVariable = "MEDIA_REMOTE_NAMESPACE"
	remoteNameVariable      = "MEDIA_REMOTE_NAME"

	// remoteDiscoveryVariable is set to discoveryOn only when the
	// Remote's spec turns discovery on.
	remoteDiscoveryVariable = "MEDIA_REMOTE_DISCOVERY"
	discoveryOn             = "true"

	// The events topic and the focus topic of each remote, as two lists
	// with one topic per line, where line n of one pairs with line n of
	// the other. The sidecar and the idle client build the cycle topic
	// from the focus topic. An idle client's lists follow spec.remotes,
	// because a focus message identifies a remote by its index there. A
	// Player with no remotes gets neither variable.
	remoteEventsTopicsVariable = "MEDIA_REMOTE_EVENTS_TOPICS"
	remoteFocusTopicsVariable  = "MEDIA_REMOTE_FOCUS_TOPICS"

	// The Player's topics. The operator passes each full topic name, so
	// a client does not need the topic base.
	//
	// The playback pod publishes to the commands topic to request the
	// work that follows a run. The operator sets it on the idle client.
	playerCommandsTopicVariable = "MEDIA_PLAYER_COMMANDS_TOPIC"
	// The status topic has the Player's state for the screen. The
	// sidecar and the idle client read it. It is retained, so a client
	// that starts later reads the current state.
	playerStatusTopicVariable = "MEDIA_PLAYER_STATUS_TOPIC"
	// The idle client publishes the panel state it requests to the panel
	// topic. The client has no API credentials, so the operator writes
	// the request to the screen's Display.
	playerPanelTopicVariable = "MEDIA_PLAYER_PANEL_TOPIC"
	// The operator publishes wake and sleep requests to the power topic.
	playerPowerTopicVariable = "MEDIA_PLAYER_POWER_TOPIC"
	// The operator publishes the volume to the volume topic. It sets the
	// variable only for a Player with sinks, and a client without it
	// draws no volume.
	playerVolumeTopicVariable = "MEDIA_PLAYER_VOLUME_TOPIC"

	// mediaVersionVariable is the image tag of a screen client, which the
	// client reports in its `liken_build_info` gauge. The client has no
	// API credentials to read its own image. metricsAddressVariable is
	// in operate.go.
	mediaVersionVariable = "MEDIA_VERSION"
)
