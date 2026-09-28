package main

// These tests cover how each way the player container ends decides the
// run: a film that reached its end and a person's exit finish the Play,
// and a lost compositor or any other failure creates the run again at
// its last position.

import (
	"net/http"
	"testing"
)

// exitedPod is the playback pod after mpv exited with one code, in the
// phase the kubelet gives a pod whose one container exited that way.
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

// mpv exits zero at the end of the last item, and at the quit the
// sidecar sends for a person's exit press. Both finish the Play, and the
// pass creates no pod.
func TestAZeroExitFinishesTheRun(t *testing.T) {
	cluster := runningCluster(housePlayer())
	exitedPod(cluster, 0)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.fold("house", "movie", playReport{Item: 1, Position: "1:58:40", Ended: true})

	media.pass()

	mustMatch(t, cluster.plays["movie"].Status.Phase, phaseFinished)
	mustMatch(t, countMethod(cluster.requests, http.MethodPost), 0)
}

// Every non-zero exit resumes the run at its last position. The
// compositor that went away under the film is the one the shim's
// binding turns from a zero exit into one of these.
func TestANonZeroExitResumesTheRunWhereItWas(t *testing.T) {
	cases := []struct {
		name string
		code int
	}{
		{name: "the compositor went away", code: compositorLostExit},
		{name: "mpv could not play the file", code: 2},
		{name: "the kernel ended mpv over its limit", code: 137},
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

// A lost compositor names itself in the status, so a person reads why
// the film came back and not a bare exit code.
func TestALostCompositorNamesItselfInTheStatus(t *testing.T) {
	pod := playbackPod(podFailed)
	pod.Status.ContainerStatuses = []ContainerStatus{{
		Name:  playerContainer,
		State: ContainerState{Terminated: &ContainerStateTerminated{ExitCode: compositorLostExit, Reason: "Error"}},
	}}

	mustMatch(t, podFailureMessage(pod), "the playback pod failed: the player lost its compositor (exit code 7)")
}
