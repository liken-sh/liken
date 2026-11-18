package main

// This file derives a Play's DisplayAlive condition from the display
// sidecar of its playback pod, counts the sidecar's restarts in
// media_display_restarts_total, and ends the run when the sidecar
// restarts too often.

import "fmt"

// The type and the reasons of the DisplayAlive condition.
const (
	displayAliveCondition   = "DisplayAlive"
	displayReasonRunning    = "Running"
	displayReasonRestarting = "Restarting"
)

// maxDisplayRestarts is the number of display restarts a run survives.
// On the next restart the run ends, so a display that keeps crashing
// does not restart forever.
const maxDisplayRestarts = 2

// displayContainerStatus returns the status of the display container,
// which is a native sidecar and so reports among the init containers.
// The second value is false when the pod has no display container.
func displayContainerStatus(pod *Pod) (ContainerStatus, bool) {
	for _, container := range pod.Status.InitContainerStatuses {
		if container.Name == displayContainer {
			return container, true
		}
	}
	return ContainerStatus{}, false
}

// displayAlive derives the run's DisplayAlive condition from the pod.
// The second value is false when the pod has no display container, or
// the container has not started yet.
func displayAlive(pod *Pod) (PlayCondition, bool) {
	container, reported := displayContainerStatus(pod)
	if !reported {
		return PlayCondition{}, false
	}
	if container.State.Running != nil {
		return PlayCondition{
			Type:    displayAliveCondition,
			Status:  conditionTrue,
			Reason:  displayReasonRunning,
			Message: displayRestartMessage(container),
		}, true
	}
	// A container that is not running and has never terminated has not
	// started yet, which is not a display fault.
	if container.LastState.Terminated == nil {
		return PlayCondition{}, false
	}
	return PlayCondition{
		Type:    displayAliveCondition,
		Status:  conditionFalse,
		Reason:  displayReasonRestarting,
		Message: displayRestartMessage(container),
	}, true
}

// displayRestartMessage returns the restart count with the reason and
// exit code of the last termination, or "" when the display has not
// restarted.
func displayRestartMessage(container ContainerStatus) string {
	if container.RestartCount == 0 {
		return ""
	}
	restarts := fmt.Sprintf("the display restarted %d times", container.RestartCount)
	if container.RestartCount == 1 {
		restarts = "the display restarted 1 time"
	}
	terminated := container.LastState.Terminated
	if terminated == nil {
		return restarts
	}
	reason := terminated.Reason
	if reason == "" {
		reason = "the display exited"
	}
	return fmt.Sprintf("%s: %s (exit code %d)", restarts, reason, terminated.ExitCode)
}

// displayKeepsDying reports whether the display has restarted more
// than maxDisplayRestarts times, which ends the run.
func displayKeepsDying(pod *Pod) bool {
	container, reported := displayContainerStatus(pod)
	return reported && container.RestartCount > maxDisplayRestarts
}

// displayRestartCount returns the restart count of the display, or 0
// when the pod is nil or has no display container.
func displayRestartCount(pod *Pod) int {
	if pod == nil {
		return 0
	}
	container, reported := displayContainerStatus(pod)
	if !reported {
		return 0
	}
	return container.RestartCount
}

// displayRestartMemo records, for one run, the restart count already
// added to the counter and the Player that labels the series. The
// Player is kept because the series is deleted after the Play is gone.
type displayRestartMemo struct {
	player string
	count  int
}

// countDisplayRestarts adds to media_display_restarts_total the
// restarts since the previous pass. A recreated pod starts its count at
// 0 again, and a lower count adds nothing, so the counter never
// decreases. The series is created on the first pass that reads the
// run's pod, so a healthy run exports 0 instead of no series.
func (o *operator) countDisplayRestarts(play *Play, pod *Pod) {
	key := runKey(play.Metadata.Namespace, play.Metadata.Name)
	count := displayRestartCount(pod)
	counted := o.displayRestarts[key]
	o.displayRestarts[key] = displayRestartMemo{player: playerName(play), count: count}
	if o.metrics == nil || pod == nil {
		return
	}
	series := o.metrics.displayRestarts.WithLabelValues(playerName(play))
	if count > counted.count {
		series.Add(float64(count - counted.count))
	}
}

// forgetDisplayRestarts deletes the run's memo and its metric series.
func (o *operator) forgetDisplayRestarts(key string) {
	memo := o.displayRestarts[key]
	delete(o.displayRestarts, key)
	if o.metrics != nil && memo.player != "" {
		o.metrics.displayRestarts.DeleteLabelValues(memo.player)
	}
}
