package main

// These tests check the fake API server and the fake broker together,
// because the end of a Play writes to both.

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// mustClearPlayTopics waits for the empty retained publishes that clear
// a run's status and availability topics, and skips the other topics
// that a pass publishes.
func mustClearPlayTopics(t *testing.T, broker *fakeBroker, namespace, name string) {
	t.Helper()
	status := playStatusTopic(defaultTopicBase, namespace, name)
	availability := playAvailabilityTopic(defaultTopicBase, namespace, name)
	cleared := map[string]bool{}
	for !cleared[status] || !cleared[availability] {
		published := waitForPublish(t, broker.pubs)
		if published.topic != status && published.topic != availability {
			continue
		}
		if len(published.payload) != 0 || !published.retained {
			t.Errorf("the clear published %+v, want an empty retained payload", published)
			return
		}
		cleared[published.topic] = true
	}
}

func TestSweepClearsTheTopicsOfARunWhosePlayIsGone(t *testing.T) {
	cases := []struct {
		name         string
		play         string
		disconnected bool
		wantCleared  bool
	}{
		{name: "a run whose Play is gone loses both topics", play: "old-film", wantCleared: true},
		{name: "a run whose Play still exists keeps both topics", play: "new-film"},
		{name: "a bus with no session sweeps nothing", play: "old-film", disconnected: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bus, brokers, connected := startBus(t, 1, nil, nil)
				waitForConnect(t, connected)
				broker := brokers[0]
				wake := make(chan struct{}, 1)
				media := &operator{
					topicBase: defaultTopicBase,
					bus:       bus,
					reports:   newReports(wake),
				}

				// The operator records a run when it receives the run's
				// retained availability message after it subscribes.
				media.reports.availability("den", test.play, false, "")
				if test.disconnected {
					waitForDisconnect(t, media.bus, broker)
				}

				media.reclaimPlays(map[string]bool{runKey("den", "new-film"): true})

				if !test.wantCleared {
					select {
					case got := <-broker.pubs:
						t.Fatalf("%s: the sweep published %+v, want no publish", test.name, got)
					case <-time.After(50 * time.Millisecond):
					}
					if test.disconnected && len(media.reports.stale(map[string]bool{})) != 1 {
						t.Error("after a sweep with no bus session, the run is no longer listed as stale, want it still listed because its topics were not cleared")
					}
					return
				}
				mustClearPlayTopics(t, broker, "den", test.play)
				if got := media.reports.stale(map[string]bool{}); len(got) != 0 {
					t.Errorf("after the sweep cleared the topics, the stale runs are %v, want none", got)
				}
			})
		})
	}
}

func TestPassAddsTheFinalizerToEveryPlay(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	held := cluster.plays["movie"].Metadata.Finalizers
	if !reflect.DeepEqual(held, []string{playFinalizer}) {
		t.Errorf("finalizers = %v, want %v", held, []string{playFinalizer})
	}

	// A second pass finds the finalizer and sends no patch.
	before := len(cluster.requests)
	media.pass()
	for _, request := range cluster.requests[before:] {
		if strings.HasPrefix(request, http.MethodPatch+" ") && strings.Contains(request, "/plays/") {
			t.Errorf("the second pass patched a Play that already carries the finalizer: %s", request)
		}
	}
}

// busOperator returns an operator connected to a fake broker, so a test
// can read both a pass's API requests and what it published.
func busOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.bus = bus
	return media, brokers[0]
}

func TestDeletingPlayIsReleasedOnlyOnceItsPodIsGone(t *testing.T) {
	cases := []struct {
		name         string
		lingers      bool
		disconnected bool
		wantPlay     bool
		wantCleared  bool
	}{
		{
			name:     "a pod that is still there keeps the finalizer and clears nothing",
			lingers:  true,
			wantPlay: true,
		},
		{
			name:         "a bus with no session keeps the finalizer and clears nothing",
			disconnected: true,
			wantPlay:     true,
		},
		{
			name:        "a pod that is gone clears both topics and releases the Play",
			wantCleared: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				play := playOnPlayer("movie", "theater", "2026-09-08T10:00:00Z")
				play.Metadata.DeletionTimestamp = "2026-09-08T10:30:00Z"
				play.Metadata.Finalizers = []string{playFinalizer}
				cluster.plays["movie"] = play
				cluster.pods["movie-playback"] = housePlaybackPod()
				cluster.podsLinger["movie-playback"] = test.lingers
				media, broker := busOperator(t, cluster)
				if test.disconnected {
					waitForDisconnect(t, media.bus, broker)
				}

				media.pass()

				if _, exists := cluster.plays["movie"]; exists != test.wantPlay {
					t.Fatalf("the Play exists = %v, want %v", exists, test.wantPlay)
				}
				if !test.wantCleared {
					if held := cluster.plays["movie"].Metadata.Finalizers; !reflect.DeepEqual(held, []string{playFinalizer}) {
						t.Errorf("finalizers = %v, want the operator to keep %q", held, playFinalizer)
					}
					select {
					case got := <-broker.pubs:
						t.Fatalf("the pass published %+v while the playback pod still exists, want no publish", got)
					case <-time.After(50 * time.Millisecond):
					}
					return
				}
				mustClearPlayTopics(t, broker, "house", "movie")
			})
		})
	}
}

