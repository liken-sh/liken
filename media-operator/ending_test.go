package main

// What a pass does with an ending report: the label it patches onto the
// playback pod, and the unit's Idle status it publishes ahead of every
// read the status does not depend on. Both are proved against the same
// small API server the other pass tests run on.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// The ending report is what labels the pod, and the label is what the
// compositor fades on. The pass patches it once: the second pass reads
// the same ending and writes nothing, because a pod that carries the
// label needs no second patch.
func TestAFoldedEndingLabelsThePlaybackPodOnce(t *testing.T) {
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

// A film that is still playing is no ending, so the pod carries no
// label and the pass sends no patch. Only the mark on the report labels
// a pod.
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

// A patch the API server refuses leaves the pod unlabeled, so the next
// pass patches it again. The memo records a pod that carries the label,
// never a write that failed.
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

// The browser's return and the room's lights key on the unit reading
// Idle, so a pass publishes that state before it reads anything the
// state does not come from. Each case holds one such request until the
// status has reached the broker. A pass that published the state at the
// end of its work would never reach the publish, and the case would fail
// on the wait rather than pass late.
func TestAnEndingPublishesIdleBeforeTheRestOfThePass(t *testing.T) {
	cases := []struct {
		name    string
		request string
		phase   string
	}{
		{
			name:    "before the pass lists the Remotes",
			request: http.MethodGet + " " + remotesAllPath,
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
			// The held request is on the pass's path, so the publish above
			// happened before it and not in place of it.
			if !slices.Contains(cluster.requests, test.request) {
				t.Errorf("requests = %v, want the pass to reach %q", cluster.requests, test.request)
			}
		})
	}
}

// mustPublishPlayerStatus reads publishes until one unit's status arrives,
// and answers the state it carried. A pass publishes several topics, so a
// test that waits for a unit reads past the rest.
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

// podPatches counts the patches the passes sent to one pod, which is how
// the tests above read what the operator wrote and how often.
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
