package main

// The operator's Prometheus metrics. The series of solves, guiding, the
// runner, and centering register on the same registry from their own
// files.
//
// A scrape reads only memory. It never calls INDI, PHD2, a worker, or
// the API server. The operator updates each series when it reads the
// value for its own work.

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

// metricsPrefix starts the name of every series of this operator,
// except liken_build_info, which every liken component exports under
// that one name.
const metricsPrefix = "astrophotography_"

// reconciledKinds are the values of the kind label of the reconcile
// series, one for each kind whose loops call observePass.
var reconciledKinds = []string{
	astrophotography.TargetKind.Name, astrophotography.SolverKind.Name, observatory.ReservationKind.Name,
}

// reconcileBuckets run from 1 ms to about 16 s, in steps of 4x.
var reconcileBuckets = prometheus.ExponentialBuckets(0.001, 4, 8)

type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec
	solve             *solveMetrics
	guiding           *guidingMetrics
	runner            *runnerMetrics
	centering         *centeringMetrics
}

// newMetrics registers every series of the operator. release is the
// version label of liken_build_info.
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

	// The gauge is always 1 and carries the release in a label, so one
	// query of liken_build_info lists the release of every component.
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
	m.solve = newSolveMetrics(m.registry)

	// Each kind exports its counters at zero from the first scrape.
	// Prometheus computes rate() and increase() from two samples, so a
	// counter that first appears with the value 1 hides that increment.
	for _, kind := range reconciledKinds {
		m.reconcileDuration.WithLabelValues(kind)
		m.reconcileErrors.WithLabelValues(kind)
	}
	for _, w := range watches() {
		m.watchRestarts.WithLabelValues(w.kind)
	}
	m.runner = newRunnerMetrics(m.registry)
	m.guiding = newGuidingMetrics(m.registry)
	m.centering = newCenteringMetrics(m.registry)
	return m
}

// observePass records every pass, also one that changed nothing,
// because the rate of passes shows that the loop runs.
func (m *metrics) observePass(kind string, took time.Duration, failed bool) {
	m.reconcileDuration.WithLabelValues(kind).Observe(took.Seconds())
	if failed {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// watchReopened returns the informer.Options.Reopened function for one
// kind.
func (m *metrics) watchReopened(kind string) func() {
	return m.watchRestarts.WithLabelValues(kind).Inc
}

func (m *metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		// A failing collector fails the whole scrape, so a gap in the
		// data is never served as if it were complete.
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// listen is net.Listen. A test replaces it with an in-memory listener,
// because a real socket blocks the fake clock of a synctest bubble.
var listen = net.Listen

// serve starts the /metrics server and returns the address it listens
// on, or nil when address is empty. It binds the port before it
// returns, so the caller gets the error of a port that is in use. The
// server stops when ctx ends.
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
		// Without this, a client that connects and sends nothing holds a
		// goroutine forever. The handler only reads memory, so no other
		// timeout is needed.
		ReadHeaderTimeout: 10 * time.Second,
	}
	context.AfterFunc(ctx, func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return listener.Addr(), nil
}
