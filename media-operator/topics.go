package main

// The MQTT topics of the media bus. Every component of media-operator
// reads and writes the topics that these functions build, so this file
// is also the reference for another client of the bus, such as a Home
// Assistant integration.
//
// Every topic starts with one base, liken/media by default. Home
// Assistant's discovery topics start with homeassistant/, so the two
// trees do not overlap.

import "strings"

// splitTopicLines splits a topic list that the operator joins with
// newlines into a pod's environment variable. It keeps blank entries,
// because the events and focus lists are paired by position.
func splitTopicLines(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

const defaultTopicBase = "liken/media"

// The values of an availability payload. A pod publishes online when it
// connects, and registers offline as its MQTT Last Will on the same
// topic. When the pod dies without disconnecting, the broker publishes
// offline, so readers do not treat the pod's retained status or codes
// as current.
const (
	availabilityOnline  = "online"
	availabilityOffline = "offline"
)

// playAvailability builds a playback pod's availability payload: the
// word, a space, and the pod's UID, or the word alone when the UID is
// empty. The broker can publish an old pod's Last Will after the Play's
// new pod has come online, and the UID lets the operator ignore it.
func playAvailability(word, pod string) []byte {
	if pod == "" {
		return []byte(word)
	}
	return []byte(word + " " + pod)
}

func parsePlayAvailability(payload []byte) (online bool, pod string) {
	word, pod, _ := strings.Cut(string(payload), " ")
	return word == availabilityOnline, pod
}

// The last segment of a plays topic.
const (
	playStatusKind       = "status"
	playAvailabilityKind = "availability"
)

// The last segment of a remotes topic.
const (
	remoteAvailabilityKind = "availability"
	remoteCodesKind        = "codes"
	remoteKeysKind         = "keys"
	remoteEventsKind       = "events"
)

// The last segment of a players topic.
const (
	playerPanelKind  = "panel"
	playerVolumeKind = "volume"
	playerPowerKind  = "power"
)

// remoteEventsTopic is where a Remote's reader pod publishes each key
// event, such as {"key": "KEY_UP", "value": 1}, not retained. The pod
// translates the controller's codes with the Remote's key table, so
// every subscriber reads the same key names.
func remoteEventsTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remoteEventsKind
}

// remoteEventsFilter matches the events topic of every Remote. The
// operator reads presses there to change a Player's spec.power and to
// ask the Player's Receiver to select the Player's input.
func remoteEventsFilter(base string) string {
	return base + "/remotes/+/+/" + remoteEventsKind
}

func parseRemoteEventsTopic(base, topic string) (namespace, name string, ok bool) {
	return parseRemoteTopic(base, topic, remoteEventsKind)
}

// remoteFocusTopic holds a Remote's focus mark: the name of the Player,
// in the Remote's namespace, that the controller's presses go to. The
// operator publishes it retained, and every reader of the Remote's
// events checks it before it acts on a press. Because the mark is
// retained, presses reach the right Player while the operator is down.
func remoteFocusTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/focus"
}

// remoteFocusCycleTopic receives a request to move the focus mark to
// the next Player, not retained. The pod of the Player that has focus
// publishes it when the source key is pressed: the playback pod's
// command sidecar while a Play runs, and the idle screen client
// otherwise.
func remoteFocusCycleTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/focus/cycle"
}

// remoteFocusFilter matches the focus topic of every Remote. The
// operator subscribes to its own marks so that after a restart the
// broker returns the marks it published earlier.
func remoteFocusFilter(base string) string {
	return base + "/remotes/+/+/focus"
}

func remoteFocusCycleFilter(base string) string {
	return base + "/remotes/+/+/focus/cycle"
}

// remoteAvailabilityTopic is where a Remote's reader pod publishes
// online or offline, with no pod UID.
func remoteAvailabilityTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remoteAvailabilityKind
}

