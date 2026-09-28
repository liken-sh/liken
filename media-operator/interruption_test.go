package main

// A run's pod can stop while its Play goes on: a Player edit recreates
// it, mpv crashes and the operator resumes the run, or something else
// deletes the pod, such as an eviction. The new pod keeps the old pod's
// name, so the pod UID on each bus message is what tells the two apart.
// These tests play each case out and read what the unit published.

import (
	"testing"
)

// interruption is one way a run's pod stops while its Play goes on. stop
// runs after the old pod has reported its place, and it leaves the old
// pod on its way out.
type interruption struct {
	name    string
	cluster func() *fakeCluster
	stop    func(t *testing.T, cluster *fakeCluster, media *operator)
	want    []string
}

// oldPodEnds is the old pod's sidecar reporting the ending, which it does
// on every SIGTERM and when mpv closes its socket.
func oldPodEnds(media *operator) {
	media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
		[]byte(`{"item":1,"position":"0:36:01","ended":true,"pod":"old-pod"}`))
}

// activity is the line the unit writes on one published move.
func activity(move string) string {
	return "player house/theater: activity " + move + ", published to " +
		playerStatusTopic(defaultTopicBase, "house", "theater")
}

var interruptions = []interruption{
	{
		// A remote edit on the Player reshapes the pod, so the pass deletes
		// it, and the kubelet's SIGTERM makes the sidecar report the ending.
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
		// mpv exits non-zero. The sidecar reports the ending when the socket
		// closes, and the pod fails. The run is interrupted on the screen,
		// so the unit reads Idle, and the resume is a new start.
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

// interrupt plays one interruption out. The old pod plays and reports,
// stops the way the case says, and goes. The pass then creates the new
// pod under the same name, and the new pod plays and reports.
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

// The unit never goes back to a state it left for the Play. Only the
// crash reads Idle, because the film stopped on the screen.
func TestAnInterruptedRunMovesItsUnitForwardOnly(t *testing.T) {
	for _, each := range interruptions {
		t.Run(each.name, func(t *testing.T) {
			_, _, log := interrupt(t, each)

			mustMatchAll(t, activityLines(log), each.want)
		})
	}
}

// The old pod's ending does not use up the run's ending label, so the new
// pod's own ending labels the new pod, and its fade runs.
func TestAnInterruptedRunLabelsTheNewPodsEnding(t *testing.T) {
	for _, each := range interruptions {
		t.Run(each.name, func(t *testing.T) {
			cluster, media, _ := interrupt(t, each)
			fresh := cluster.pods["movie-playback"]

			media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
				[]byte(`{"item":1,"position":"1:58:03","ended":true,"pod":"`+fresh.Metadata.UID+`"}`))
			media.pass()

			mustMatch(t, fresh.Metadata.Labels[endingLabelKey], endingLabelValue)
		})
	}
}

// The broker sends a dead pod's Last Will once the keepalive runs out,
// which can be after the new pod plays. That offline names the old pod,
// so the new pod's report stands and the unit stays Playing.
func TestALateOfflineFromADeadPodLeavesTheNewPodsReport(t *testing.T) {
	for _, each := range interruptions {
		t.Run(each.name, func(t *testing.T) {
			_, media, log := interrupt(t, each)

			media.handleBusMessage(playAvailabilityTopic(defaultTopicBase, "house", "movie"),
				[]byte("offline old-pod"))
			media.pass()

			mustMatchAll(t, activityLines(log), each.want)
			mustMatch(t, media.reports.latestFor("house", "movie") != nil, true)
		})
	}
}
