package main

// This file lists every Event reason the media operator and media-api
// post, and the references that name the objects they post on. The
// reasons are part of the published interface: the troubleshooting
// guide names each one, and an alert or a person's search reads them.
//
// An Event records what just happened, for a person, and the API
// server deletes it an hour after its last write. The Play's phase,
// its conditions, and the Player's Screen condition hold what is true
// now. So each phase move and each condition transition posts one
// Event, and so does each action the operator takes on its own, such
// as a pod it creates again. A press, a position report, a volume
// step, and each pass of a retry loop post none: those go to the log.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// The reportingComponent of each Event: the operator, and the HTTP API
// that runs in its own Deployment.
const (
	operatorComponent = "media-operator"
	apiComponent      = "media-api"
)

// The reasons of the Events on a Play.
const (
	// The phase moved to Running, Finished, or Failed. A Failed phase
	// whose cause is the Play's own spec is InvalidSpec instead.
	reasonPlaybackStarted  = "PlaybackStarted"
	reasonPlaybackFinished = "PlaybackFinished"
	reasonPlaybackFailed   = "PlaybackFailed"
	// The Play can never run as written: it names no Player, an item
	// does not resolve, or a Remote the pod needs does not exist.
	reasonInvalidSpec = "InvalidSpec"
	// The operator created the playback pod again: the old one failed,
	// vanished, or no longer matched the Player's spec.
	reasonPodRecreated = "PodRecreated"
	// The pod failed again soon after a recreate, so the next recreate
	// waits longer.
	reasonResumeBackoff = "ResumeBackoff"
	// The operator deleted the Play: a newer Play on the same Player
	// replaced it, or its time to live after it finished passed. The
	// same Event goes on the Player, because the Play is gone.
	reasonSuperseded = "Superseded"
	reasonRetired    = "Retired"
)

// The reasons of the Events on a Player. The Screen condition's own
// reasons, such as Present and PanelAway, are the reasons of its
// transitions, so they are listed in screen.go with the condition.
const (
	// Writing the session on the unit's Receiver failed, and the
	// write that landed after one or more failures.
	reasonReceiverWriteFailed    = "ReceiverWriteFailed"
	reasonReceiverWriteRecovered = "ReceiverWriteRecovered"
)

// The reasons of the Events on a Remote.
const (
	// The Remote's Keymap does not compile, so the last good key table
	// stays on the bus.
	reasonKeymapRefused = "KeymapRefused"
)

// The reason of the Event media-api posts on a Player for each request
// that produced bytes.
const reasonCaptured = "Captured"

func playRef(play *Play) events.ObjectReference {
	return objectRef("Play", play.Metadata)
}

func playerRef(player *Player) events.ObjectReference {
	return objectRef("Player", player.Metadata)
}

func remoteRef(remote *Remote) events.ObjectReference {
	return objectRef("Remote", remote.Metadata)
}

func objectRef(kind string, meta ObjectMeta) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: mediaAPIVersion,
		Kind:       kind,
		Namespace:  meta.Namespace,
		Name:       meta.Name,
		UID:        meta.UID,
	}
}

// transitions answers the conditions of next that transitioned from
// held: each one that is new, or whose status or reason changed. A
// status write composes its conditions from the copy the pass read,
// and the write compares them with the copy the API server held, so
// the Events follow what the API server stored.
func transitions(held, next []conditions.Condition) []conditions.Condition {
	var moved []conditions.Condition
	list := append([]conditions.Condition(nil), held...)
	for _, condition := range next {
		if conditions.Set(&list, condition) {
			moved = append(moved, condition)
		}
	}
	return moved
}
