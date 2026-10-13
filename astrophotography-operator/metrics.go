package main

// The operator's half of liken's Prometheus contract (root plan 65,
// plans/completed/65-prometheus-metrics.md at the top of the
// repository): the runtime layer and the reconcile loop layer, on one
// registry that a scrape reads from memory, under the prefix
// astrophotography_. Plan 08 names the domain series, and each piece
// of plans 01, 02, and 03 adds its own with the code that sets it.
//
// A scrape never touches INDI, PHD2, a worker, or the API server. Each
// series is set when the operator reads the fact for its own work.

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// version is the release that this binary was built from. The
// Dockerfile sets it with -ldflags "-X main.version=...", and a build
// with no such flag, such as go test, reports dev.
var version = "dev"

// metricsPrefix is the prefix that root plan 65 gives this operator.
// liken_build_info is the one name that every component shares, so it
// has no prefix.
const metricsPrefix = "astrophotography_"

// reconciledKinds are the kinds that a loop serves: the Targets and
// the Solvers, and the Reservations that the runner drives.
var reconciledKinds = []string{
	astrophotography.TargetKind.Name, astrophotography.SolverKind.Name, observatory.ReservationKind.Name,
}

// reconcileBuckets cover a pass of one loop: a pass that reads the
// stores and changes nothing takes about a millisecond, and a pass
// that writes several statuses takes tens of milliseconds.
var reconcileBuckets = prometheus.ExponentialBuckets(0.001, 4, 8)

// metrics is the registry that the listener serves.
type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	// runner holds the runner's series of layer 3 (runnermetrics.go).
	runner *runnerMetrics
}

// newMetrics builds the registry. release is the release that the
// binary was built from.
func newMetrics(release string) *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metricsPrefix + "reconcile_duration_seconds",
			Help:    "How long one pass of a loop takes, by the resource kind that it serves.",
			Buckets: reconcileBuckets,
		}, []string{"kind"}),
		reconcileErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "reconcile_errors_total",
			Help: "Passes of a loop that failed, by the resource kind that it serves.",
		}, []string{"kind"}),
		watchRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "watch_restarts_total",
			Help: "Watches that the API server closed and the operator opened again, by resource kind.",
		}, []string{"kind"}),
	}

	// Layer 1. The gauge always holds 1, and its labels hold the
	// release, so one panel that reads liken_build_info shows the
	// release of every component in a cluster.
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release each liken component runs, as labels on a gauge that is always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(managedBy, release).Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		m.reconcileDuration, m.reconcileErrors, m.watchRestarts,
	)

	// Each kind publishes its counters at zero from the first scrape.
	// Prometheus computes a rate or an increase from two readings, so a
	// counter that appears only at its first increase hides that
	// increase.
	for _, kind := range reconciledKinds {
		m.reconcileDuration.WithLabelValues(kind)
		m.reconcileErrors.WithLabelValues(kind)
	}
	for _, w := range watches() {
		m.watchRestarts.WithLabelValues(w.kind)
	}
	m.runner = newRunnerMetrics(m.registry)
	return m
}

// observePass records one pass of a loop. Every pass counts, a pass
// that changed nothing too, because the rate of passes shows that the
// loop runs.
func (m *metrics) observePass(kind string, took time.Duration, failed bool) {
	m.reconcileDuration.WithLabelValues(kind).Observe(took.Seconds())
	if failed {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// watchReopened answers the function that counts the reopened watches
// of one kind (informer.Options.Reopened).
func (m *metrics) watchReopened(kind string) func() {
	return m.watchRestarts.WithLabelValues(kind).Inc
}

// handler answers a scrape from the registry.
func (m *metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		// A collector that fails gives a failed scrape, and not a
		// document with a gap in it.
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// listen is net.Listen. A test replaces it with a listener in memory,
// which keeps a synctest bubble's clock moving where a real socket stops
// it.
var listen = net.Listen

// serve opens the /metrics listener and returns at once, with the
// address that the listener holds. An empty address turns the listener
// off. The listen runs in the caller's goroutine, so a port in use is
// an error that the caller reports. The server runs in its own
// goroutine, because the operator's work never waits on a scraper, and
// it stops when ctx ends.
func (m *metrics) serve(ctx context.Context, address string) (net.Addr, error) {
	if address == "" {
		return nil, nil
	}
	listener, err := listen("tcp", address)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.handler())
	server := &http.Server{
		Handler: mux,
		// A client that opens a connection and sends no headers holds a
		// goroutine until this bound. The handler reads memory alone, so
		// the server needs no other timeout.
		ReadHeaderTimeout: 10 * time.Second,
	}
	context.AfterFunc(ctx, func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return listener.Addr(), nil
}
