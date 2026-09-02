package main

// The bus topic layout. Every message the operator, the playback
// pod, and the standing remote pod exchange lands on a topic built
// here, so this file is the public contract of the bus and the one
// place a Home Assistant integration reads to map a Play or a Remote
// onto an entity.
//
// Each topic extends a base the operator holds as one string,
// liken/media by default. The data topics stay under this one base,
// and Home Assistant's own discovery topics stay under
// homeassistant/, so neither tree constrains the other. A base that
// carries a cluster's name so several clusters share one broker is a
// later refinement the string already allows.

import "strings"

// splitTopicLines splits one of the newline-joined topic lists the
// operator sets on a pod. Blank lines are kept, because two lists stay
// aligned by position.
func splitTopicLines(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

// defaultTopicBase is the base every topic extends when the operator
// sets none. zigbee2mqtt uses one configurable base the same way.
const defaultTopicBase = "liken/media"

// The two words the availability topic carries. The sidecar names its
// availability topic as the MQTT Last Will with offline as the
// payload, and publishes online once it connects, so a retained
// status a killed pod left behind does not read as a live Play.
const (
	availabilityOnline  = "online"
	availabilityOffline = "offline"
)

// The kind at the end of a plays topic. parsePlayTopic returns one of
// these so the operator folds a status report and an availability
// signal through separate paths.
const (
	playStatusKind       = "status"
	playAvailabilityKind = "availability"
)

// The kinds at the end of the remotes topics the operator reads for a
// controller. They are separate constants from the plays kinds above,
// because the two trees carry different payloads under the same words.
const (
	remoteAvailabilityKind = "availability"
	// The second retained remotes kind, the codes one controller
	// declares.
	remoteCodesKind = "codes"
	// The third retained remotes kind, the compiled key table the
	// operator writes and the standing pod reads.
	remoteKeysKind = "keys"
)

// The last segment of the players topic that carries the panel
// state, beside the commands and status topics of the same tree.
const playerPanelKind = "panel"

// The last segment of the players topic that carries the listening
// level, beside the panel, commands, and status kinds of the same
// tree.
const playerVolumeKind = "volume"

// remoteEventsTopic carries one Remote's key events. The standing
// remote pod publishes {"key": "KEY_UP", "value": 1} to it, not
// retained, because a press is an event and not a state. The pod
// normalised the event, so every consumer reads one vocabulary.
func remoteEventsTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/events"
}

// remoteFocusTopic carries the retained focus mark, the bare name of the
// Player that owns this controller now, in the Remote's own namespace. A
// mark may name an idle Player. The operator writes it, and every reader
// of the controller's presses gates on it. It is retained, so a press
// reaches the owning unit with the operator up or down.
func remoteFocusTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/focus"
}

// remoteFocusCycleTopic carries the cycle request a source press
// publishes. Only the holder of focus publishes it, the playback pod's
// command sidecar during a film and the idle screen client between
// films, and the operator reads it to advance the mark. It is not
// retained, because a cycle is an event and not a state.
func remoteFocusCycleTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/focus/cycle"
}

// remoteFocusFilter is the operator's subscription that reaches every
// controller's focus mark. The operator is the only writer of a mark, so
// this subscription reads its own retained writes back and recovers the
// current marks after a restart.
func remoteFocusFilter(base string) string {
	return base + "/remotes/+/+/focus"
}

// remoteFocusCycleFilter is the operator's subscription that reaches every
// controller's cycle request. Each plus matches one level, so this filter
// covers the five-level cycle topics alone and stays disjoint from the
// four-level focus filter.
func remoteFocusCycleFilter(base string) string {
	return base + "/remotes/+/+/focus/cycle"
}

// remoteAvailabilityTopic carries online or offline for the standing
// remote pod itself, the same two words the plays availability uses. The
// pod names this topic as its MQTT Last Will, so a pod the kubelet killed
// reads offline and the retained codes it left behind do not read as a
// live declaration.
func remoteAvailabilityTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remoteAvailabilityKind
}

// remoteCodesTopic carries the codes one controller declares, as
// {"keys": [...], "axes": [...]}. The standing remote pod publishes it
// retained at every node open, because a declared set is a state and
// not an event, and clears it with an empty payload when the nodes
// vanish.
func remoteCodesTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remoteCodesKind
}

// remoteCodesFilter is the operator's subscription that reaches every
// controller's declared codes.
func remoteCodesFilter(base string) string {
	return base + "/remotes/+/+/" + remoteCodesKind
}

// parseRemoteCodesTopic maps a codes topic back to the controller it
// names.
func parseRemoteCodesTopic(base, topic string) (namespace, name string, ok bool) {
	return parseRemoteTopic(base, topic, remoteCodesKind)
}

// remoteAvailabilityFilter is the operator's subscription that reaches
// every standing remote pod's availability signal.
func remoteAvailabilityFilter(base string) string {
	return base + "/remotes/+/+/" + remoteAvailabilityKind
}

// parseRemoteAvailabilityTopic maps an availability topic back to the
// controller whose pod it names.
func parseRemoteAvailabilityTopic(base, topic string) (namespace, name string, ok bool) {
	return parseRemoteTopic(base, topic, remoteAvailabilityKind)
}

