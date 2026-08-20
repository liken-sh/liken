package main

// Every status this operator writes comes from the one derivation in
// this file, and the derivation reads nothing but its arguments. The
// shape matters because the loop is level-triggered: a pass must
// reach the same status from the same facts, whatever order the
// events arrived in, and a function of its arguments cannot do
// otherwise.

import (
	"encoding/json"
	"errors"
	"fmt"
)

// derivePlayStatus builds the phase-and-numbers status and stamps
// the activity word onto it, so every path through the derivation
// carries the one word a person reads without a second rule
// deciding it later.
func derivePlayStatus(play *Play, player *Player, buildErr error, pod *Pod, latest *playReport) PlayStatus {
	status := buildPlayStatus(play, player, buildErr, pod, latest)
	status.Activity = playActivity(status.Phase, status.Paused)
	return status
}

// buildPlayStatus takes the phase from the pod and the numbers from
// the pod's reports. The kubelet is the authority for whether the
// player process runs, and it cannot lie about an exit; only mpv
// knows where the playhead is, and it can only say so through the
// supervisor's reports. Each argument may be absent: a nil player is
// a Player not yet declared, a buildErr is a run no pod could ever
// perform, a nil pod is a run not yet started, and a nil report is a
// pod that has not spoken yet.
func buildPlayStatus(play *Play, player *Player, buildErr error, pod *Pod, latest *playReport) PlayStatus {
	if buildErr != nil {
		// A URI the resolver refuses, or a Remote whose Keymap will
		// not compile, fails the Play before any object exists,
		// because the pod it describes could never be built.
		return PlayStatus{Phase: phaseFailed, Message: buildErr.Error()}
	}
	if player == nil {
		// An absent Player is Pending rather than Failed. The Player
		// may arrive after the Play, and the run starts when it
		// does; Failed would end a run that never had its chance.
		return PlayStatus{
			Phase:   phasePending,
			Message: fmt.Sprintf("the Player %s does not exist in this namespace", playerName(play)),
		}
	}
	if pod == nil {
		return PlayStatus{Phase: phasePending}
	}

	status := PlayStatus{Pod: pod.Metadata.Name}
	switch pod.Status.Phase {
	case podRunning:
		// Running covers a paused film too. The phase moves forward
		// only; the paused field beside the position says the rest.
		status.Phase = phaseRunning
		foldReport(&status, latest)
		status.Paused = latest != nil && latest.Paused
	case podSucceeded:
		// The last item ended: mpv exited zero and the pod
		// succeeded. The numbers stay as the last report left them,
		// so a finished season still shows which episode ended it
		// and where.
		status.Phase = phaseFinished
		foldReport(&status, latest)
	case podFailed:
		status.Phase = phaseFailed
		status.Message = podFailureMessage(pod)
	default:
		// A pod that has not started yet is a Play still Pending.
		// The ordinary hold is allocation: a Player whose devices no
		// one machine can satisfy parks its pod unschedulable, and
		// the pod's own message says which request wants.
		status.Phase = phasePending
		if pod.Status.Message != "" {
			status.Message = pod.Status.Message
		}
	}
	return status
}

// foldReport passes the item, the position, and the duration through
// as the supervisor sent them. This operator never parses a time; it
// carries the supervisor's strings into the status unread.
func foldReport(status *PlayStatus, latest *playReport) {
	if latest == nil {
		return
	}
	status.Item = latest.Item
	status.Position = latest.Position
	status.Duration = latest.Duration
}

// podFailureMessage prefers the container's terminated state,
// because it carries the exit code, which is the part a person acts
// on: 1 is the player refusing its input, 137 is the kernel ending a
// pod over its limit.
func podFailureMessage(pod *Pod) string {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != playerContainer || container.State.Terminated == nil {
			continue
		}
		terminated := container.State.Terminated
		reason := terminated.Reason
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

// writePlayStatus follows the two rules that keep the write quiet:
// an unchanged status is not written at all, and a conflict earns
// exactly one retry.
//
// The unchanged case matters because every status write bumps the
// resourceVersion, and this operator watches its own collection. A
// write per pass would wake the watch that wakes the pass, and the
// ten-second backstop would become a write every ten seconds for
// every settled Play in the cluster.
func writePlayStatus(c *Client, play *Play, desired PlayStatus) error {
	same, err := sameStatus(play.Status, desired)
	if err != nil {
		return err
	}
	if same {
		return nil
	}

	play.Status = desired
	_, err = PutPlayStatus(c, play)
	if !errors.Is(err, ErrConflict) {
		return err
	}

	// A conflict means something wrote the Play between the read and
	// the write. The fresh copy carries the resourceVersion the API
	// server will accept, and the desired status still describes the
	// same facts, so it goes on unchanged.
	fresh, err := GetPlay(c, play.Metadata.Namespace, play.Metadata.Name)
	if err != nil {
		return err
	}
	same, err = sameStatus(fresh.Status, desired)
	if err != nil || same {
		return err
	}
	fresh.Status = desired
	_, err = PutPlayStatus(c, fresh)
	return err
}

// sameStatus compares the marshaled form, because that is what the
// API server stores and what a field's omitempty decides: two
// statuses that marshal alike write alike.
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
