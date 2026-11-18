package main

import (
	"net/http"
	"testing"
)

// exitedPod sets the playback pod to the state after the player
// container exits with code.
func exitedPod(cluster *fakeCluster, code int) {
	pod := cluster.pods["movie-playback"]
	pod.Status.Phase = podSucceeded
	if code != 0 {
		pod.Status.Phase = podFailed
	}
	pod.Status.ContainerStatuses = []ContainerStatus{{
		Name:  playerContainer,
		State: ContainerState{Terminated: &ContainerStateTerminated{ExitCode: code, Reason: "Completed"}},
	}}
}

// mpv exits with code 0 after the last item and when the sidecar sends
// quit for an exit press. Both finish the Play.
func TestAZeroExitFinishesTheRun(t *testing.T) {
	cluster := runningCluster(housePlayer())
	exitedPod(cluster, 0)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.fold("house", "movie", playReport{Item: 1, Position: "1:58:40", Ended: true})

	media.pass()

	mustMatch(t, cluster.plays["movie"].Status.Phase, phaseFinished)
	mustMatch(t, countMethod(cluster.requests, http.MethodPost), 0)
}

// After any non-zero exit, the pass creates the pod again at the last
// reported position. compositorLostExit in player.go explains why a lost
// compositor exits non-zero.
func TestANonZeroExitResumesTheRunWhereItWas(t *testing.T) {
	cases := []struct {
		name string
		code int
	}{
		{name: "the compositor was lost", code: compositorLostExit},
		{name: "mpv could not play the file", code: 2},
		{name: "mpv was killed with SIGKILL", code: 137},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := runningCluster(housePlayer())
			exitedPod(cluster, each.code)
			media := testOperator(t, cluster, make(chan struct{}, 1))
			media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:22:21", Ended: true})

			media.pass()

			mustMatch(t, cluster.plays["movie"].Status.FinishedAt, "")
			mustMatch(t, podStart(cluster.pods["movie-playback"]), "0:22:21")
		})
	}
}

// The failure message says that the player lost its compositor, so a
// person sees why the film restarted and not only an exit code.
func TestAPodFailureMessageNamesALostCompositor(t *testing.T) {
	pod := playbackPod(podFailed)
	pod.Status.ContainerStatuses = []ContainerStatus{{
		Name:  playerContainer,
		State: ContainerState{Terminated: &ContainerStateTerminated{ExitCode: compositorLostExit, Reason: "Error"}},
	}}

	mustMatch(t, podFailureMessage(pod), "the playback pod failed: the player lost its compositor (exit code 7)")
}
