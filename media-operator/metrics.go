package main

// This file has the Prometheus metrics of the operator role, and
// baseMetrics, which every role of this binary publishes.
// commandmetrics.go and apimetrics.go add the series of the command
// sidecar and the API server.
//
// Each process builds its own registry in place of the Prometheus
// default registry, so a test starts with no series, and the listener
// serves only the series that this process registered.

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// buildInfoComponent is the component label of liken_build_info for
// the operator role. The other roles of this binary set their own value,
// so a scrape shows which role a process runs.
const buildInfoComponent = "media-operator"

// The values of the kind label on the reconcile and watch series.
const (
	kindPlay             = "Play"
	kindRemote           = "Remote"
	kindPlayer           = "Player"
	kindKeymap           = "Keymap"
	kindMediaPreferences = "MediaPreferences"
	kindPod              = "Pod"
	kindPeripheral       = "Peripheral"
	kindResourceClaim    = "ResourceClaim"
	kindResourceSlice    = "ResourceSlice"
	kindDisplay          = "Display"
	kindReceiver         = "Receiver"
	kindSink             = "Sink"
	kindTelevision       = "Television"
)

// The values of the reason label of media_playback_failures_total. A
// Play that fails before it has a pod, for example because it names no
// Player, counts as setup. A Play that fails after its pod was created
// counts as pod, whether mpv exited or the kubelet failed the pod.
const (
	failureReasonSetup = "setup"
	failureReasonPod   = "pod"
)

// The values of the state label of media_players. A Player counts as
// playing or paused only while its Play is in the Running phase. A
// Player with no Play, or with a Play that is still Pending, counts as
// idle.
const (
	playerMetricIdle    = "idle"
	playerMetricPlaying = "playing"
	playerMetricPaused  = "paused"
)

// baseMetrics is a registry with the Go and process collectors and
// liken_build_info. Each role registers its own series on it.
type baseMetrics struct {
	registry *prometheus.Registry
}

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

// serve starts the metrics listener in the background, and does nothing
// when address is empty. When the listener fails, serve logs the error
// and the process keeps running, because a broken metrics endpoint must
// not stop playback.
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

// mediaMetrics holds the series of the operator role. The reconcile pass
// and the watches write them as they run, so a scrape reads no API
// server or bus and does not wait on either.
type mediaMetrics struct {
	baseMetrics

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	players          *prometheus.GaugeVec
	playbackStarts   prometheus.Counter
	playbackFailures *prometheus.CounterVec
	displayRestarts  *prometheus.CounterVec
	busConnected     prometheus.Gauge
}

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
		displayRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "media_display_restarts_total",
			Help: "Times the kubelet restarted a playback pod's display container, by player.",
		}, []string{"player"}),
		busConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "media_bus_connected",
			Help: "Whether the operator's own MQTT client is connected to the broker.",
		}),
	}
	base.registry.MustRegister(
		m.reconcileDuration,
		m.reconcileErrors,
		m.watchRestarts,
		m.players,
		m.playbackStarts,
		m.playbackFailures,
		m.displayRestarts,
		m.busConnected,
	)
	return m
}

func (m *mediaMetrics) observeReconcile(kind string, duration time.Duration, err error) {
	m.reconcileDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// notePlaybackPhase counts a start or a failure only on the reconcile
// where the Play's phase changes to Running or Failed, so a Play that
// stays in that phase across passes is counted once.
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

func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

// watchRestartFunc returns a function that counts one reopen of the
// watch of kind, so a watch loop needs no Prometheus import. It returns
// nil when m is nil, so a test can run a watch without metrics.
func watchRestartFunc(m *mediaMetrics, kind string) func() {
	if m == nil {
		return nil
	}
	return func() { m.watchRestarts.WithLabelValues(kind).Inc() }
}
