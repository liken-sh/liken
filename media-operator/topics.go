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

// remoteEventsTopic carries one Remote's raw button and axis events.
// The standing remote pod publishes to it, not retained, because a
// press is an event and not a state. The keymap stays off this topic,
// so the events are the controller's own evdev codes and one Remote
// can feed two players that map it differently.
func remoteEventsTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/events"
}

// remoteFocusTopic carries the mark that says which binding is active.
// It is retained control-plane state the operator writes. This plan
// publishes nothing to it, because a binding list of length one leaves
// no focus to arbitrate. The topic is named now so the sidecar can
// subscribe to it from the start and the mark can arrive in a later
// plan without a pod restart.
func remoteFocusTopic(base, namespace, name string) string {
	return base + "/remotes/" + namespace + "/" + name + "/focus"
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
