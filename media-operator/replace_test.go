package main

// These tests run a recreate against a fake API server that keeps a
// deleted pod while its containers stop. However many passes find the old
// pod, the operator sends one delete and creates one new pod.

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

// podLines returns the log lines that report a created or recreated
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

			// The old pod remains while its containers stop, and every pass in
			// that time reads it. Its mpv exits with 4 on SIGTERM, so the pod
			// fails before it is removed.
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

// The terminating pod fails because of the delete, not because playback
// failed, so the Play does not show Failed while the operator waits.
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

// The new pod subscribes to the remotes of the edited Player.
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

// A recreate that changed the claim also waits until the old claim is
// deleted, because a pod that uses a claim that is being deleted never
// starts.
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

// When the API server refuses the delete, the pod is still the run's pod,
// so its report that playback ended still ends the run.
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
