package main

// The bus topic layout. No pod this operator stands holds an API
// credential, so every fact a scanner learns about a library, and
// every choice a person makes on a screen, reaches the control plane
// over the bus. This file builds the topics the pods publish and the
// filters the operator subscribes to, so it is the public contract of
// the bus and the one place another program reads to follow a library
// or to ask for a play.
//
// The broker is the one the media operator runs. Each topic extends a
// base the operator holds as one string, liken/library by default, so
// this operator's tree and the media operator's liken/media tree stay
// disjoint on the same broker. A base that carries a cluster's name so
// several clusters share one broker is a later refinement the string
// already allows.

import "strings"

// defaultTopicBase is the base every topic extends when the operator
// sets none.
const defaultTopicBase = "liken/library"

// The two words the availability topic carries. A scanner names its
// availability topic as the MQTT Last Will with offline as the
// payload, and publishes online once it connects, so a retained report
// a killed pod left behind does not read as a running scanner.
const (
	availabilityOnline  = "online"
	availabilityOffline = "offline"
)

// The kind at the end of a libraries topic. parseLibraryTopic returns
// one of these so the operator folds a report and an availability
// signal through separate paths.
const (
	libraryStatusKind       = "status"
	libraryAvailabilityKind = "availability"
)

// libraryStatusTopic carries one Library's report: the count of
// titles, the count of folders the scanner could not identify, and the
// times of the last walk and the last change. The scanner publishes it
// retained, so an operator that restarts reads the current counts back
// from the broker without waiting for the next walk.
func libraryStatusTopic(base, namespace, name string) string {
	return base + "/libraries/" + namespace + "/" + name + "/" + libraryStatusKind
}

// libraryAvailabilityTopic carries online or offline for the scanner
// that publishes the report. The broker publishes offline on any
// disconnect the scanner does not make cleanly.
func libraryAvailabilityTopic(base, namespace, name string) string {
	return base + "/libraries/" + namespace + "/" + name + "/" + libraryAvailabilityKind
}

// libraryStatusFilter is the subscription that reaches every Library's
// report, whatever namespace and name it carries. The two plus signs
// are the MQTT single-level wildcards for the namespace and the name.
func libraryStatusFilter(base string) string {
	return base + "/libraries/+/+/" + libraryStatusKind
}

// libraryAvailabilityFilter is the subscription that reaches every
// scanner's availability signal.
func libraryAvailabilityFilter(base string) string {
	return base + "/libraries/+/+/" + libraryAvailabilityKind
}

// playRequestKind is the last level of a play request topic. A screen
// pod publishes what a person chose here, because the browser holds
// the catalog and the operator holds the credential that creates a
// Play.
const playRequestKind = "play"

// playRequestTopic carries one Player's play requests. The operator
// sets it on the browser container, because the browser knows neither
// this operator's topic base nor the Player's name.
func playRequestTopic(base, namespace, player string) string {
	return base + "/players/" + namespace + "/" + player + "/" + playRequestKind
}

// playRequestFilter is the subscription that reaches every Player's
// play requests. A media browser of another make that publishes here
// gets the same service.
func playRequestFilter(base string) string {
	return base + "/players/+/+/" + playRequestKind
}

// parsePlayRequestTopic maps an inbound play topic back to the Player
// it names. The operator creates a Play only for a Player it serves,
// so the topic is what says which Player asked.
func parsePlayRequestTopic(base, topic string) (namespace, player string, ok bool) {
	prefix := base + "/players/"
	if !strings.HasPrefix(topic, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	if len(parts) != 3 {
		return "", "", false
	}
	namespace, player = parts[0], parts[1]
	if namespace == "" || player == "" || parts[2] != playRequestKind {
		return "", "", false
	}
	return namespace, player, true
}

// parseLibraryTopic maps an inbound libraries topic back to the
// Library it names and the kind of message it carries. The operator
// subscribes to the two filters above, and each wildcard subscription
// carries messages for every Library on one stream, so the topic is
// what says which Library a message belongs to.
func parseLibraryTopic(base, topic string) (namespace, name, kind string, ok bool) {
	prefix := base + "/libraries/"
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
	if kind != libraryStatusKind && kind != libraryAvailabilityKind {
		return "", "", "", false
	}
	return namespace, name, kind, true
}
