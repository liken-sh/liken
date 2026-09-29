package main

// A unit's activity on the bus moves forward for each Play: a subscriber
// that reads Idle for a Play never reads Playing for it again. The idle
// screen, a remote, and a home automation all act on each move, so a
// move back, even for a few milliseconds, draws a film that is over or
// wakes a room for nothing. These tests run whole passes and read the
// operator's activity lines, which it writes on each published move.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// activityLines keeps the lines about the house unit's activity.
func activityLines(log *logBuffer) []string {
	var kept []string
	for _, line := range linesAbout(log, "player house/theater") {
		if strings.HasPrefix(line, "player house/theater: activity ") {
			kept = append(kept, line)
		}
	}
	return kept
}

// A deleted Play is over for its unit from the delete. The release
// forgets the run's ending mark on the pass that clears its topics, and
// that pass still holds the Play in its list, so the Play must name
// nothing from its deletion mark on, not only while the mark stands.
func TestADeletedPlayNeverTurnsItsPlayerBackToPlaying(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.plays["movie"].Metadata.Finalizers = []string{playFinalizer}
	cluster.podsLinger["movie-playback"] = true
	media, _ := busOperator(t, cluster)
	var log logBuffer
	media.log = &log
	media.pass()

	// A person deletes the Play. The pass deletes its pod, which stays
	// while its containers stop.
	cluster.plays["movie"].Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
	media.pass()

	// The sidecar reports the ending on the pod's SIGTERM.
	media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
		[]byte(`{"item":1,"position":"1:00:00","ended":true}`))

	// The pod is gone, so the pass clears the run's topics and releases
	// the Play, and the pass after it reads the Play gone.
	delete(cluster.pods, "movie-playback")
	media.pass()
	media.pass()

	mustMatch(t, len(cluster.plays), 0)
	mustMatchAll(t, activityLines(&log), []string{
		"player house/theater: activity Idle, was Playing, published to " +
			playerStatusTopic(defaultTopicBase, "house", "theater"),
	})
}

// The store's copy of a Play can be older than the operator's own
// status write, because the watch delivers the write a moment later. A
// pass that derived the unit's activity from that copy would publish
// the phase the Play already left, and then the one the write set.
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

// passWithAnEndingDuringIt runs one pass over a running unit and folds
// the run's ending while the pass works on the unit. The pass derives
// each unit's activity and publishes it at the end of the unit's work,
// and the bus reader publishes Idle the moment an ending arrives. The
// receiver session is a request the pass sends between the two, so the
// test holds it and folds the ending while the pass waits.
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

// An ending that arrives while the pass works on a unit is not
// published over with the Playing the pass derived before it.
func TestAnEndingDuringThePassIsNotPublishedOver(t *testing.T) {
	_, media, log := passWithAnEndingDuringIt(t)
	media.pass()

	mustMatchAll(t, activityLines(log), []string{
		"player house/theater: activity Idle, was Playing, published to " +
			playerStatusTopic(defaultTopicBase, "house", "theater"),
	})
}

// The Player's status and the media_players gauge say the state the
// pass published, not the state it derived before an ending arrived.
func TestAnEndingDuringThePassReachesTheStatusAndTheGauge(t *testing.T) {
	cluster, media, _ := passWithAnEndingDuringIt(t)

	mustMatch(t, cluster.players["theater"].Status.Activity, playerIdle)
	mustMatch(t, testutil.ToFloat64(media.metrics.players.WithLabelValues("living-room", playerMetricIdle)), 1.0)
	mustMatch(t, testutil.ToFloat64(media.metrics.players.WithLabelValues("living-room", playerMetricPlaying)), 0.0)
}
