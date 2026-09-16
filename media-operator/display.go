package main

// The display sidecar's liveness on a run. This file derives the
// DisplayAlive condition a Play reports, feeds the restart counter the
// metrics serve, and decides the point where a display that keeps
// crashing ends the run.

import "fmt"

// The DisplayAlive condition a Play carries, and its two reasons.
const (
	displayAliveCondition   = "DisplayAlive"
	displayReasonRunning    = "Running"
	displayReasonRestarting = "Restarting"
)

// A run whose display has restarted more than displayRestartsEnding
// times has no display a viewer can use, so the run ends.
const displayRestartsEnding = 2

// displayContainerStatus returns the status of the display container.
// A playback pod with no screen has no display container, so the second
// value reports whether the pod has one at all.
func displayContainerStatus(pod *Pod) (ContainerStatus, bool) {
	for _, container := range pod.Status.InitContainerStatuses {
		if container.Name == displayContainer {
			return container, true
		}
	}
	return ContainerStatus{}, false
}

// displayAlive derives the run's DisplayAlive condition from the pod.
// The second value is false where the pod reports too little to state
// one: no display container, or a container that has not started yet.
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
	// A container that has neither started nor ended belongs to a pod
	// that is still starting, which is no fault of the display.
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

// displayRestartMessage states the restart count and the last exit the
// kubelet recorded, its reason and its exit code, because those are the
// facts a person acts on.
//
// A display with a restart count of 0 has no message, because there is
// no exit to report and the status and reason say the rest.
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
// than displayRestartsEnding times, the most one run allows.
func displayKeepsDying(pod *Pod) bool {
	container, reported := displayContainerStatus(pod)
	return reported && container.RestartCount > displayRestartsEnding
}

// displayRestartCount returns the restart count of one run's display.
// It is zero where the pod is gone or has no display container.
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

// displayRestartMemo is the restart count one run has already added to
// the counter, and the Player its series is labeled with, so the run's
// end can delete that series.
type displayRestartMemo struct {
	player string
	count  int
}

// countDisplayRestarts adds the growth of the kubelet's count since the
// last pass to media_display_restarts_total. A recreated pod starts its
// count at zero again, and a count that fell adds nothing, so the total
// never falls.
//
// The series is created at 0 on the pass that first reads the run's
// pod, so a scrape of a healthy run reports 0 and not an absent series.
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

// forgetDisplayRestarts drops one run's memo and deletes the series the
// run created, the way the pass drops every other memo of a run the
// collection no longer holds.
func (o *operator) forgetDisplayRestarts(key string) {
	memo := o.displayRestarts[key]
	delete(o.displayRestarts, key)
	if o.metrics != nil && memo.player != "" {
		o.metrics.displayRestarts.DeleteLabelValues(memo.player)
	}
}
