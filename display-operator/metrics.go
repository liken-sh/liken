package main

// metrics.go holds what milestone 65 requires of every process in the
// organization: the Go runtime's own series, one build_info gauge, and
// the reconcile loop's duration, errors, and watch restarts. All three
// live on one registry, so a scrape of /metrics reads them together.
// displaymetrics.go adds the signals that are this operator's own.
//
// A scrape reads this registry and nothing else. Every Set and Inc
// here happens on a pass that already walked sysfs, wrote a status, or
// watched a socket for its own reason, so recording a reading costs no
// second card, socket, D-Bus, or API call.

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsDeadline bounds a scrape's headers and the listener's stop.
const metricsDeadline = 30 * time.Second

// defaultMetricsAddr is display-operator's port in milestone 65's
// table. The manifest states it in METRICS_ADDR explicitly, so this
// default only matters to a binary run outside the deployment.
const defaultMetricsAddr = ":9210"

// reconcileKind names the resource kind a pass or a watch serves.
// The label is bounded to these four, because milestone 65 forbids a
// label with unbounded cardinality.
type reconcileKind string

const (
	kindResourceSlice reconcileKind = "ResourceSlice"
	kindDisplay       reconcileKind = "Display"
	kindLayout        reconcileKind = "Layout"
	kindPod           reconcileKind = "Pod"
)

// metrics is the registry the listener serves. Layer 1 and layer 2 are
// declared here, under every process's own names; layer 3, this
// operator's domain metrics, is declared in displaymetrics.go on the
// same struct, so one registry answers for all three.
type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	outputConnected    *prometheus.GaugeVec
	outputClaimed      *prometheus.GaugeVec
	observationValid   *prometheus.GaugeVec
	observationSuccess *prometheus.GaugeVec
	outputMode         *prometheus.GaugeVec
	compositorRestarts *prometheus.CounterVec
	surfaces           *prometheus.GaugeVec
	panelPower         *prometheus.GaugeVec
	panelBrightness    *prometheus.GaugeVec
}

// newMetrics builds the registry and states this build's identity on
// it. component and version are the two liken_build_info labels every
// process in the organization carries, so one panel lists every
// release running in the cluster.
func newMetrics(component, version string) *metrics {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "Always 1. The component and version label the release running.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(component, version).Set(1)

	m := &metrics{
		registry: registry,
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "display_reconcile_duration_seconds",
			Help: "How long one pass of the reconcile loop took.",
		}, []string{"kind"}),
		reconcileErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "display_reconcile_errors_total",
			Help: "Passes of the reconcile loop that returned an error.",
		}, []string{"kind"}),
		watchRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "display_watch_restarts_total",
			Help: "Times a watch the API server closed was opened again.",
		}, []string{"kind"}),
	}
	m.newDisplayMetrics()

	registry.MustRegister(buildInfo, m.reconcileDuration, m.reconcileErrors, m.watchRestarts,
		m.outputConnected, m.outputClaimed, m.observationValid, m.observationSuccess,
		m.outputMode, m.compositorRestarts, m.surfaces, m.panelPower, m.panelBrightness)
	return m
}

// reconciled times one pass and counts it if it failed. The caller
// passes the function that does the pass's own work, so a duration
// covers exactly what the pass did and nothing this file adds around
// it.
func (m *metrics) reconciled(kind reconcileKind, pass func() error) error {
	if m == nil {
		return pass()
	}
	start := time.Now()
	err := pass()
	m.reconcileDuration.WithLabelValues(string(kind)).Observe(time.Since(start).Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(string(kind)).Inc()
	}
	return err
}

// watchRestarted counts one watch connection opened after an earlier
// one closed. The first connection of a process is not a restart, so
// the caller of a watch loop counts from its second iteration.
func (m *metrics) watchRestarted(kind reconcileKind) {
	if m == nil {
		return
	}
	m.watchRestarts.WithLabelValues(string(kind)).Inc()
}

// listen opens the address METRICS_ADDR names. An empty address
// serves no metrics, which is every test in this repository and an
// operator a person runs by hand with no listener at all.
func (m *metrics) listen(address string) (net.Listener, error) {
	if address == "" {
		return nil, nil
	}
	return net.Listen("tcp", address)
}

// handler serves the registry at /metrics and nothing else.
func (m *metrics) handler() http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return served
}

// serveMetrics answers on the listener until the run ends. A failure
// here is logged and never fatal: the compositor and the DRA plugin
// are the work this pod exists for, and neither depends on a scrape
// succeeding.
func serveMetrics(ctx context.Context, listener net.Listener, readings *metrics) {
	serving := &http.Server{
		Handler:           readings.handler(),
		ReadHeaderTimeout: metricsDeadline,
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		slog.Warn("the metrics listener stopped", "error", err)
	}
}
