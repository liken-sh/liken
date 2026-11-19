package main

// This file lists the Event reasons that media-operator and media-api
// post, and builds the references to the objects they post on. The
// reasons are part of the published interface: the troubleshooting
// guide lists each one, and alerts and searches match on them.
//
// An Event tells a person what just happened, and the API server
// deletes it an hour after its last write. What is true now is in the
// Play's phase and conditions and in the Player's Screen condition. So
// the operator posts one Event for each phase change, each condition
// transition, and each action it takes on its own, such as creating a
// pod again. It posts none for a key press, a position report, a volume
// step, or a retry attempt. Those go to the log.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// The reporting component of an Event: the operator, or the HTTP API
// that runs in its own Deployment.
const (
	operatorComponent = "media-operator"
	apiComponent      = "media-api"
)

// The reasons of the Events on a Play.
const (
	// The phase changed to Running, Finished, or Failed. When the cause
	// of a Failed phase is the Play's own spec, the reason is
	// InvalidSpec instead.
	reasonPlaybackStarted  = "PlaybackStarted"
	reasonPlaybackFinished = "PlaybackFinished"
	reasonPlaybackFailed   = "PlaybackFailed"
	// The Play cannot run as written: it names no Player, an item does
	// not resolve, or a Remote the pod needs does not exist.
	reasonInvalidSpec = "InvalidSpec"
	// The operator created the playback pod again because the old pod
	// failed, was deleted, or no longer matched the Player's spec. The
	// same reason goes on a Player or a Remote when the operator replaces
	// its idle pod or reader pod that the kubelet ended (endedpod.go).
	reasonPodRecreated = "PodRecreated"
	// The pod failed again soon after being recreated, so the operator
	// waits longer before the next recreate.
	reasonResumeBackoff = "ResumeBackoff"
	// The operator deleted the Play, either because a newer Play on the
	// same Player replaced it (Superseded) or because its time to live
	// after finishing ran out (Retired). The operator posts the same
	// Event on the Player too, because `kubectl describe` cannot show a
	// deleted Play.
	reasonSuperseded = "Superseded"
	reasonRetired    = "Retired"
)

// The reasons of the Events on a Player. A transition of the Screen
// condition posts an Event with the condition's own reason, such as
// Present or PanelAway. Those reasons are in screen.go.
const (
	// Writing the session to the Player's Receiver failed, or a write
	// succeeded after earlier failures.
	reasonReceiverWriteFailed    = "ReceiverWriteFailed"
	reasonReceiverWriteRecovered = "ReceiverWriteRecovered"
)

// The reasons of the Events on a Remote.
const (
	// The Remote's Keymap does not compile, so the last key table that
	// compiled stays published on the bus.
	reasonKeymapRefused = "KeymapRefused"
)

// The reason of the Event that media-api posts on a Player for each
// request that returned captured data.
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

// transitions returns the conditions in next that are new or whose
// status or reason differ from the same type in held. Each caller
// passes as held the conditions of the copy that its status write
// replaced, which after a conflict is the copy read again from the API
// server, so each Event matches a change that the API server stored.
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
