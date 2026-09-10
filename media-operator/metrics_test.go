package main

// These tests prove the metrics milestone 65 and plan 26 ask for: a
// scrape through promhttp answers the layers a fresh registry starts
// with, and a pass wires the reconcile loop, the Play lifecycle, and
// the bus into the series a real scrape reads back.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// scrapeMetrics reads the registry the way Prometheus does: a plain GET
// against the handler promhttp builds, with no test double standing in
// for either end. Both metrics types in this binary embed baseMetrics,
// so this reads either one's registry field directly.
func scrapeMetrics(t *testing.T, registry *prometheus.Registry) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{Registry: registry}).ServeHTTP(recorder, request)
	return recorder.Body.String()
}

// A fresh registry answers layer 1 with no pass ever run: the runtime
// collectors, and this process's own build_info.
func TestAScrapeCarriesLayerOneWithNoPassEverRun(t *testing.T) {
	m := newMediaMetrics("2026.09.10-001")

	body := scrapeMetrics(t, m.registry)

	for _, want := range []string{
		`liken_build_info{component="media-operator",version="2026.09.10-001"} 1`,
		"go_goroutines",
		"process_start_time_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape did not carry %q\n%s", want, body)
		}
	}
}

// A pass with nothing to reconcile still records layer 2: a reconcile
// that changes nothing is still a run, and the operator's own bus
// client answers Connected before the bus ever dials out.
func TestAPassRecordsLayerTwoAndTheBusGaugeWithNothingToReconcile(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.metrics = newMediaMetrics("test")

	media.pass()

	if got := testutil.ToFloat64(media.metrics.busConnected); got != 0 {
		t.Errorf("busConnected = %v, want 0 with no session ever dialed", got)
	}
	if got := testutil.CollectAndCount(media.metrics.reconcileDuration); got == 0 {
		t.Error("reconcileDuration recorded nothing, want at least the Player kind")
	}
	body := scrapeMetrics(t, media.metrics.registry)
	if !strings.Contains(body, `media_reconcile_duration_seconds_count{kind="Player"} 1`) {
		t.Errorf("scrape did not carry the Player reconcile count\n%s", body)
	}
}

// media_playback_starts_total counts the pass a Play first reaches
// Running, and no later pass that leaves it Running counts it again.
func TestPlaybackStartsCountsOnceAndRepeatedPassesLeaveItUnchanged(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.metrics = newMediaMetrics("test")

	// The first pass only creates the pod, so the Play is still Pending.
	media.pass()
	if got := testutil.ToFloat64(media.metrics.playbackStarts); got != 0 {
		t.Errorf("playbackStarts = %v, want 0 while the pod is still Pending", got)
	}

	cluster.pods["movie-playback"].Status.Phase = podRunning
	media.pass()
	if got := testutil.ToFloat64(media.metrics.playbackStarts); got != 1 {
		t.Errorf("playbackStarts = %v, want 1 on the pass that reached Running", got)
	}

	// A repeated pass over the same Running Play must not count a second
	// start: the counter answers "how many, over time", not "how many
	// right now".
	media.pass()
	media.pass()
	if got := testutil.ToFloat64(media.metrics.playbackStarts); got != 1 {
		t.Errorf("playbackStarts = %v, want 1 after passes that changed nothing", got)
	}
}

// media_playback_failures_total carries the fixed reason vocabulary: a
// Play that never reaches a pod fails at setup, and a Play whose pod
// fails is a pod failure.
func TestPlaybackFailuresCategorizesSetupAndPodReasons(t *testing.T) {
	cluster := newFakeCluster()
	// This Play names no Player, so reconcile fails it before any pod
	// exists.
	orphan := housePlay("https://nas/film.mkv")
	orphan.Metadata.Name = "orphan"
	orphan.Spec.Players = nil
	cluster.plays["orphan"] = orphan
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.metrics = newMediaMetrics("test")

	media.pass()
	if got := testutil.ToFloat64(media.metrics.playbackFailures.WithLabelValues(failureReasonSetup)); got != 1 {
		t.Errorf("setup failures = %v, want 1 for the Play with no Player", got)
	}

	cluster.pods["movie-playback"].Status.Phase = podRunning
	media.pass()
	cluster.pods["movie-playback"].Status.Phase = podFailed
	media.pass()
	if got := testutil.ToFloat64(media.metrics.playbackFailures.WithLabelValues(failureReasonPod)); got != 1 {
		t.Errorf("pod failures = %v, want 1 once the running pod fails", got)
	}

	// The orphan stays Failed every pass; it must not recount.
	media.pass()
	if got := testutil.ToFloat64(media.metrics.playbackFailures.WithLabelValues(failureReasonSetup)); got != 1 {
		t.Errorf("setup failures = %v, want 1 after a pass that changed nothing", got)
	}
}

// media_players counts units by zone and state, and a unit that moves
// from one state to another leaves no stale series behind for the
// state it left.
func TestPlayersGaugeReportsZoneAndStateAndDropsStaleBuckets(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.metrics = newMediaMetrics("test")

	media.pass()
	if got := testutil.ToFloat64(media.metrics.players.WithLabelValues("living-room", playerMetricIdle)); got != 1 {
		t.Errorf("idle count = %v, want 1 with no Play running", got)
	}

	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	media.pass()
	cluster.pods["movie-playback"].Status.Phase = podRunning
	media.reports.fold("house", "movie", runningReport())
	media.pass()

	if got := testutil.ToFloat64(media.metrics.players.WithLabelValues("living-room", playerMetricPlaying)); got != 1 {
		t.Errorf("playing count = %v, want 1 once the Play is Running", got)
	}
	if got := testutil.ToFloat64(media.metrics.players.WithLabelValues("living-room", playerMetricIdle)); got != 0 {
		t.Errorf("idle count = %v, want 0: the unit left idle and Reset must drop the stale series", got)
	}

	paused := runningReport()
	paused.Paused = true
	media.reports.fold("house", "movie", paused)
	media.pass()
	if got := testutil.ToFloat64(media.metrics.players.WithLabelValues("living-room", playerMetricPaused)); got != 1 {
		t.Errorf("paused count = %v, want 1 once the report carries paused", got)
	}
}

// The listener setting follows the pattern every other operator
// setting uses: an empty address serves nothing. serve must return
// with no goroutine left trying to bind, which is what every other
// test in this file relies on to run with no network at all.
func TestServeWithNoAddressBindsNoListener(t *testing.T) {
	newMediaMetrics("test").serve("")
}
