package main

// Each test runs whole passes in a synctest bubble, so the recorder's
// queue drains on the bubble's clock.

import (
	"testing"
	"testing/synctest"
	"time"
)

// postedOn returns the Events about one object as "type reason:
// message" strings. synctest.Wait lets the recorder write each queued
// Event first.
func postedOn(cluster *fakeCluster, kind, name string) []string {
	synctest.Wait()
	var posted []string
	for _, event := range cluster.events.About(kind, name) {
		posted = append(posted, event.Type+" "+event.Reason+": "+event.Message)
	}
	return posted
}

// pendingCluster returns a running cluster whose Play is Pending, so
// the next pass changes the phase.
func pendingCluster() *fakeCluster {
	cluster := runningCluster(housePlayer())
	cluster.plays["movie"].Status = PlayStatus{Phase: phasePending, Pod: "movie-playback"}
	return cluster
}

func TestAPlayPostsTheEventOfEachPhaseItMovesTo(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeCluster, *operator)
		want  []string
	}{
		{
			name:  "a pod that runs",
			setup: func(*fakeCluster, *operator) {},
			want:  []string{"Normal PlaybackStarted: playback pod movie-playback runs on player theater"},
		},
		{
			name: "a pod that succeeded",
			setup: func(cluster *fakeCluster, media *operator) {
				cluster.pods["movie-playback"].Status.Phase = podSucceeded
				media.reports.fold("house", "movie", playReport{Item: 2, Position: "0:44:10"})
			},
			want: []string{"Normal PlaybackFinished: finished at item 2, 0:44:10"},
		},
		{
			name: "a pod that failed while the backoff holds the recreate",
			setup: func(cluster *fakeCluster, media *operator) {
				cluster.pods["movie-playback"].Status.Phase = podFailed
				cluster.pods["movie-playback"].Status.Message = "the node ran out of memory"
				media.recreateBackoff[runKey("house", "movie")] = backoffState{
					count: 1, last: time.Now(), next: time.Now().Add(time.Hour),
				}
			},
			want: []string{"Warning PlaybackFailed: the playback pod failed: the node ran out of memory"},
		},
		{
			name: "an item the operator does not resolve",
			setup: func(cluster *fakeCluster, _ *operator) {
				cluster.plays["movie"].Spec.Items = []PlayItem{{URI: "rtsp://camera/front"}}
			},
			want: []string{"Warning InvalidSpec: the scheme rtsp:// is not one the operator resolves, " +
				"which are https://, nfs://, claim://, and pattern://"},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := pendingCluster()
				media := testOperator(t, cluster, make(chan struct{}, 1))
				each.setup(cluster, media)

				media.pass()

				mustMatchAll(t, postedOn(cluster, "Play", "movie"), each.want)
			})
		})
	}
}

func TestASecondPassOverTheSamePhasePostsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := pendingCluster()
		media := testOperator(t, cluster, make(chan struct{}, 1))

		media.pass()
		media.pass()

		mustMatchAll(t, postedOn(cluster, "Play", "movie"), []string{
			"Normal PlaybackStarted: playback pod movie-playback runs on player theater",
		})
	})
}

func TestARecreatedPodPostsWhy(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeCluster)
		want  string
	}{
		{
			name:  "a spec edit on the player",
			setup: func(cluster *fakeCluster) { cluster.players["theater"] = brightPlayer() },
			want: "Normal PodRecreated: recreated playback pod movie-playback at 0:15:00, " +
				"because a spec edit changed its devices on player theater",
		},
		{
			name: "a failed pod",
			setup: func(cluster *fakeCluster) {
				cluster.pods["movie-playback"].Status.Phase = podFailed
				cluster.pods["movie-playback"].Status.Message = "the node ran out of memory"
			},
			want: "Warning PodRecreated: recreated playback pod movie-playback at 0:15:00, " +
				"because the pod failed: the node ran out of memory",
		},
		{
			name:  "a pod that is gone",
			setup: func(cluster *fakeCluster) { delete(cluster.pods, "movie-playback") },
			want: "Warning PodRecreated: created playback pod movie-playback on player theater at 0:15:00, " +
				"because the run had no pod",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := runningCluster(housePlayer())
				each.setup(cluster)
				media := testOperator(t, cluster, make(chan struct{}, 1))
				media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:15:00"})

				media.pass()

				posted := postedOn(cluster, "Play", "movie")
				mustMatch(t, len(posted) > 0, true)
				mustMatch(t, posted[0], each.want)
			})
		})
	}
}

func TestAPodThatKeepsFailingPostsTheBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := runningCluster(housePlayer())
		cluster.pods["movie-playback"].Status.Phase = podFailed
		media := testOperator(t, cluster, make(chan struct{}, 1))
		media.recreateBackoff[runKey("house", "movie")] = backoffState{
			count: 1, last: time.Now().Add(-time.Minute), next: time.Now().Add(-time.Second),
		}

		media.pass()

		mustMatchAll(t, postedOn(cluster, "Play", "movie"), []string{
			"Warning PodRecreated: recreated playback pod movie-playback at the start, because the pod failed",
			"Warning ResumeBackoff: Recreated the playback pod 2 times in a row, so the next recreate waits at least 20s",
		})
	})
}

func TestADeletedPlayPostsOnThePlayAndThePlayer(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*fakeCluster)
		reason  string
		message string
	}{
		{
			name: "a newer play on the same player",
			setup: func(cluster *fakeCluster) {
				cluster.plays["movie"].Metadata.CreationTimestamp = "2026-01-01T00:00:00Z"
				newer := housePlay("https://nas/next.mkv")
				newer.Metadata.Name = "sequel"
				newer.Metadata.CreationTimestamp = "2026-01-02T00:00:00Z"
				cluster.plays["sequel"] = newer
			},
			reason:  reasonSuperseded,
			message: "deleted, because a newer play on player theater replaces it",
		},
		{
			name: "a play past its window",
			setup: func(cluster *fakeCluster) {
				cluster.plays["movie"].Status = PlayStatus{Phase: phaseFinished, FinishedAt: stampAgo(2 * time.Minute)}
				cluster.plays["movie"].Spec.TTLSecondsAfterFinished = ttlSeconds(60)
			},
			reason:  reasonRetired,
			message: "deleted, because 1m0s passed after it finished",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := runningCluster(housePlayer())
				each.setup(cluster)
				media := testOperator(t, cluster, make(chan struct{}, 1))

				media.pass()

				mustMatchAll(t, postedOn(cluster, "Play", "movie"), []string{
					"Normal " + each.reason + ": " + each.message,
				})
				mustMatchAll(t, postedOn(cluster, "Player", "theater"), []string{
					"Normal " + each.reason + ": play movie: " + each.message,
				})
			})
		})
	}
}
