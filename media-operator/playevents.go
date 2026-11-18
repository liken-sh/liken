package main

// This file posts the Kubernetes Events of a Play: each phase change,
// each recreate of its playback pod, and the delete that ends it. The
// reasons are declared in events.go.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// postPhase posts the Event for a Play's new phase. The caller calls it
// only after the status write succeeded. A change to Pending posts
// nothing, because a recreated pod already posts PodRecreated.
// failReason is the reason to post for a change to Failed: InvalidSpec
// or PlaybackFailed.
func (o *operator) postPhase(play *Play, status PlayStatus, failReason string) {
	object := playRef(play)
	switch status.Phase {
	case phaseRunning:
		o.recorder.Normal(object, reasonPlaybackStarted,
			fmt.Sprintf("playback pod %s runs on player %s", status.Pod, playerName(play)))
	case phaseFinished:
		o.recorder.Normal(object, reasonPlaybackFinished,
			fmt.Sprintf("finished at item %d, %s%s", status.Item, startName(status.Position), messageOf(status)))
	case phaseFailed:
		message := status.Message
		if message == "" {
			message = "the playback pod failed"
		}
		o.recorder.Warning(object, failReason, message)
	}
}

// notePodRecreated logs and posts PodRecreated for a recreated playback
// pod. fault is true when the old pod failed or disappeared, and makes
// the Event a Warning. A recreate after a spec edit is Normal. After a
// fault, once the run's recreate count reaches backoffNoteThreshold, it
// also posts ResumeBackoff with the delay before the next recreate.
func (o *operator) notePodRecreated(play *Play, message string, fault bool) {
	key := runKey(play.Metadata.Namespace, play.Metadata.Name)
	logLine(o.log, "play %s: %s", key, message)
	if !fault {
		o.recorder.Normal(playRef(play), reasonPodRecreated, message)
		return
	}
	o.recorder.Warning(playRef(play), reasonPodRecreated, message)
	state := o.recreateBackoff[key]
	if state.count < backoffNoteThreshold {
		return
	}
	o.recorder.Warning(playRef(play), reasonResumeBackoff,
		fmt.Sprintf("Recreated the playback pod %d times in a row, so the next recreate waits at least %s",
			state.count, backoffDelay(state.count).Round(time.Second)))
}

// postEnded posts the Event for a Play that the operator deleted, on the
// Play and on its Player. `kubectl describe` cannot show a deleted Play,
// so a person reads the Event on the Player.
func (o *operator) postEnded(play *Play, reason, message string) {
	o.recorder.Normal(playRef(play), reason, message)
	if player, found := o.playerOf(play); found {
		o.recorder.Normal(player, reason, "play "+play.Metadata.Name+": "+message)
	}
}

// playerOf returns the reference to the Play's Player from the Players
// that the last pass listed. The reference needs the Player's UID, so
// playerOf returns false for a Player that is not in that list.
func (o *operator) playerOf(play *Play) (events.ObjectReference, bool) {
	_, players := o.snapshot.lists()
	for index := range players {
		player := &players[index]
		if player.Metadata.Namespace == play.Metadata.Namespace && player.Metadata.Name == playerName(play) {
			return playerRef(player), true
		}
	}
	return events.ObjectReference{}, false
}
