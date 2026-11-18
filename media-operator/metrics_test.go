package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// scrapeMetrics returns what a Prometheus scrape of registry reads.
func scrapeMetrics(t *testing.T, registry *prometheus.Registry) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{Registry: registry}).ServeHTTP(recorder, request)
	return recorder.Body.String()
}

func TestANewRegistryHasTheBaseSeries(t *testing.T) {
	m := newMediaMetrics("2026.09.10-001")

	body := scrapeMetrics(t, m.registry)

	for _, want := range []string{
		`liken_build_info{component="media-operator",version="2026.09.10-001"} 1`,
		"go_goroutines",
		"process_start_time_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the scrape is missing %q\n%s", want, body)
		}
	}
}

// A pass that changes nothing still records a reconcile and sets
// media_bus_connected.
func TestAPassThatChangesNothingRecordsReconcilesAndTheBusGauge(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.metrics = newMediaMetrics("test")

	media.pass()

	if got := testutil.ToFloat64(media.metrics.busConnected); got != 0 {
		t.Errorf("busConnected = %v, want 0 with no session ever dialed", got)
	}
	if got := testutil.CollectAndCount(media.metrics.reconcileDuration); got == 0 {
		t.Error("reconcileDuration has no series, want one for the Player kind")
	}
	body := scrapeMetrics(t, media.metrics.registry)
	if !strings.Contains(body, `media_reconcile_duration_seconds_count{kind="Player"} 1`) {
		t.Errorf("the scrape is missing the Player reconcile count\n%s", body)
	}
}

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

	media.pass()
	media.pass()
	if got := testutil.ToFloat64(media.metrics.playbackStarts); got != 1 {
		t.Errorf("playbackStarts = %v, want 1 after passes that changed nothing", got)
	}
}

func TestPlaybackFailuresCategorizesSetupAndPodReasons(t *testing.T) {
	cluster := newFakeCluster()
	// This Play names no Player, so reconcile fails it before it creates
	// a pod.
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

	media.pass()
	if got := testutil.ToFloat64(media.metrics.playbackFailures.WithLabelValues(failureReasonSetup)); got != 1 {
		t.Errorf("setup failures = %v, want 1 after a pass that changed nothing", got)
	}
}

// When a Player changes state, the pass drops the series of its old
// state.
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

// With an empty address, serve starts no listener, so the other tests in
// this file run without binding a port.
func TestServeWithNoAddressBindsNoListener(t *testing.T) {
	newMediaMetrics("test").serve("")
}
