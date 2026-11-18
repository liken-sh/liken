package main

// This file builds a Play's status and writes it. A pass derives the
// status from the current state, not from the events that woke it, so
// the same inputs must give the same status in any event order. A
// timestamp changes only on a transition: conditions.Set changes a
// condition's lastTransitionTime only when its status changes, and
// retire in operate.go writes finishedAt once, in the first pass that
// reads the Play as Finished.

import (
	"encoding/json"
	"fmt"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

func derivePlayStatus(play *Play, player *Player, buildErr error, pod *Pod, latest *playReport, prefs resolvedPreferences) PlayStatus {
	status := buildPlayStatus(play, player, buildErr, pod, latest)
	status.Activity = playActivity(status.Phase, status.Paused)
	// A person compares the resolved preferences with the languages mpv
	// chose, AudioLanguage and SubtitleLanguage.
	status.AudioLanguages = prefs.AudioLanguages
	status.SubtitleLanguages = prefs.SubtitleLanguages
	status.Subtitles = prefs.Subtitles
	status.SDH = prefs.SDH
	return status
}

// buildPlayStatus reads the phase from the pod and the playback
// position from the pod's latest report. player is nil when the Player
// does not exist, buildErr is set when no pod could be built for the
// Play, pod is nil when the run has not started, and latest is nil when
// the pod has sent no report.
func buildPlayStatus(play *Play, player *Player, buildErr error, pod *Pod, latest *playReport) PlayStatus {
	if buildErr != nil {
		// For example, a URI the resolver refuses, or a Remote whose
		// Keymap does not compile.
		return PlayStatus{Phase: phaseFailed, Message: buildErr.Error()}
	}
	if player == nil {
		// Pending, not Failed, because Failed is final and the Player
		// can still be created after the Play.
		return PlayStatus{
			Phase:   phasePending,
			Message: fmt.Sprintf("the Player %s does not exist in this namespace", playerName(play)),
		}
	}
	if pod == nil {
		return PlayStatus{
			Phase:    phasePending,
			Item:     play.Status.Item,
			Position: play.Status.Position,
			Duration: play.Status.Duration,
		}
	}

	// The position and the track languages stay as the last report set
	// them until a newer report arrives. The reports store drops a run's
	// report when its pod goes offline, and a replacement pod resumes
	// from the position in the status, so clearing it would restart the
	// film from the top.
	status := PlayStatus{
		Pod:              pod.Metadata.Name,
		Item:             play.Status.Item,
		Position:         play.Status.Position,
		Duration:         play.Status.Duration,
		AudioLanguage:    play.Status.AudioLanguage,
		SubtitleLanguage: play.Status.SubtitleLanguage,
	}
	switch pod.Status.Phase {
	case podRunning:
		status.Phase = phaseRunning
		foldReport(&status, latest)
		status.Paused = latest != nil && latest.Paused
		// When the display container keeps restarting, the film would
		// play without its on-screen display. The run ends as Finished
		// instead, the same as when the film ends, and the Player
		// returns to its idle screen.
		if condition, reported := displayAlive(pod); reported {
			status.Conditions = []PlayCondition{
				foldPlayCondition(play.Status.Conditions, condition, play.Metadata.Generation),
			}
			if displayKeepsDying(pod) {
				status.Phase = phaseFinished
				status.Message = condition.Message
			}
		}
	case podSucceeded:
		// mpv exited zero because the last item ended or a person
		// pressed the exit key. A lost compositor exits with
		// compositorLostExit instead, so that the pod fails and the run
		// starts again.
		status.Phase = phaseFinished
		foldReport(&status, latest)
	case podFailed:
		status.Phase = phaseFailed
		status.Message = podFailureMessage(pod)
	default:
		// The pod has not started. When no node can allocate the
		// Player's devices, the pod's message names the request that
		// failed. Other scheduling failures, such as a missing claim,
		// appear only in a pod condition.
		status.Phase = phasePending
		if pod.Status.Message != "" {
			status.Message = pod.Status.Message
		} else {
			status.Message = podConditionMessage(pod)
		}
	}
	return status
}

// foldPlayCondition returns condition as conditions.Set merges it into
// the Play's current conditions, so it keeps the existing
// lastTransitionTime while its status is unchanged.
func foldPlayCondition(current []PlayCondition, condition PlayCondition, generation int64) PlayCondition {
	condition.ObservedGeneration = generation
	list := append([]PlayCondition(nil), current...)
	conditions.Set(&list, condition)
	folded, _ := conditions.Find(list, condition.Type)
	return folded
}

// podConditionMessage returns the message of the first pod condition
// whose status is False, or "". The scheduler reports a missing claim
// this way.
func podConditionMessage(pod *Pod) string {
	for _, condition := range pod.Status.Conditions {
		if condition.Status == "False" && condition.Message != "" {
			return condition.Message
		}
	}
	return ""
}

// foldReport copies the playback fields of the sidecar's report into
// the status. The position and duration stay the strings the sidecar
// sent, because the operator never parses them.
func foldReport(status *PlayStatus, latest *playReport) {
	if latest == nil {
		return
	}
	status.Item = latest.Item
	status.Position = latest.Position
	status.Duration = latest.Duration
	status.AudioLanguage = latest.AudioLanguage
	status.SubtitleLanguage = latest.SubtitleLanguage
}

// podFailureMessage describes why a pod failed. It prefers the player
// container's terminated state, because the exit code tells a person
// what happened. 1 means the player could not build mpv's arguments or
// mpv failed to start, compositorLostExit means the compositor went
// away, and 137 means the container was killed with SIGKILL, usually
// for exceeding its memory limit.
func podFailureMessage(pod *Pod) string {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != playerContainer || container.State.Terminated == nil {
			continue
		}
		terminated := container.State.Terminated
		reason := terminated.Reason
		if terminated.ExitCode == compositorLostExit {
			reason = "the player lost its compositor"
		}
		if reason == "" {
			reason = "the player exited"
		}
		return fmt.Sprintf("the playback pod failed: %s (exit code %d)", reason, terminated.ExitCode)
	}
	if pod.Status.Message != "" {
		return "the playback pod failed: " + pod.Status.Message
	}
	if pod.Status.Reason != "" {
		return "the playback pod failed: " + pod.Status.Reason
	}
	return "the playback pod failed"
}