// remoteCodesTopic is where a Remote's reader pod publishes, retained,
// the key and axis codes that the controller declares, as
// {"keys": [...], "axes": [...]}. It publishes them each time it opens
// the controller's input devices, and clears the topic with an empty
// payload when the devices close.
func remoteCodesTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remoteCodesKind
}

func remoteCodesFilter(base string) string {
	return base + "/remotes/+/+/" + remoteCodesKind
}

func parseRemoteCodesTopic(base, topic string) (namespace, name string, ok bool) {
	return parseRemoteTopic(base, topic, remoteCodesKind)
}

func remoteAvailabilityFilter(base string) string {
	return base + "/remotes/+/+/" + remoteAvailabilityKind
}

func parseRemoteAvailabilityTopic(base, topic string) (namespace, name string, ok bool) {
	return parseRemoteTopic(base, topic, remoteAvailabilityKind)
}

// parseRemoteTopic returns the namespace and name in a remotes topic
// whose last segment is kind. It requires exactly three segments after
// remotes/, so it never matches a focus/cycle topic.
func parseRemoteTopic(base, topic, kind string) (namespace, name string, ok bool) {
	prefix := base + "/remotes/"
	if !strings.HasPrefix(topic, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	if len(parts) != 3 || parts[2] != kind {
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func parseRemoteFocusTopic(base, topic string) (namespace, name string, ok bool) {
	prefix := base + "/remotes/"
	if !strings.HasPrefix(topic, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	if len(parts) != 3 || parts[2] != "focus" {
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func parseRemoteFocusCycleTopic(base, topic string) (namespace, name string, ok bool) {
	prefix := base + "/remotes/"
	if !strings.HasPrefix(topic, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	if len(parts) != 4 || parts[2] != "focus" || parts[3] != "cycle" {
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// playCommandsTopic receives the commands that control one Play, such
// as play-pause and seek, not retained. Any client on the bus can
// publish them, and the operator publishes a stop when a Player powers
// off. input.go lists the commands. Volume belongs to the Player, so
// volume commands go to playerVolumeCommandsTopic.
func playCommandsTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/commands"
}

// playerCommandsTopic receives commands for a Player, not retained.
// The playback pod's command sidecar publishes the viewer's answer to
// the up-next offer here, and the program that created the Play reads
// it and creates the next Play. A request to go home is published here
// too. The idle screen client handles it as a press of the home key,
// and the operator asks the Player's Receiver to show the Player. The
// topic is under players/ and not plays/ because it outlives each Play.
func playerCommandsTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/" + playerCommandsKind
}

const playerCommandsKind = "commands"

// playerStatusTopic is where the operator publishes, retained, the
// Player's state for the screens: its display name, what it is doing,
// the Play it runs, and the link and charge of each of its devices. An
// idle screen client that starts reads the current state from the
// broker.
func playerStatusTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/status"
}

// playerPanelTopic is where a Player's idle screen client publishes its
// panel state, retained. The client has no Kubernetes API credentials,
// so the operator copies the state into the Player's status.
func playerPanelTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/" + playerPanelKind
}

// playerPowerTopic is where the operator publishes wake and sleep
// requests for a Player's screen, not retained, for the idle screen
// client. It publishes one when spec.power changes (playerpower.go) and
// when the television asks for the Player over HDMI-CEC (screenask.go).
func playerPowerTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/" + playerPowerKind
}

func playerPanelFilter(base string) string {
	return base + "/players/+/+/" + playerPanelKind
}

func parsePlayerPanelTopic(base, topic string) (namespace, name string, ok bool) {
	return parsePlayerTopic(base, topic, playerPanelKind)
}

// parsePlayerTopic returns the namespace and name in a players topic
// whose last segment is kind. It requires exactly three segments after
// players/, so it never matches a volume/commands topic.
func parsePlayerTopic(base, topic, kind string) (namespace, name string, ok bool) {
	prefix := base + "/players/"
	if !strings.HasPrefix(topic, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	if len(parts) != 3 || parts[2] != kind {
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// playerVolumeTopic is where the operator publishes the Player's
// volume, retained, as {"level": 0.63, "muted": false}. It publishes
// the target level as key presses move it, and the level that the
// device reports while no target is pending (volumeengine.go). The
// screens draw the volume indicator from each message.
func playerVolumeTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/" + playerVolumeKind
}

// playerVolumeCommandsTopic receives volume commands from clients that
// are not a Remote, not retained: {"step": "up"}, {"step": "down"},
// {"mute": "toggle"}, {"mute": true}, and {"mute": false}. The operator
// handles each one as a press of a volume key.
func playerVolumeCommandsTopic(base, namespace, name string) string {
	return playerVolumeTopic(base, namespace, name) + "/commands"
}

// playerVolumeFilter matches the volume topic of every Player. The
// operator subscribes to its own levels so that after a restart it does
// not publish a level that the broker already retains.
func playerVolumeFilter(base string) string {
	return base + "/players/+/+/" + playerVolumeKind
}

func playerVolumeCommandsFilter(base string) string {
	return playerVolumeFilter(base) + "/commands"
}

func parsePlayerVolumeTopic(base, topic string) (namespace, name string, ok bool) {
	return parsePlayerTopic(base, topic, playerVolumeKind)
}

func parsePlayerVolumeCommandsTopic(base, topic string) (namespace, name string, ok bool) {
	if !strings.HasSuffix(topic, "/commands") {
		return "", "", false
	}
	return parsePlayerVolumeTopic(base, strings.TrimSuffix(topic, "/commands"))
}

// playerCommandsFilter matches the commands topic of every Player. The
// operator acts on each request to go home that does not name a Remote
// (ensure.go), because it already handled the Remote's press.
func playerCommandsFilter(base string) string {
	return base + "/players/+/+/" + playerCommandsKind
}

func parsePlayerCommandsTopic(base, topic string) (namespace, name string, ok bool) {
	return parsePlayerTopic(base, topic, playerCommandsKind)
}

func busFilters(base string) []string {
	return []string{
		playStatusFilter(base),
		playAvailabilityFilter(base),
		remoteFocusFilter(base),
		remoteFocusCycleFilter(base),
		remoteEventsFilter(base),
		remoteAvailabilityFilter(base),
		remoteCodesFilter(base),
		playerPanelFilter(base),
		playerVolumeFilter(base),
		playerVolumeCommandsFilter(base),
		playerCommandsFilter(base),
	}
}

// remoteKeysTopic is where the operator publishes, retained, a Remote's
// key table: the base mapping merged with the Remote's Keymap. It is
// per Remote because a Remote with no Keymap still needs the base
// mapping. The reader pod receives the current table when it connects,
// and receives each Keymap edit without a restart.
func remoteKeysTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remoteKeysKind
}

// playStatusTopic is where the playback pod's command sidecar publishes,
// retained, the Play's paused flag, item, position, and duration. A
// restarted operator reads the current position from the broker.
func playStatusTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/" + playStatusKind
}

// playAvailabilityTopic is where the command sidecar publishes its
// availability payload (playAvailability).
func playAvailabilityTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/" + playAvailabilityKind
}

func playStatusFilter(base string) string {
	return base + "/plays/+/+/" + playStatusKind
}

func playAvailabilityFilter(base string) string {
	return base + "/plays/+/+/" + playAvailabilityKind
}

// parsePlayTopic returns the namespace, name, and kind of a plays
// status or availability topic.
func parsePlayTopic(base, topic string) (namespace, name, kind string, ok bool) {
	prefix := base + "/plays/"
	if !strings.HasPrefix(topic, prefix) {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	if len(parts) != 3 {
		return "", "", "", false
	}
	namespace, name, kind = parts[0], parts[1], parts[2]
	if namespace == "" || name == "" {
		return "", "", "", false
	}
	if kind != playStatusKind && kind != playAvailabilityKind {
		return "", "", "", false
	}
	return namespace, name, kind, true
}
