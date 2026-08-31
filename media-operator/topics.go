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

// The kind at the end of the two remotes topics the operator reads for a
// controller's presence. They are separate constants from the plays kinds
// above, because the two trees carry different payloads under the same two
// words: a plays status is one run's report, and a remotes presence is one
// controller's connected flag.
const (
	remotePresenceKind     = "presence"
	remoteAvailabilityKind = "availability"
)

// The last segment of the players topic that carries the panel
// state, beside the commands and status topics of the same tree.
const playerPanelKind = "panel"

// The last segment of the players topic that carries the listening
// level, beside the panel, commands, and status kinds of the same
// tree.
const playerVolumeKind = "volume"

// The last segment of the players topic that carries what the idle
// sidecar decided for the screen, beside the panel, commands, status,
// and volume kinds of the same tree.
const playerScreenKind = "screen"

// remoteEventsTopic carries one Remote's raw button and axis events.
// The standing remote pod publishes to it, not retained, because a
// press is an event and not a state. The keymap stays off this topic,
// so the events are the controller's own evdev codes and one Remote
// can feed two players that map it differently.
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
// publishes. Only the holder of focus publishes it, the translator during
// a film and the idle sidecar between films, and the operator reads it to
// advance the mark. It is not retained, because a cycle is an event and
// not a state.
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

// remotePresenceTopic carries whether one controller is connected right
// now, as {"connected": true} or false. The standing remote pod publishes
// it retained, because presence is a state and not an event, so the
// operator reads the current value the instant it subscribes. The pod
// senses the controller first-hand: its evdev nodes open when the
// controller connects and vanish when it disconnects, so the signal
// starts where it is read and no Kubernetes watch carries it.
func remotePresenceTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remotePresenceKind
}

// remoteAvailabilityTopic carries online or offline for the standing
// remote pod itself, the same two words the plays availability uses. The
// pod names this topic as its MQTT Last Will, so a pod the kubelet killed
// reads offline and the retained presence it left behind does not stand as
// a connected controller.
func remoteAvailabilityTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/" + remoteAvailabilityKind
}

// remotePresenceFilter is the operator's subscription that reaches every
// controller's presence. The operator folds each message into the Player
// status it publishes, so the idle pod reads one topic and no more.
func remotePresenceFilter(base string) string {
	return base + "/remotes/+/+/" + remotePresenceKind
}

// remoteAvailabilityFilter is the operator's subscription that reaches
// every standing remote pod's availability signal.
func remoteAvailabilityFilter(base string) string {
	return base + "/remotes/+/+/" + remoteAvailabilityKind
}

// parseRemotePresenceTopic maps a presence topic back to the controller it
// names.
func parseRemotePresenceTopic(base, topic string) (namespace, name string, ok bool) {
	return parseRemoteTopic(base, topic, remotePresenceKind)
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

// playCommandsTopic carries the named media commands any program may
// publish to drive one Play: play-pause, a seek, a volume step, and the
// rest of the vocabulary in input.go. It is not retained, because a
// command is an event and not a state. This is the one open surface a
// program joins a Play on in media terms, so a translator, a phone, or a
// Home Assistant integration all reach the Play the same way.
func playCommandsTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/commands"
}

// playerCommandsTopic carries the display commands the operator sends
// one Player's idle pod, the re-present that returns the screen to the
// idle client when a Play ends. It is not retained, because a re-present is an event
// and not a state, the same as the play-commands and remote-events
// topics. It stays off the plays tree because it drives the standing
// idle pod, not a Play, and it carries no media vocabulary a controller
// sends.
func playerCommandsTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/commands"
}

// playerStatusTopic carries one unit's presentable state: its friendly
// name, what it is doing, the Play it runs, and its parts with the
// presence of each. The operator is the only writer, and the topic is
// retained, so an idle pod that just started paints the live state the
// broker already holds and asks for nothing. It stays off the plays tree
// because it describes the equipment, which stands whether or not a Play
// runs.
func playerStatusTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/status"
}

// playerPanelTopic carries the panel state one unit's idle sidecar
// publishes, retained, because the panel is a state and not an event.
// The sidecar holds no API credentials, so the operator folds this
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

// playerScreenTopic carries the four moments the idle sidecar decides
// for one unit's screen: the shade down, the shade up, a focus mark
// that named this unit, and the surface the idle client presents again
// when a Play ends. The sidecar is the only writer, and whichever
// client the idle pod runs reads them here.
//
// The shade is the lasting state of the two, so the shade down
// and the shade up are retained and a client that restarts reads the
// shade it left rather than waking lit. The focus and the present are
// moments, so they are not retained, and a restart replays no press. The
// broker holds one retained message per topic, which is the last shade
// either way.
func playerScreenTopic(base, namespace, name string) string {
	return base + "/players/" + namespace + "/" + name + "/" + playerScreenKind
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

// keymapTopic carries one Keymap's compiled table. It drops the
// namespace segment because a Keymap is cluster-scoped. The operator
// publishes the table here retained, so a translator sidecar reads the
// current table the instant it connects and a Keymap edit reaches every
// translator with no pod restart.
func keymapTopic(base, name string) string {
	return base + "/keymaps/" + name
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
