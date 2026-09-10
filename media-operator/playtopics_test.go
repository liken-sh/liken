package main

// These tests run whole passes over a Play's ending: the finalizer going
// on, the release waiting for the pod, the two clears, and the sweep
// behind them. Each one reads the fake API server and the fake broker
// together, because the act this file proves is one write to each.

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// mustClearPlayTopics reads the two empty retained publishes one run's clear
// makes. A whole pass publishes each unit's presentable state as well, so
// this reads past every topic that is not one of the run's two.
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

// The sweep behind the finalizer clears the retained topics of a run whose
// Play the API server no longer holds, and passes over a run whose Play is
// still there.
func TestTheSweepClearsTheTopicsOfARunWhosePlayIsGone(t *testing.T) {
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
			bus, brokers, connected := startBus(t, 1, nil, nil)
			waitForConnect(t, connected)
			broker := brokers[0]
			wake := make(chan struct{}, 1)
			media := &operator{
				topicBase: defaultTopicBase,
				bus:       bus,
				reports:   newReports(wake),
			}

			// The desk has seen the run, the way the broker's retained
			// availability marks it seen the moment the operator subscribes.
			media.reports.availability("den", test.play, false)
			if test.disconnected {
				waitForDisconnect(t, media.bus, broker)
			}

			media.reclaimPlays(map[string]bool{runKey("den", "new-film"): true})

			if !test.wantCleared {
				select {
				case got := <-broker.pubs:
					t.Fatalf("the sweep published %+v for a run it must leave alone: %s", got, test.name)
				case <-time.After(50 * time.Millisecond):
				}
				if test.disconnected && len(media.reports.stale(map[string]bool{})) != 1 {
					t.Error("the sweep forgot a run whose clear it never published")
				}
				return
			}
			mustClearPlayTopics(t, broker, "den", test.play)
			if got := media.reports.stale(map[string]bool{}); len(got) != 0 {
				t.Errorf("the desk still offers %v after the sweep cleared it", got)
			}
		})
	}
}

// The operator's own finalizer goes on every Play the first pass reads.
// It is what keeps the Play until the run's retained topics are cleared.
func TestAPassHoldsEveryPlayWithTheFinalizer(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	held := cluster.plays["movie"].Metadata.Finalizers
	if !reflect.DeepEqual(held, []string{playFinalizer}) {
		t.Errorf("finalizers = %v, want %v", held, []string{playFinalizer})
	}

	// A second pass reads a Play that already carries it and writes
	// nothing, so the operator does not patch the same Play every pass.
	before := len(cluster.requests)
	media.pass()
	for _, request := range cluster.requests[before:] {
		if strings.HasPrefix(request, http.MethodPatch+" ") && strings.Contains(request, "/plays/") {
			t.Errorf("the second pass patched a Play that already carries the finalizer: %s", request)
		}
	}
}

// busOperator wires a pass's operator to a fake broker, so a test reads the
// whole pass and what it published in one place.
func busOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.bus = bus
	return media, brokers[0]
}

// A deleting Play keeps the finalizer while its pod is still there. The
// pod's sidecar publishes its own closing messages as it exits, so a clear
// the operator published first would be overwritten.
func TestADeletingPlayIsReleasedOnlyOnceItsPodIsGone(t *testing.T) {
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

			if _, standing := cluster.plays["movie"]; standing != test.wantPlay {
				t.Fatalf("the Play is still in the collection = %v, want %v", standing, test.wantPlay)
			}
			if !test.wantCleared {
				if held := cluster.plays["movie"].Metadata.Finalizers; !reflect.DeepEqual(held, []string{playFinalizer}) {
					t.Errorf("finalizers = %v, want the operator to keep %q", held, playFinalizer)
				}
				select {
				case got := <-broker.pubs:
					t.Fatalf("the pass published %+v while the pod was still there", got)
				case <-time.After(50 * time.Millisecond):
				}
				return
			}
			mustClearPlayTopics(t, broker, "house", "movie")
		})
	}
}

// A Play created under the name of one the operator just released reports
// a run of its own, so nothing the earlier run left on the desk reads as
// this run's state.
func TestAPlayRecreatedUnderTheSameNameStartsClean(t *testing.T) {
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
		t.Error("the recreated Play reads the released run's report")
	}
	if media.reports.endedFor("house", "movie") {
		t.Error("the recreated Play starts ended")
	}
	held := cluster.plays["movie"].Metadata.Finalizers
	if !reflect.DeepEqual(held, []string{playFinalizer}) {
		t.Errorf("finalizers = %v, want the recreated Play held by %q", held, playFinalizer)
	}
}

// This operator removes only its own finalizer, so the library operator's
// finalizer on the same Play keeps the Play until that operator's own work
// is done.
func TestAReleaseLeavesAnotherOperatorsFinalizerAlone(t *testing.T) {
	const otherFinalizer = "library.liken.sh/progress"
	cluster := newFakeCluster()
	play := playOnPlayer("movie", "theater", "2026-09-08T10:00:00Z")
	play.Metadata.DeletionTimestamp = "2026-09-08T10:30:00Z"
	play.Metadata.Finalizers = []string{otherFinalizer, playFinalizer}
	cluster.plays["movie"] = play
	media, broker := busOperator(t, cluster)

	media.pass()

	mustClearPlayTopics(t, broker, "house", "movie")
	held, standing := cluster.plays["movie"]
	if !standing {
		t.Fatal("the release took the other operator's finalizer with its own")
	}
	if !reflect.DeepEqual(held.Metadata.Finalizers, []string{otherFinalizer}) {
		t.Errorf("finalizers = %v, want %v", held.Metadata.Finalizers, []string{otherFinalizer})
	}
}

// A failed write anywhere on the two paths leaves the finalizer where it
// is, so the next pass reads the Play again and finishes the work then.
func TestAFailedWriteLeavesAPlayHeld(t *testing.T) {
	cases := []struct {
		name     string
		deleting bool
		fails    string
	}{
		{
			name:  "the hold cannot patch the Play",
			fails: playPath("house", "movie"),
		},
		{
			name:     "the release cannot delete the pod",
			deleting: true,
			fails:    podsPath("house") + "/movie-playback",
		},
		{
			name:     "the release cannot patch the Play",
			deleting: true,
			fails:    playPath("house", "movie"),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
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

			held, standing := cluster.plays["movie"]
			if !standing {
				t.Fatal("a failed write released the Play")
			}
			if test.deleting && !held.Metadata.holds(playFinalizer) {
				t.Errorf("finalizers = %v, want the operator to keep %q",
					held.Metadata.Finalizers, playFinalizer)
			}
		})
	}
}
