package main

// For each Play, the activity that the operator publishes for its
// Player only moves forward: after a subscriber reads Idle for a Play,
// it never reads Playing for that Play again. The idle screen, a
// remote, and a home automation act on every change, so even a brief
// return to Playing draws the screen of a film that is over, or turns
// on the lights for nothing. These tests run whole passes and read the
// line that the operator logs each time it publishes an activity.

import (
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// activityLines returns the log lines about the activity of the
// `house/theater` Player.
func activityLines(log *logBuffer) []string {
	var kept []string
	for _, line := range linesAbout(log, "player house/theater") {
		if strings.HasPrefix(line, "player house/theater: activity ") {
			kept = append(kept, line)
		}
	}
	return kept
}

// A deleted Play is over for its Player once its deletion timestamp is
// set. The pass that clears the Play's topics also forgets that the run
// reported its end, and that pass still lists the Play. So the Play
// must not make its Player Playing again, before or after that pass.
func TestADeletedPlayNeverTurnsItsPlayerBackToPlaying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := runningCluster(housePlayer())
		cluster.plays["movie"].Metadata.Finalizers = []string{playFinalizer}
		cluster.podsLinger["movie-playback"] = true
		media, _ := busOperator(t, cluster)
		var log logBuffer
		media.log = &log
		media.pass()

		// A person deletes the Play. The pass deletes its pod, which
		// remains while its containers stop.
		cluster.plays["movie"].Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
		media.pass()

		// The sidecar sends the end report when the pod receives SIGTERM.
		media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
			[]byte(`{"item":1,"position":"1:00:00","ended":true}`))

		// The pod is gone, so the pass clears the run's topics and
		// removes the finalizer, and the next pass finds the Play gone.
		delete(cluster.pods, "movie-playback")
		media.pass()
		media.pass()

		mustMatch(t, len(cluster.plays), 0)
		mustMatchAll(t, activityLines(&log), []string{
			"player house/theater: activity Idle, was Playing, published to " +
				playerStatusTopic(defaultTopicBase, "house", "theater"),
		})
	})
}

// The store's copy of a Play can be older than the operator's own
// status write, because the watch delivers the write a moment later. A
// pass that derived the Player's activity from that copy would publish
// the phase the Play already left, and then the phase the write set.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	starting := func() *fakeCluster {
		cluster := runningCluster(housePlayer())
		cluster.plays["movie"].Status = PlayStatus{Phase: phasePending, Activity: activityStarting}
		return cluster
	}
	cluster := starting()
	media, log := loggingOperator(t, cluster)

	// The pod runs, so the pass writes the Play Running.
	media.pass()
	mustMatch(t, cluster.plays["movie"].Status.Phase, phaseRunning)

	// The watch has not delivered that write, so the store still holds
	// the copy the write replaced.
	media.view.plays.View.Store = starting().view().plays.View.Store
	media.pass()

	mustMatchAll(t, activityLines(log), []string{
		"player house/theater: activity Playing, was Starting, play movie, published to " +
			playerStatusTopic(defaultTopicBase, "house", "theater"),
	})
}

// passWithAnEndingDuringIt runs one pass over a playing Player and
// delivers the run's end report while the pass works on that Player.
// The pass derives the Player's activity first and publishes it last,
// and the bus reader publishes Idle as soon as an end report arrives.
// Between those steps the pass sends a PATCH to the Receiver's status,
// so the test holds that request and delivers the end report while the
// pass waits.
func passWithAnEndingDuringIt(t *testing.T) (*fakeCluster, *operator, *logBuffer) {
	t.Helper()
	cluster := runningCluster(housePlayer())
	screen := screenCluster()
	cluster.claims[idleClaimName("theater")] = screen.claims[idleClaimName("theater")]
	cluster.slices = screen.slices
	cluster.displays = screen.displays
	cluster.receivers["den-receiver"] = houseReceiver()
	session := http.MethodPatch + " " + receiversPath + "/den-receiver/status"
	release := cluster.hold(session)
	defer release()
	cluster.arrived = make(chan string, 1)
	media, _ := busOperator(t, cluster)
	media.metrics = newMediaMetrics("test")
	var log logBuffer
	media.log = &log

	passed := make(chan struct{})
	go func() {
		defer close(passed)
		media.pass()
	}()
	select {
	case arrived := <-cluster.arrived:
		mustMatch(t, arrived, session)
	case <-time.After(busTestTimeout):
		t.Fatal("the pass never sent the session request")
	}
	media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
		[]byte(`{"item":1,"position":"1:58:03","ended":true}`))
	release()
	select {
	case <-passed:
	case <-time.After(busTestTimeout):
		t.Fatal("the pass did not finish after the release")
	}
	return cluster, media, &log
}

// An end report that arrives during a pass is not overwritten by the
// Playing that the pass derived before it.
func TestAnEndingDuringThePassIsNotPublishedOver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, media, log := passWithAnEndingDuringIt(t)
		media.pass()

		mustMatchAll(t, activityLines(log), []string{
			"player house/theater: activity Idle, was Playing, published to " +
				playerStatusTopic(defaultTopicBase, "house", "theater"),
		})
	})
}

// The Player's status and the media_players gauge show the published
// activity, not the activity that the pass derived before the end
// report arrived.
func TestAnEndingDuringThePassReachesTheStatusAndTheGauge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster, media, _ := passWithAnEndingDuringIt(t)

		mustMatch(t, cluster.players["theater"].Status.Activity, playerIdle)
		mustMatch(t, testutil.ToFloat64(media.metrics.players.WithLabelValues("living-room", playerMetricIdle)), 1.0)
		mustMatch(t, testutil.ToFloat64(media.metrics.players.WithLabelValues("living-room", playerMetricPlaying)), 0.0)
	})
}
