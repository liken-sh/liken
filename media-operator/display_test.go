package main

// The tests for the display sidecar's liveness on a Play.

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// displayPod builds a running playback pod whose display sidecar
// reports the given restart count, whether it runs now, and its last
// termination.
func displayPod(restarts int, running bool, last *ContainerStateTerminated) *Pod {
	pod := playbackPod(podRunning)
	state := ContainerState{}
	if running {
		state.Running = &ContainerStateRunning{}
	}
	pod.Status.InitContainerStatuses = []ContainerStatus{{
		Name:         displayContainer,
		State:        state,
		RestartCount: restarts,
		LastState:    ContainerState{Terminated: last},
	}}
	return pod
}

// playCondition reads the one condition off a status, with its stamp
// cleared so a case compares the rest. The second value reports whether
// the status holds any condition at all.
func playCondition(t *testing.T, status PlayStatus) (PlayCondition, bool) {
	t.Helper()
	if len(status.Conditions) == 0 {
		return PlayCondition{}, false
	}
	if len(status.Conditions) != 1 {
		t.Fatalf("the status carries %+v, want one condition", status.Conditions)
	}
	condition := status.Conditions[0]
	condition.LastTransitionTime = ""
	return condition, true
}

func TestTheDisplayAliveCondition(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
	crash := &ContainerStateTerminated{ExitCode: 1, Reason: "Error"}

	cases := []struct {
		name  string
		pod   *Pod
		held  bool
		want  PlayCondition
		phase string
	}{{
		name:  "a pod with no display container reports nothing",
		pod:   playbackPod(podRunning),
		phase: phaseRunning,
	}, {
		name:  "a display that has not started yet reports nothing",
		pod:   displayPod(0, false, nil),
		phase: phaseRunning,
	}, {
		name: "a display that never restarted is alive",
		pod:  displayPod(0, true, nil),
		held: true,
		want: PlayCondition{
			Type:   displayAliveCondition,
			Status: conditionTrue,
			Reason: displayReasonRunning,
		},
		phase: phaseRunning,
	}, {
		name: "a display running again after one restart is alive, and says so",
		pod:  displayPod(1, true, crash),
		held: true,
		want: PlayCondition{
			Type:    displayAliveCondition,
			Status:  conditionTrue,
			Reason:  displayReasonRunning,
			Message: "the display restarted 1 time: Error (exit code 1)",
		},
		phase: phaseRunning,
	}, {
		name: "a display running again after two restarts is alive",
		pod:  displayPod(2, true, crash),
		held: true,
		want: PlayCondition{
			Type:    displayAliveCondition,
			Status:  conditionTrue,
			Reason:  displayReasonRunning,
			Message: "the display restarted 2 times: Error (exit code 1)",
		},
		phase: phaseRunning,
	}, {
		name: "a display waiting between restarts reads as restarting",
		pod:  displayPod(2, false, crash),
		held: true,
		want: PlayCondition{
			Type:    displayAliveCondition,
			Status:  conditionFalse,
			Reason:  displayReasonRestarting,
			Message: "the display restarted 2 times: Error (exit code 1)",
		},
		phase: phaseRunning,
	}, {
		name: "a restart with no reason names the exit alone",
		pod:  displayPod(2, false, &ContainerStateTerminated{ExitCode: 137}),
		held: true,
		want: PlayCondition{
			Type:    displayAliveCondition,
			Status:  conditionFalse,
			Reason:  displayReasonRestarting,
			Message: "the display restarted 2 times: the display exited (exit code 137)",
		},
		phase: phaseRunning,
	}, {
		name: "a restart the kubelet recorded no exit for names the count alone",
		pod:  displayPod(2, true, nil),
		held: true,
		want: PlayCondition{
			Type:    displayAliveCondition,
			Status:  conditionTrue,
			Reason:  displayReasonRunning,
			Message: "the display restarted 2 times",
		},
		phase: phaseRunning,
	}, {
		name: "a third restart ends the run, and the display that runs again says so",
		pod:  displayPod(3, true, crash),
		held: true,
		want: PlayCondition{
			Type:    displayAliveCondition,
			Status:  conditionTrue,
			Reason:  displayReasonRunning,
			Message: "the display restarted 3 times: Error (exit code 1)",
		},
		phase: phaseFinished,
	}, {
		name: "a third restart ends the run while the display is down",
		pod:  displayPod(3, false, crash),
		held: true,
		want: PlayCondition{
			Type:    displayAliveCondition,
			Status:  conditionFalse,
			Reason:  displayReasonRestarting,
			Message: "the display restarted 3 times: Error (exit code 1)",
		},
		phase: phaseFinished,
	}}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := derivePlayStatus(statusTestPlay(), player, nil, one.pod, nil, resolvedPreferences{})
			condition, held := playCondition(t, status)
			mustMatch(t, held, one.held)
			mustMatch(t, condition, one.want)
			mustMatch(t, status.Phase, one.phase)
		})
	}
}

