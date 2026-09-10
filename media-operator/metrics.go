package main

// The operator role's Prometheus metrics, and the layer 1 both roles
// this binary can run share. Milestone 65 in the liken repository is
// the contract every process in the organization follows: the three
// layers, the names, the port, and the monitoring component. Plan 26
// in this repository states what this operator adds under the
// contract. commandmetrics.go holds the command sidecar role's own
// series, on top of the baseMetrics this file builds.
//
// One registry per process, so a test builds its own with nothing left
// over from the last one, and a listener that serves it with no other
// path to the registry.
//
// Every series here reads a fact the reconcile loop already derives
// for a Play's or a Player's status.

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// buildInfoComponent is the operator's own name in liken_build_info,
// the one series every process in the organization publishes under its
// own name so a single panel lists every release in the cluster.
// commandMetrics carries the command sidecar's name for the same
// gauge, because the two roles are the one binary and share this file.
const buildInfoComponent = "media-operator"

// The kinds layer 2 labels its three series with, one per collection
// the reconcile loop reads or watches.
const (
	kindPlay             = "Play"
	kindRemote           = "Remote"
	kindPlayer           = "Player"
	kindKeymap           = "Keymap"
	kindMediaPreferences = "MediaPreferences"
	kindPod              = "Pod"
	kindPeripheral       = "Peripheral"
)

// The fixed vocabulary for media_playback_failures_total. A Play that
// never reached a pod, because it named no Player, its items would
// not resolve, or its Remotes would not gather, failed at setup. A
// Play whose pod existed and failed is a pod failure: mpv's own exit,
// or the kubelet's.
const (
	failureReasonSetup = "setup"
	failureReasonPod   = "pod"
)

// The three words media_players reports. A unit with no Play running
// or with one still starting is idle for this gauge's purpose; only a
// Play in the Running phase reads as playing or paused, the same fold
// the Play's own activity performs.
const (
	playerMetricIdle    = "idle"
	playerMetricPlaying = "playing"
	playerMetricPaused  = "paused"
)

// baseMetrics is layer 1: a fresh registry carrying the Go and process
// collectors every process in the organization gets for free, and
// liken_build_info under that process's own component name. The
// operator role and the command role are the one binary, and each
// builds its own series on top of this, so this is the one place layer
// 1 exists rather than twice.
type baseMetrics struct {
	registry *prometheus.Registry
}

// newBaseMetrics registers layer 1 on a fresh registry and reports
// component and version once, at construction, because liken_build_info
// never changes for the life of a process.
func newBaseMetrics(component, version string) baseMetrics {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The component and version this process runs. Always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(component, version).Set(1)
	registry.MustRegister(buildInfo)

	return baseMetrics{registry: registry}
}

// serve starts the metrics listener in the background. An empty
// address turns it off, the setting every process in the organization
// shares. A listener that cannot bind is logged and nothing else: a
// scrape endpoint must never hold up the work the process exists for.
func (b baseMetrics) serve(address string) {
	if address == "" {
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(b.registry, promhttp.HandlerOpts{Registry: b.registry}))
	go func() {
		if err := http.ListenAndServe(address, mux); err != nil {
			fmt.Fprintf(os.Stderr, "metrics listener on %s: %v\n", address, err)
		}
	}()
}

// mediaMetrics holds every series the operator role publishes. The
// pass and the watches write to it directly; nothing here reads the
// API server or the bus, so a scrape never blocks on either.
type mediaMetrics struct {
	baseMetrics

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	players          *prometheus.GaugeVec
	playbackStarts   prometheus.Counter
	playbackFailures *prometheus.CounterVec
	busConnected     prometheus.Gauge
}

// newMediaMetrics builds the operator's metrics on a fresh registry.
// version is what liken_build_info reports; the operator's own image
// tag is the release it runs.
func newMediaMetrics(version string) *mediaMetrics {
	base := newBaseMetrics(buildInfoComponent, version)
	m := &mediaMetrics{
		baseMetrics: base,
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "media_reconcile_duration_seconds",
			Help: "How long one reconcile of one kind took.",
		}, []string{"kind"}),
		reconcileErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "media_reconcile_errors_total",
			Help: "Reconciles that returned an error, by kind.",
		}, []string{"kind"}),
		watchRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "media_watch_restarts_total",
			Help: "Times a watch's stream ended and the operator opened it again, by kind.",
		}, []string{"kind"}),
		players: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "media_players",
			Help: "Players by zone and state: idle, playing, or paused.",
		}, []string{"zone", "state"}),
		playbackStarts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "media_playback_starts_total",
			Help: "Plays that reached the Running phase.",
		}),
		playbackFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "media_playback_failures_total",
			Help: "Plays that reached the Failed phase, by reason.",
		}, []string{"reason"}),
		busConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "media_bus_connected",
			Help: "Whether the operator's own MQTT client holds a live session.",
		}),
	}
	base.registry.MustRegister(
		m.reconcileDuration,
		m.reconcileErrors,
		m.watchRestarts,
		m.players,
		m.playbackStarts,
		m.playbackFailures,
		m.busConnected,
	)
	return m
}

// observeReconcile records one reconcile of one kind: how long it took,
// and whether it returned an error. The pass calls this around each
// kind it reconciles, so a reconcile that changes nothing still counts
// as a run, the way milestone 65 requires.
func (m *mediaMetrics) observeReconcile(kind string, duration time.Duration, err error) {
	m.reconcileDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// notePlaybackPhase folds a Play's phase before and after one
// reconcile into the two lifecycle counters. Both counters are
// edge-triggered: a Play that stays Running or stays Failed across a
// pass counts once, on the pass that changed it, because a status
// write that changes nothing must not inflate a total meant to answer
// "how many, over time".
func (m *mediaMetrics) notePlaybackPhase(previous string, status PlayStatus) {
	if status.Phase == phaseRunning && previous != phaseRunning {
		m.playbackStarts.Inc()
	}
	if status.Phase == phaseFailed && previous != phaseFailed {
		reason := failureReasonPod
		if status.Pod == "" {
			reason = failureReasonSetup
		}
		m.playbackFailures.WithLabelValues(reason).Inc()
	}
}

// boolToFloat is the gauge encoding milestone 65 fixes for a fact that
// is really a boolean: 1 or 0, never anything between.
func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

// watchRestartFunc closes over one kind's counter, so a watch loop that
// knows nothing about Prometheus can still report each reconnect. It
// answers nil when m is nil, which is what lets a test call a watch*
// function with no registry at all.
func watchRestartFunc(m *mediaMetrics, kind string) func() {
	if m == nil {
		return nil
	}
	return func() { m.watchRestarts.WithLabelValues(kind).Inc() }
}