// A Play created under the name of a released Play starts with none of
// the earlier run's cached report or ended state.
func TestPlayRecreatedUnderTheSameNameStartsWithNoEarlierReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		leaving := playOnPlayer("movie", "theater", "2026-09-08T10:00:00Z")
		leaving.Metadata.DeletionTimestamp = "2026-09-08T10:30:00Z"
		leaving.Metadata.Finalizers = []string{playFinalizer}
		cluster.plays["movie"] = leaving
		cluster.players["theater"] = housePlayer()
		media, broker := busOperator(t, cluster)
		media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:00:08", Ended: true})

		media.pass()
		mustClearPlayTopics(t, broker, "house", "movie")

		cluster.plays["movie"] = playOnPlayer("movie", "theater", "2026-09-08T11:00:00Z")
		media.pass()

		if media.reports.latestFor("house", "movie") != nil {
			t.Error("the recreated Play has the released run's cached report, want none")
		}
		if media.reports.endedFor("house", "movie") {
			t.Error("the recreated Play is marked ended, want not ended")
		}
		held := cluster.plays["movie"].Metadata.Finalizers
		if !reflect.DeepEqual(held, []string{playFinalizer}) {
			t.Errorf("finalizers = %v, want the recreated Play held by %q", held, playFinalizer)
		}
	})
}

// A release removes only playFinalizer, so another operator's finalizer
// still keeps the Play.
func TestReleaseLeavesAnotherOperatorsFinalizerAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const otherFinalizer = "library.liken.sh/progress"
		cluster := newFakeCluster()
		play := playOnPlayer("movie", "theater", "2026-09-08T10:00:00Z")
		play.Metadata.DeletionTimestamp = "2026-09-08T10:30:00Z"
		play.Metadata.Finalizers = []string{otherFinalizer, playFinalizer}
		cluster.plays["movie"] = play
		media, broker := busOperator(t, cluster)

		media.pass()

		mustClearPlayTopics(t, broker, "house", "movie")
		held, exists := cluster.plays["movie"]
		if !exists {
			t.Fatal("the release removed the other operator's finalizer, want the Play to remain")
		}
		if !reflect.DeepEqual(held.Metadata.Finalizers, []string{otherFinalizer}) {
			t.Errorf("finalizers = %v, want %v", held.Metadata.Finalizers, []string{otherFinalizer})
		}
	})
}

// When a write fails while the finalizer is added or removed, the
// finalizer stays as it was, so the next pass can finish the work.
func TestFailedWriteLeavesTheFinalizerAsItWas(t *testing.T) {
	cases := []struct {
		name     string
		deleting bool
		fails    string
	}{
		{
			name:  "adding the finalizer fails to patch the Play",
			fails: playPath("house", "movie"),
		},
		{
			name:     "the release fails to delete the pod",
			deleting: true,
			fails:    podsPath("house") + "/movie-playback",
		},
		{
			name:     "the release fails to patch the Play",
			deleting: true,
			fails:    playPath("house", "movie"),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				play := playOnPlayer("movie", "theater", "2026-09-08T10:00:00Z")
				if test.deleting {
					play.Metadata.DeletionTimestamp = "2026-09-08T10:30:00Z"
					play.Metadata.Finalizers = []string{playFinalizer}
				}
				cluster.plays["movie"] = play
				cluster.fails[test.fails] = true
				media, _ := busOperator(t, cluster)

				media.pass()

				held, exists := cluster.plays["movie"]
				if !exists {
					t.Fatal("the Play was removed after a failed write, want it to remain")
				}
				if test.deleting && !held.Metadata.hasFinalizer(playFinalizer) {
					t.Errorf("finalizers = %v, want the operator to keep %q",
						held.Metadata.Finalizers, playFinalizer)
				}
			})
		})
	}
}
