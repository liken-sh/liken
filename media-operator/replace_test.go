package main

// These tests cover a recreate against an API server that keeps a deleted
// pod while its containers stop: one delete, one new pod, and one line,
// however many passes read the old pod in that window.

import (
	"net/http"
	"strings"
	"testing"
)

// podDeletes counts the deletes the operator sent for the run's pod.
func podDeletes(requests []string) int {
	count := 0
	for _, request := range requests {
		if request == http.MethodDelete+" "+podsPath("house")+"/movie-playback" {
			count++
		}
	}
	return count
}

// podLines keeps the lines that say the operator created or recreated a
// playback pod.
func podLines(log *logBuffer) []string {
	var kept []string
	for _, line := range linesAbout(log, "play house/movie") {
		if strings.Contains(line, "created playback pod") {
			kept = append(kept, line)
		}
	}
	return kept
}

// failedRun is a running Play whose mpv exited non-zero.
func failedRun() *fakeCluster {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Status.Phase = podFailed
	return cluster
}

func TestARecreateWaitsForTheOldPodAndRecreatesOnce(t *testing.T) {
	cases := []struct {
		name    string
		cluster func() *fakeCluster
		want    string
	}{
		{
			name:    "a remote edit on the player",
			cluster: remoteChangeCluster,
			want:    "play house/movie: recreated playback pod movie-playback at 0:36:01, because a spec edit changed its remotes on player theater",
		},
		{
			name:    "a failed pod",
			cluster: failedRun,
			want:    "play house/movie: recreated playback pod movie-playback at 0:36:01, because the pod failed",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := each.cluster()
			cluster.podsLinger["movie-playback"] = true
			media, log := loggingOperator(t, cluster)
			media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:36:01"})

			// The old pod stands while its containers stop, and every pass in
			// that window reads it. Its mpv exits 4 on the SIGTERM, which
			// fails the pod before it goes.
			media.pass()
			media.pass()
			cluster.pods["movie-playback"].Status.Phase = podFailed
			media.pass()
			media.pass()
			delete(cluster.pods, "movie-playback")
			media.pass()
			media.pass()

			mustMatchAll(t, podLines(log), []string{each.want})
			mustMatch(t, podDeletes(cluster.requests), 1)
			mustMatch(t, podStart(cluster.pods["movie-playback"]), "0:36:01")
			mustMatch(t, cluster.pods["movie-playback"].Metadata.DeletionTimestamp, "")
		})
	}
}

// The terminating pod's own failure is the delete, not the run's, so the
// Play never reads Failed while it waits.
func TestTheTerminatingPodDoesNotFailThePlay(t *testing.T) {
	cluster := remoteChangeCluster()
	cluster.podsLinger["movie-playback"] = true
	media, log := loggingOperator(t, cluster)

	media.pass()
	cluster.pods["movie-playback"].Status.Phase = podFailed
	cluster.pods["movie-playback"].Status.Message = "Error (exit code 4)"
	media.pass()

	mustMatchAll(t, linesAbout(log, "play house/movie"), []string{
		"play house/movie: phase Pending, was Running",
	})
}

// The new pod carries the controllers the edited Player names.
func TestTheReplacementCarriesTheEditedRemoteSet(t *testing.T) {
	cluster := remoteChangeCluster()
	cluster.podsLinger["movie-playback"] = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	delete(cluster.pods, "movie-playback")
	media.pass()

	mustMatchAll(t, podRemoteTopics(cluster.pods["movie-playback"]),
		[]string{remoteEventsTopic(defaultTopicBase, "house", "armchair")})
}

// A recreate that changed the claim waits for the old claim to go too,
// because a pod created against a claim on its way out never starts.
func TestAReplacementWaitsForTheOldClaim(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.players["theater"] = brightPlayer()
	cluster.podsLinger["movie-playback"] = true
	cluster.claimsLinger["movie-devices"] = true
	media, log := loggingOperator(t, cluster)
	media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:36:01"})

	media.pass()
	delete(cluster.pods, "movie-playback")
	media.pass()
	_, early := cluster.pods["movie-playback"]
	delete(cluster.claims, "movie-devices")
	media.pass()

	mustMatch(t, early, false)
	mustMatchAll(t, podLines(log), []string{
		"play house/movie: recreated playback pod movie-playback at 0:36:01, because a spec edit changed its devices on player theater",
	})
	mustMatch(t, len(cluster.claims["movie-devices"].Spec.Devices.Config) > 0, true)
}

// A delete the API server refused leaves the pod running as the run's
// pod, so its reports still count and its ending still ends the run.
func TestAPodWhoseDeleteFailedStillEndsTheRun(t *testing.T) {
	cluster := remoteChangeCluster()
	cluster.pods["movie-playback"].Metadata.UID = "old-pod"
	cluster.podDeleteFails = true
	media, _ := loggingOperator(t, cluster)

	media.pass()
	media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
		[]byte(`{"item":1,"position":"1:58:03","ended":true,"pod":"old-pod"}`))

	mustMatch(t, media.reports.endedFor("house", "movie"), true)
}
