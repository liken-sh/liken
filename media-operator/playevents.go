package main

// This file posts the Events of a run: each move of its phase, each
// pod the operator creates again, and the delete that ends a Play. The
// reasons are in events.go. Each Event here is also a log line, and
// the line keeps the detail an Event leaves out after an hour.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// postPhase posts the Event of one phase move, after the write that
// moved it landed. A move to Pending posts nothing: a new run waits
// for its pod, and a recreate posts PodRecreated on its own.
// failReason is the reason a move to Failed posts, so a Play that can
// never run reads InvalidSpec and a pod that failed reads
// PlaybackFailed.
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

// notePodRecreated writes the line and posts the Event of a playback
// pod the operator created again. fault marks a pod that failed or
// vanished, which is a Warning, apart from a spec edit, which is
// Normal. A run whose pod keeps failing also posts ResumeBackoff,
// with the wait the next recreate takes.
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
		fmt.Sprintf("the playback pod failed %d times in a row; the next recreate waits at least %s",
			state.count, backoffDelay(state.count).Round(time.Second)))
}

// postEnded posts the Event of a Play the operator deleted on the Play
// and on its Player. The Play is gone a moment later, and its Events
// with it in `kubectl describe`, so the Player keeps the record of
// which run ended and why.
func (o *operator) postEnded(play *Play, reason, message string) {
	o.recorder.Normal(playRef(play), reason, message)
	if player, found := o.playerOf(play); found {
		o.recorder.Normal(player, reason, "play "+play.Metadata.Name+": "+message)
	}
}

// playerOf answers the reference of the Player a Play names, from the
// Players the last pass listed. `kubectl describe` matches an Event to
// its object by UID, so a Player the pass has not listed gets no Event.
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