// A run that ends on its display reports the display's message as its
// own.
func TestARunThatEndsOnTheDisplayCarriesTheReason(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
	pod := displayPod(3, true, &ContainerStateTerminated{ExitCode: 1, Reason: "Error"})

	status := derivePlayStatus(statusTestPlay(), player, nil, pod, nil, resolvedPreferences{})

	mustMatch(t, status.Phase, phaseFinished)
	mustMatch(t, status.Message, "the display restarted 3 times: Error (exit code 1)")
}

// The condition names the spec revision it was derived from, so a
// reader can tell a condition on the current spec from a stale one.
func TestTheDisplayAliveConditionReportsTheGeneration(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
	play := statusTestPlay()
	play.Metadata.Generation = 4

	status := derivePlayStatus(play, player, nil, displayPod(0, true, nil), nil, resolvedPreferences{})

	mustMatch(t, status.Conditions[0].ObservedGeneration, int64(4))
}

// The stamp moves only when the status changes.
func TestTheDisplayAliveStampMovesOnlyOnAChange(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
	stamped := "2026-01-01T00:00:00Z"
	crash := &ContainerStateTerminated{ExitCode: 1, Reason: "Error"}

	alive := PlayCondition{
		Type:               displayAliveCondition,
		Status:             conditionTrue,
		Reason:             displayReasonRunning,
		LastTransitionTime: stamped,
	}
	other := PlayCondition{Type: "Scheduled", Status: conditionTrue, LastTransitionTime: stamped}

	cases := []struct {
		name string
		held []PlayCondition
		pod  *Pod
		kept bool
	}{
		{name: "the display is alive as it was", held: []PlayCondition{alive},
			pod: displayPod(1, true, crash), kept: true},
		{name: "the display went from alive to restarting", held: []PlayCondition{alive},
			pod: displayPod(2, false, crash)},
		{name: "the run holds another condition first", held: []PlayCondition{other, alive},
			pod: displayPod(1, true, crash), kept: true},
		{name: "the run holds no condition of this type", held: []PlayCondition{other},
			pod: displayPod(1, true, crash)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			play := statusTestPlay()
			play.Status.Conditions = one.held

			status := derivePlayStatus(play, player, nil, one.pod, nil, resolvedPreferences{})

			mustMatch(t, status.Conditions[0].LastTransitionTime == stamped, one.kept)
		})
	}
}

// The counter follows the kubelet's count and never falls.
func TestDisplayRestartsCountUpAcrossPasses(t *testing.T) {
	cluster := runningCluster(housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.metrics = newMediaMetrics("test")
	crash := &ContainerStateTerminated{ExitCode: 1, Reason: "Error"}

	counted := func() float64 {
		return testutil.ToFloat64(media.metrics.displayRestarts.WithLabelValues("theater"))
	}

	cluster.pods["movie-playback"].Status.InitContainerStatuses =
		displayPod(1, true, crash).Status.InitContainerStatuses
	media.pass()
	mustMatch(t, counted(), 1)

	// The same count read twice counts once.
	media.pass()
	mustMatch(t, counted(), 1)

	cluster.pods["movie-playback"].Status.InitContainerStatuses =
		displayPod(2, true, crash).Status.InitContainerStatuses
	media.pass()
	mustMatch(t, counted(), 2)

	// A pod recreated at zero leaves the total where it stands.
	cluster.pods["movie-playback"].Status.InitContainerStatuses =
		displayPod(0, true, nil).Status.InitContainerStatuses
	media.pass()
	mustMatch(t, counted(), 2)

	body := scrapeMetrics(t, media.metrics.registry)
	if !strings.Contains(body, `media_display_restarts_total{player="theater"} 2`) {
		t.Errorf("the scrape did not carry the display restarts\n%s", body)
	}
}

// The count a run reports, read from the pod the pass holds.
func TestTheDisplayRestartCount(t *testing.T) {
	cases := []struct {
		name string
		pod  *Pod
		want int
	}{
		{name: "no pod at all"},
		{name: "a pod with no display container", pod: playbackPod(podRunning)},
		{name: "a display that restarted twice", pod: displayPod(2, true, nil), want: 2},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, displayRestartCount(one.pod), one.want)
		})
	}
}

