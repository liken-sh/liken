package main

// The ending label as a pass leaves it, proved against the same small
// API server the other pass tests run on.

import (
	"net/http"
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
