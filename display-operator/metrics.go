package main

// A scrape only reads the registry. Each Set and Inc happens in a pass
// that already read sysfs, wrote a status, or watched a socket for its
// own work, so a scrape never reads the card, a socket, D-Bus, or the
// API server.

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

// metricsDeadline is the time a client has to send its request headers.
const metricsDeadline = 30 * time.Second

// defaultMetricsAddr is the metrics port of every liken process. The
// pods use the pod network, so the port does not collide on the node.
// The manifests set METRICS_ADDR too, so the default applies only to a
// binary run outside them.
const defaultMetricsAddr = ":9200"

// reconcileKind is the kind label of the reconcile and watch series.
// It takes only the constants below, so the label's cardinality stays
// bounded.
type reconcileKind string

const (
	kindResourceSlice reconcileKind = "ResourceSlice"
	kindDisplay       reconcileKind = "Display"
	kindLayout        reconcileKind = "Layout"
	kindPod           reconcileKind = "Pod"
)

// metrics is the registry that the listener serves and the series on
// it. newDisplayMetrics in displaymetrics.go creates the series after
// watchRestarts.
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
	// The two compositor series have no label because one pod runs one
	// compositor. The PodMonitor's relabeling adds the node label to
	// every series of this process.
	compositorServing           prometheus.Gauge
	compositorContainerRestarts prometheus.Counter
	surfaces                    *prometheus.GaugeVec
	panelPower                  *prometheus.GaugeVec
	panelBrightness             *prometheus.GaugeVec
}

// newProcessRegistry returns a registry with the Go runtime and
// process collectors and the liken_build_info gauge. Every liken
// process serves liken_build_info with its component and version, so
// one query lists every release that runs in the cluster.
func newProcessRegistry(component, version string) *prometheus.Registry {
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
	registry.MustRegister(buildInfo)
	return registry
}

// newMetrics returns the process registry with this operator's series
// added.
func newMetrics(component, version string) *metrics {
	registry := newProcessRegistry(component, version)

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
			Help: "Times the API server accepted a watch after the first one, for each kind.",
		}, []string{"kind"}),
	}
	m.newDisplayMetrics()

	registry.MustRegister(m.reconcileDuration, m.reconcileErrors, m.watchRestarts,
		m.outputConnected, m.outputClaimed, m.observationValid, m.observationSuccess,
		m.outputMode, m.compositorRestarts, m.compositorServing, m.compositorContainerRestarts,
		m.surfaces, m.panelPower, m.panelBrightness)
	return m
}

// reconciled runs pass, records its duration, counts it when it fails,
// and returns its error. A nil *metrics runs pass and records nothing.
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

// watchRestarted counts a watch that the API server accepted after the
// first one of the same kind. informer.Options.Reopened calls it, and
// does not call it for a refused watch.
func (m *metrics) watchRestarted(kind reconcileKind) {
	if m == nil {
		return
	}
	m.watchRestarts.WithLabelValues(string(kind)).Inc()
}

// listen opens a TCP listener on address. An empty address returns a
// nil listener and no error.
func (m *metrics) listen(address string) (net.Listener, error) {
	if address == "" {
		return nil, nil
	}
	return net.Listen("tcp", address)
}

// handler serves the registry at /metrics and no other path.
func (m *metrics) handler() http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return served
}

// serveMetrics serves scrapes until ctx ends. It logs a failure and
// does not return it, because the compositor and the DRA plugin do not
// depend on metrics.
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
		slog.Warn("The metrics listener stopped", "error", err)
	}
}