// A run whose display never crashed reports a count of 0 from the pass
// that first reads its pod, so a scrape tells a healthy run from a unit
// with no run.
func TestAHealthyRunReportsZeroDisplayRestarts(t *testing.T) {
	cluster := runningCluster(housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.metrics = newMediaMetrics("test")

	media.pass()

	body := scrapeMetrics(t, media.metrics.registry)
	if !strings.Contains(body, `media_display_restarts_total{player="theater"} 0`) {
		t.Errorf("the scrape shows no series for a healthy run\n%s", body)
	}
}

// The series is deleted when the run leaves, so a unit that plays
// nothing has no display series at all.
func TestTheDisplayRestartsSeriesGoesWithTheRun(t *testing.T) {
	cluster := runningCluster(housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.metrics = newMediaMetrics("test")
	media.pass()
	mustMatch(t, testutil.CollectAndCount(media.metrics.displayRestarts), 1)

	delete(cluster.plays, "movie")
	media.pass()

	mustMatch(t, testutil.CollectAndCount(media.metrics.displayRestarts), 0)
}

// A display that keeps crashing ends the run the way a finished film
// ends: the pod and the claim go with it.
func TestADisplayThatKeepsDyingEndsTheRun(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Status.InitContainerStatuses =
		displayPod(3, true, &ContainerStateTerminated{ExitCode: 1, Reason: "Error"}).Status.InitContainerStatuses
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	mustMatch(t, cluster.plays["movie"].Status.Phase, phaseFinished)

	media.pass()
	if _, held := cluster.pods["movie-playback"]; held {
		t.Error("the run ended and kept its pod")
	}
	if _, held := cluster.claims["movie-devices"]; held {
		t.Error("the run ended and kept its claim")
	}
	if cluster.plays["movie"].Status.FinishedAt == "" {
		t.Error("the ended Play carries no finishedAt, so its window has no clock")
	}
}

// The first two restarts leave the running pod alone.
func TestTwoRestartsLeaveTheRunningPodAlone(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Status.InitContainerStatuses =
		displayPod(2, true, &ContainerStateTerminated{ExitCode: 1, Reason: "Error"}).Status.InitContainerStatuses
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	media.pass()

	mustMatch(t, cluster.plays["movie"].Status.Phase, phaseRunning)
	if _, held := cluster.pods["movie-playback"]; !held {
		t.Error("a restarting display took the pod with it")
	}
}

// The unit's bus status includes the Play's condition, so a client
// reads it off the bus.
func TestThePlayerBusStatusCarriesTheDisplayCondition(t *testing.T) {
	play := housePlay("https://nas/film.mkv")
	play.Status = PlayStatus{
		Phase: phaseRunning,
		Conditions: []PlayCondition{{
			Type:   "Scheduled",
			Status: conditionTrue,
		}, {
			Type:    displayAliveCondition,
			Status:  conditionFalse,
			Reason:  displayReasonRestarting,
			Message: "the display restarted 2 times: Error (exit code 1)",
		}},
	}
	plays := []Play{*play}

	got := derivePlayerBusStatus(housePlayer(), PlayerStatus{Activity: playerPlaying, Play: "movie"},
		plays, newPeripheralDesk(), newFocusDesk(nil))

	if got.Play == nil || got.Play.DisplayAlive == nil {
		t.Fatalf("the unit's status carries no display mark: %+v", got)
	}
	mustMatch(t, *got.Play.DisplayAlive, playerBusCondition{
		Status:  conditionFalse,
		Reason:  displayReasonRestarting,
		Message: "the display restarted 2 times: Error (exit code 1)",
	})
}

// A run whose display is alive reports no fault on the bus.
func TestThePlayerBusStatusMarksAnAliveDisplay(t *testing.T) {
	play := housePlay("https://nas/film.mkv")
	play.Status = PlayStatus{
		Phase: phaseRunning,
		Conditions: []PlayCondition{{
			Type:   displayAliveCondition,
			Status: conditionTrue,
			Reason: displayReasonRunning,
		}},
	}

	got := derivePlayerBusStatus(housePlayer(), PlayerStatus{Activity: playerPlaying, Play: "movie"},
		[]Play{*play}, newPeripheralDesk(), newFocusDesk(nil))

	if got.Play == nil || got.Play.DisplayAlive == nil {
		t.Fatalf("the unit's status carries no display mark: %+v", got)
	}
	mustMatch(t, got.Play.DisplayAlive.Status, conditionTrue)
}