// parseRemoteTopic maps one three-segment remotes topic back to the
// controller it names, for the kind its last segment carries. It matches
// the segment count as well as the kind, so a cycle topic, which carries a
// fourth segment, matches no three-segment kind.
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

// parseRemoteFocusTopic maps a focus topic back to the controller it
// names. It matches only the three-segment focus mark, so a cycle topic,
// which carries a fourth segment, does not read as a mark.
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

// parseRemoteFocusCycleTopic maps a cycle topic back to the controller it
// names. It matches only the four-segment cycle request, so a plain focus
// mark does not read as a cycle.
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

// playCommandsTopic carries the commands any program on the bus may
// publish to drive one Play: play-pause, a seek, a volume step, and
// the rest of the vocabulary in input.go. It is not retained, because
// a command is an event and not a state. This is the one open surface
// a program joins a Play on in media terms, so a phone or a Home
// Assistant integration reaches the Play the same way the playback
// pod's own key table does.
func playCommandsTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/commands"
}

// playerCommandsTopic carries one message, the re-present the operator
// sends when a Play ends. It is not retained, because a re-present is
// an event and not a state. The operator is the only writer: a client
// publishes nothing here, and no press is forwarded on it. It stays off
// the plays tree because it drives the standing idle screen, not a
// Play, and a controller sends nothing on it directly.
func playerCommandsTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/commands"
}

// playerStatusTopic carries one unit's presentable state: its friendly
// name, what it is doing, the Play it runs, and its parts with the link
// and charge of each. The operator is the only writer, and the topic is
// retained, so an idle pod that just started paints the live state the
// broker already holds and asks for nothing. It stays off the plays tree
// because it describes the equipment, which stands whether or not a Play
// runs.
func playerStatusTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/status"
}

// playerPanelTopic carries the panel state one unit's idle screen client
// publishes, retained, because the panel is a state and not an event.
// The client holds no API credentials, so the operator folds this
// topic into the Player's status.
func playerPanelTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/" + playerPanelKind
}

// playerPanelFilter is the operator's one subscription that reaches
// every unit's panel topic.
func playerPanelFilter(base string) string {
	return base + "/players/+/+/" + playerPanelKind
}

// parsePlayerPanelTopic maps a panel topic back to the Player it
// names.
func parsePlayerPanelTopic(base, topic string) (namespace, name string, ok bool) {
	return parsePlayerTopic(base, topic, playerPanelKind)
}

// parsePlayerTopic maps one three-segment players topic back to the
// unit it names, for the kind its last segment carries. It matches
// the segment count as well as the kind, the way parseRemoteTopic
// does, so a deeper topic under the same tree matches no kind.
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

// playerVolumeTopic carries the unit's listening level and its
// muted flag, retained, because the level is a state and not an
// event. Every pod for the unit subscribes and applies what it
// reads, and only a press or the operator publishes, so the topic is
// the authority and no observer ever writes back what it saw.
func playerVolumeTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/" + playerVolumeKind
}

// playerVolumeFilter is the operator's one subscription across
// every unit's level. The operator reads it to learn which units the
// broker already holds a level for, so the seed writes only where
// nothing stands.
func playerVolumeFilter(base string) string {
	return base + "/players/+/+/" + playerVolumeKind
}

// parsePlayerVolumeTopic maps a volume topic back to the Player it
// names.
func parsePlayerVolumeTopic(base, topic string) (namespace, name string, ok bool) {
	return parsePlayerTopic(base, topic, playerVolumeKind)
}

// remoteKeysTopic carries one Remote's compiled table, the base
// folded with its Keymap. It is per Remote and not per Keymap, because
// the standing pod that reads that one controller is the only
// subscriber, and a Remote with no Keymap still needs the base. It is
// retained, so the pod reads the current table the instant it
// connects, and an edit reaches it with no pod restart.
func remoteKeysTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remoteKeysKind
}

// playStatusTopic carries one Play's report: the paused flag, the
// item, the position, and the duration. The playback pod's sidecar
// publishes it retained, so a restarted operator reads the current
// position back from the broker and does not lose a running Play's
// place.
func playStatusTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/" + playStatusKind
}

// playAvailabilityTopic carries online or offline for the sidecar that
// publishes the status. The broker publishes offline on any disconnect
// the sidecar does not make cleanly.
func playAvailabilityTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/" + playAvailabilityKind
}

// playStatusFilter is the subscription that reaches every Play's
// status, whatever namespace and name it carries. The two plus signs
// are the MQTT single-level wildcards for the namespace and the name.
func playStatusFilter(base string) string {
	return base + "/plays/+/+/" + playStatusKind
}

// playAvailabilityFilter is the subscription that reaches every Play's
// availability signal.
func playAvailabilityFilter(base string) string {
	return base + "/plays/+/+/" + playAvailabilityKind
}

// parsePlayTopic maps an inbound plays topic back to the Play it names
// and the kind of message it carries. The operator subscribes to the
// two plays filters and reads each message's topic to learn which Play
// it belongs to, because the wildcard subscription carries messages
// for every Play on one stream.
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
