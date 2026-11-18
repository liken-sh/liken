package main

// A Play's pod can stop while the Play continues: a Player edit
// recreates the pod, mpv crashes and the operator starts the run again,
// or something else, such as an eviction, deletes the pod. The new pod
// has the old pod's name, so only the pod UID in each bus message tells
// the two apart.

import (
	"testing"
	"testing/synctest"
)

// stop runs after the old pod has reported its position.
type interruption struct {
	name    string
	cluster func() *fakeCluster
	stop    func(t *testing.T, cluster *fakeCluster, media *operator)
	want    []string
}

// The sidecar sends the ended report on SIGTERM and when mpv closes its
// socket.
func oldPodEnds(media *operator) {
	media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
		[]byte(`{"item":1,"position":"0:36:01","ended":true,"pod":"old-pod"}`))
}

func activity(move string) string {
	return "player house/theater: activity " + move + ", published to " +
		playerStatusTopic(defaultTopicBase, "house", "theater")
}

var interruptions = []interruption{
	{
		// The Player now names a different remote, which changes the pod
		// spec, so the pass deletes the pod. The kubelet's SIGTERM makes
		// the sidecar report the ending.
		name:    "a recreate after a Player edit",
		cluster: remoteChangeCluster,
		stop: func(_ *testing.T, _ *fakeCluster, media *operator) {
			media.pass()
			oldPodEnds(media)
			media.pass()
		},
		want: []string{
			activity("Starting, was Playing, play movie"),
			activity("Playing, was Starting, play movie"),
		},
	},
	{
		// mpv exits non-zero, so the sidecar reports the ending and the
		// pod fails. Playback stopped on the screen, so the Player shows
		// Idle before the run starts again.
		name:    "an mpv crash",
		cluster: func() *fakeCluster { return runningCluster(housePlayer()) },
		stop: func(_ *testing.T, cluster *fakeCluster, media *operator) {
			oldPodEnds(media)
			cluster.pods["movie-playback"].Status.Phase = podFailed
			media.pass()
		},
		want: []string{
			activity("Idle, was Playing"),
			activity("Starting, was Idle, play movie"),
			activity("Playing, was Starting, play movie"),
		},
	},
	{
		// Something other than this operator deletes the pod, such as the
		// taint manager, and the SIGTERM makes the sidecar report the ending.
		name:    "an eviction",
		cluster: func() *fakeCluster { return runningCluster(housePlayer()) },
		stop: func(_ *testing.T, cluster *fakeCluster, media *operator) {
			cluster.pods["movie-playback"].Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
			media.pass()
			oldPodEnds(media)
			media.pass()
		},
		want: []string{
			activity("Starting, was Playing, play movie"),
			activity("Playing, was Starting, play movie"),
		},
	},
}

// interrupt lets the old pod report, stops it the case's way, deletes
// it, and lets the new pod of the same name report.
func interrupt(t *testing.T, each interruption) (*fakeCluster, *operator, *logBuffer) {
	t.Helper()
	cluster := each.cluster()
	cluster.pods["movie-playback"].Metadata.UID = "old-pod"
	cluster.podsLinger["movie-playback"] = true
	media, _ := busOperator(t, cluster)
	var log logBuffer
	media.log = &log
	status := playStatusTopic(defaultTopicBase, "house", "movie")
	media.handleBusMessage(status, []byte(`{"item":1,"position":"0:36:01","pod":"old-pod"}`))
	media.pass()

	each.stop(t, cluster, media)

	delete(cluster.pods, "movie-playback")
	media.pass()
	fresh, created := cluster.pods["movie-playback"]
	if !created {
		t.Fatal("the pass created no new pod")
	}
	fresh.Status.Phase = podRunning
	media.handleBusMessage(status,
		[]byte(`{"item":1,"position":"0:36:02","pod":"`+fresh.Metadata.UID+`"}`))
	media.pass()
	return cluster, media, &log
}

// The Player's activity never goes back to a state it already left for
// the Play.
func TestAnInterruptedRunMovesItsUnitForwardOnly(t *testing.T) {
	for _, each := range interruptions {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				_, _, log := interrupt(t, each)

				mustMatchAll(t, activityLines(log), each.want)
			})
		})
	}
}

// The old pod's ended report must not set the ending label, so that the
// new pod's own ended report labels the new pod and its fade runs.
func TestAnInterruptedRunLabelsTheNewPodsEnding(t *testing.T) {
	for _, each := range interruptions {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster, media, _ := interrupt(t, each)
				fresh := cluster.pods["movie-playback"]

				media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
					[]byte(`{"item":1,"position":"1:58:03","ended":true,"pod":"`+fresh.Metadata.UID+`"}`))
				media.pass()

				mustMatch(t, fresh.Metadata.Labels[endingLabelKey], endingLabelValue)
			})
		})
	}
}

// The broker publishes a dead pod's Last Will when its keepalive
// expires, which can be after the new pod is playing. The offline
// message names the old pod's UID, so the new pod's report stays.
func TestALateOfflineFromADeadPodLeavesTheNewPodsReport(t *testing.T) {
	for _, each := range interruptions {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				_, media, log := interrupt(t, each)

				media.handleBusMessage(playAvailabilityTopic(defaultTopicBase, "house", "movie"),
					[]byte("offline old-pod"))
				media.pass()

				mustMatchAll(t, activityLines(log), each.want)
				mustMatch(t, media.reports.latestFor("house", "movie") != nil, true)
			})
		})
	}
}
