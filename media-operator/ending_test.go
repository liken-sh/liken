package main

// The fake cluster's request log shows which API requests an ending
// report caused.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

func TestAnEndingReportLabelsThePlaybackPodOnce(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = buildClaim(cluster.plays["movie"], housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reports.fold("house", "movie", playReport{Item: 1, Position: "1:58:03", Ended: true})
	media.pass()
	media.pass()

	mustMatch(t, cluster.pods["movie-playback"].Metadata.Labels[endingLabelKey], endingLabelValue)
	mustMatch(t, podPatches(cluster, "movie-playback"), 1)
}

func TestAReportThatIsNoEndingLabelsNothing(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = buildClaim(cluster.plays["movie"], housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:20:00"})
	media.pass()

	_, labeled := cluster.pods["movie-playback"].Metadata.Labels[endingLabelKey]
	mustMatch(t, labeled, false)
	mustMatch(t, podPatches(cluster, "movie-playback"), 0)
}

func TestAnEndingLabelThatFailsIsPatchedAgain(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = buildClaim(cluster.plays["movie"], housePlayer())
	cluster.podPatchFails = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reports.fold("house", "movie", playReport{Item: 1, Position: "1:58:03", Ended: true})
	media.pass()
	media.pass()

	_, labeled := cluster.pods["movie-playback"].Metadata.Labels[endingLabelKey]
	mustMatch(t, labeled, false)
	mustMatch(t, podPatches(cluster, "movie-playback"), 2)
}

// Each case holds one request that the Idle status does not depend on
// until the status reaches the broker. A pass that published the
// status after that request would block, and the case would fail
// waiting for the status.
func TestAnEndingPublishesIdleBeforeTheRestOfThePass(t *testing.T) {
	cases := []struct {
		name    string
		request string
		phase   string
	}{
		{
			name:    "before the pass writes the running Play",
			request: http.MethodPatch + " " + playPath("house", "movie"),
			phase:   phaseRunning,
		},
		{
			name:    "before the pass deletes the finished run's pod",
			request: http.MethodDelete + " /api/v1/namespaces/house/pods/movie-playback",
			phase:   phaseFinished,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := runningCluster(housePlayer())
				cluster.plays["movie"].Status.Phase = test.phase
				release := cluster.hold(test.request)
				defer release()
				media, broker := busOperator(t, cluster)
				media.reports.fold("house", "movie", playReport{Item: 1, Position: "1:58:03", Ended: true})

				passed := make(chan struct{})
				go func() {
					defer close(passed)
					media.pass()
				}()
				status := mustPublishPlayerStatus(t, broker, "house", "theater")
				release()
				<-passed

				mustMatch(t, status.Activity, playerIdle)
				mustMatch(t, status.Play == nil, true)
				// The status came before the held request, not in place
				// of it.
				if !slices.Contains(cluster.requests, test.request) {
					t.Errorf("requests = %v, want the pass to reach %q", cluster.requests, test.request)
				}
			})
		})
	}
}

func TestAnEndingPublishesIdleFromTheLastPassWithNoRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := runningCluster(housePlayer())
		media, broker := busOperator(t, cluster)
		recordPassLists(media, cluster)

		media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"), endingReport(t))

		status := mustPublishPlayerStatus(t, broker, "house", "theater")
		mustMatch(t, status.Activity, playerIdle)
		mustMatch(t, status.Play == nil, true)
		mustMatchAll(t, cluster.requests, nil)
	})
}

// A restarted operator can receive an ending report before its first
// pass has listed anything.
func TestAnEndingWithNoSnapshotIsLeftToThePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := runningCluster(housePlayer())
		media, broker := busOperator(t, cluster)

		media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"), endingReport(t))

		mustPublishNothingYet(t, broker, media.bus)
		mustMatchAll(t, cluster.requests, nil)

		media.pass()

		status := mustPublishPlayerStatus(t, broker, "house", "theater")
		mustMatch(t, status.Activity, playerIdle)
	})
}

// The sidecar sets the ended flag in every report after the ending,
// and only the first of them publishes a status.
func TestASecondEndingReportPublishesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := runningCluster(housePlayer())
		media, broker := busOperator(t, cluster)
		recordPassLists(media, cluster)
		topic := playStatusTopic(defaultTopicBase, "house", "movie")

		media.handleBusMessage(topic, endingReport(t))
		mustMatch(t, mustPublishPlayerStatus(t, broker, "house", "theater").Activity, playerIdle)

		media.handleBusMessage(topic, endingReport(t))

		mustPublishNothingYet(t, broker, media.bus)
	})
}

// recordPassLists records the lists that a finished pass would have
// recorded, without making its API requests.
func recordPassLists(media *operator, cluster *fakeCluster) {
	media.snapshot.recordPlays([]Play{*cluster.plays["movie"]})
	media.snapshot.recordPlayers([]Player{*cluster.players["theater"]})
}

// endingReport is the message that the command sidecar publishes on a
// run's status topic when the film is over.
func endingReport(t *testing.T) []byte {
	t.Helper()
	payload, err := json.Marshal(playReport{Item: 1, Position: "1:58:03", Ended: true})
	mustSucceed(t, err)
	return payload
}

// mustPublishNothingYet fails when the operator has queued a publish
// that the test has not read. The Bus sends messages in order, so when
// the test's marker is the first message the broker receives, nothing
// was queued before it.
func mustPublishNothingYet(t *testing.T, broker *fakeBroker, bus *Bus) {
	t.Helper()
	const mark = "liken/media/test/nothing-yet"
	bus.Publish(mark, []byte("read me"), false)
	if got := waitForPublish(t, broker.pubs); got.topic != mark {
		t.Errorf("the operator published %q, want nothing", got.topic)
	}
}

// mustPublishPlayerStatus skips publishes to other topics until the
// status of one Player arrives.
func mustPublishPlayerStatus(t *testing.T, broker *fakeBroker, namespace, name string) playerBusStatus {
	t.Helper()
	topic := playerStatusTopic(defaultTopicBase, namespace, name)
	for {
		published := waitForPublish(t, broker.pubs)
		if published.topic != topic {
			continue
		}
		var status playerBusStatus
		mustSucceed(t, json.Unmarshal(published.payload, &status))
		return status
	}
}

func podPatches(cluster *fakeCluster, pod string) int {
	patches := 0
	for _, request := range cluster.requests {
		if strings.HasPrefix(request, http.MethodPatch+" ") &&
			strings.HasSuffix(request, "/pods/"+pod) {
			patches++
		}
	}
	return patches
}