func playerName(play *Play) string {
	if len(play.Spec.Players) == 0 {
		return ""
	}
	return play.Spec.Players[0]
}

// writePlayStatus writes desired to the Play's status. It skips the
// write when the status is already the same, because every write wakes
// the operator's own Play watch, and the next pass would write again.
// When the write fails because the copy is stale (409 or 404), it reads
// the Play again and tries once more.
//
// versions is the memo of the Play store (objectcache.go), and it may
// be nil.
func writePlayStatus(c *apiclient.Client, versions *memo.Versions, play *Play, desired PlayStatus) error {
	_, _, err := writePlayStatusFrom(c, versions, play, desired)
	return err
}

// writePlayStatusFrom is writePlayStatus, and it also returns the
// status the Play had before the write and whether it wrote. When the
// first write fails on a stale copy, the returned status comes from the
// fresh read. The caller compares against it in its log lines,
// metrics, and Events, so they describe the change the API server
// actually stored.
//
// After the write, play is the API server's response, with the new
// resourceVersion. A second write in the same pass then does not fail
// with a conflict.
func writePlayStatusFrom(c *apiclient.Client, versions *memo.Versions, play *Play, desired PlayStatus) (PlayStatus, bool, error) {
	was := play.Status
	var compared error
	wrote, err := informer.SettleStatus(c, versions, playPath(play.Metadata.Namespace, play.Metadata.Name), play,
		func(stored *Play) bool {
			was = stored.Status
			same, err := sameStatus(stored.Status, desired)
			if err != nil {
				compared = err
				return false
			}
			if same {
				return false
			}
			stored.Status = desired
			return true
		})
	if compared != nil {
		return was, false, compared
	}
	return was, wrote, err
}

// onlyPositionChanged reports whether desired differs from current only
// in Position and Duration. positionWriteInterval in operate.go limits
// how often such a change is written.
func onlyPositionChanged(current, desired PlayStatus) bool {
	same, err := sameStatus(current, desired)
	if err != nil || same {
		return false
	}
	current.Position, desired.Position = "", ""
	current.Duration, desired.Duration = "", ""
	rest, err := sameStatus(current, desired)
	return err == nil && rest
}

// sameStatus compares the JSON of the two statuses, because the JSON is
// what the API server stores, so differences that omitempty drops do
// not count.
func sameStatus(current, desired PlayStatus) (bool, error) {
	was, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	wants, err := json.Marshal(desired)
	if err != nil {
		return false, err
	}
	return string(was) == string(wants), nil
}
