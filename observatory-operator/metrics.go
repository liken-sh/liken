package main

// The operator's half of liken's Prometheus contract (root plan 65,
// plans/completed/65-prometheus-metrics.md at the top of the
// repository, and this operator's plan 22): the runtime layer, the
// reconcile loop layer, and the domain series, on one registry that a
// scrape reads from memory. This file holds the registry, the
// listener, and the first two layers. series.go sets the domain
// series of one namespace.
//
// A scrape never touches INDI, PHD2, or the API server. Each series is
// set when the operator reads the fact for its own work: the status
// writer's composition of a status, a runner's step, an INDI event, or
// a PHD2 event.

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// version is the release that this binary was built from. The
// Dockerfile sets it with -ldflags "-X main.version=...", and a build
// with no such flag, such as go test, reports dev.
var version = "dev"

// metricsPrefix is the prefix that root plan 65 gives this operator.
// liken_build_info is the one name that every component shares, so it
// has no prefix.
const metricsPrefix = "observatory_"

// metricsComponent is the value of liken_build_info's component label.
const metricsComponent = "observatory-operator"

// The kinds of the watches that follow the operator's own objects. The
// watches of the group are labeled with the name of their kind.
var ownedKinds = []string{"Pod", "Service", "ConfigMap", "Job"}

// reconcileBuckets cover a pass of one loop over one namespace: a pass
// that reads the stores and changes nothing takes about a millisecond,
// and a pass that writes several statuses takes tens of milliseconds.
var reconcileBuckets = prometheus.ExponentialBuckets(0.001, 4, 8)

// activationBuckets cover an activation from its first step to Ready:
// about half a minute for the simulators, and up to the 20 minutes of
// a step's longest deadline for a camera that cools or a dome that
// turns.
var activationBuckets = []float64{10, 30, 60, 120, 300, 600, 1200, 2400}

// metrics is the registry that the listener serves. One process builds
// one, and each namespace's operator sets its series through a
// namespaceSeries (series.go).
type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	deviceConnected    *prometheus.GaugeVec
	deviceClaimed      *prometheus.GaugeVec
	deviceDisconnects  *prometheus.CounterVec
	observationValid   *prometheus.GaugeVec
	observationSuccess *prometheus.GaugeVec
	telescopePhase     *prometheus.GaugeVec
	activationDuration *prometheus.HistogramVec
	stepFailures       *prometheus.CounterVec
	cameraTemperature  *prometheus.GaugeVec
	cameraCoolerPower  *prometheus.GaugeVec
	guidingError       *prometheus.GaugeVec
	guiderState        *prometheus.GaugeVec
}

// newMetrics builds the registry. release is the release that the
// binary was built from.
func newMetrics(release string) *metrics {
	gauge := func(name, help string, labels ...string) *prometheus.GaugeVec {
		return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: metricsPrefix + name, Help: help}, labels)
	}
	counter := func(name, help string, labels ...string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{Name: metricsPrefix + name, Help: help}, labels)
	}
	m := &metrics{
		registry: prometheus.NewRegistry(),
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: metricsPrefix + "reconcile_duration_seconds", Help: "How long one pass of a loop takes, by the resource kind that it serves.",
			Buckets: reconcileBuckets,
		}, []string{"kind"}),
		reconcileErrors: counter("reconcile_errors_total", "Passes of a loop that failed, by the resource kind that it serves.", "kind"),
		watchRestarts:   counter("watch_restarts_total", "Watches that the API server closed and the operator opened again, by resource kind.", "kind"),

		deviceConnected:   gauge("device_connected", "One while the device's driver reports CONNECTION on, zero otherwise.", "namespace", "kind", "name"),
		deviceClaimed:     gauge("device_claimed", "One while the session of a reservation holds the device, zero otherwise.", "namespace", "kind", "name"),
		deviceDisconnects: counter("device_disconnects_total", "Drivers that dropped their connection while a session held the device.", "namespace", "kind", "name"),
		observationValid:  gauge("observation_valid", "One while the operator has a connection to the INDI server or the PHD2 of the source, zero otherwise.", "namespace", "source"),
		observationSuccess: gauge("observation_last_success_timestamp_seconds",
			"When the source last sent a message on an open connection, in seconds since the epoch.", "namespace", "source"),
		telescopePhase: gauge("telescope_phase", "One for the phase of the telescope's reservation, or Idle, and zero for every other phase.", "namespace", "telescope", "phase"),
		activationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: metricsPrefix + "activation_duration_seconds", Help: "How long an activation took, from its first step after Wait to Ready.",
			Buckets: activationBuckets,
		}, []string{"namespace", "telescope"}),
		stepFailures:      counter("step_failures_total", "Steps of a reservation that failed, by the step's name.", "namespace", "telescope", "step"),
		cameraTemperature: gauge("camera_temperature_celsius", "The temperature that the camera's driver reports in CCD_TEMPERATURE.", "namespace", "name"),
		cameraCoolerPower: gauge("camera_cooler_power_ratio", "The power that the camera's cooler draws, as a fraction of its greatest power.", "namespace", "name"),
		guidingError:      gauge("guiding_error_arcseconds", "The RMS guiding error that PHD2 reports, by axis: ra, dec, and total.", "namespace", "guider", "axis"),
		guiderState:       gauge("guider_state", "One for the state that PHD2 reports, and zero for every other state.", "namespace", "guider", "state"),
	}

	// Layer 1. The gauge always holds 1, and its labels hold the
	// release, so one panel that reads liken_build_info shows the
	// release of every component in a cluster.
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release each liken component runs, as labels on a gauge that is always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(metricsComponent, release).Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		m.reconcileDuration, m.reconcileErrors, m.watchRestarts,
		m.deviceConnected, m.deviceClaimed, m.deviceDisconnects,
		m.observationValid, m.observationSuccess,
		m.telescopePhase, m.activationDuration, m.stepFailures,
		m.cameraTemperature, m.cameraCoolerPower,
		m.guidingError, m.guiderState,
	)

	// Each kind publishes its counters at zero from the first scrape.
	// Prometheus computes a rate or an increase from two readings, so a
	// counter that appears only at its first increase hides that
	// increase.
	for _, kind := range observatory.Kinds {
		m.reconcileDuration.WithLabelValues(kind.Name)
		m.reconcileErrors.WithLabelValues(kind.Name)
		m.watchRestarts.WithLabelValues(kind.Name)
	}
	for _, kind := range ownedKinds {
		m.watchRestarts.WithLabelValues(kind)
	}
	return m
}

// Layer 2 labels each loop with the kind of the resource that it
// serves:
//
//   - The supervisor counts under Reservation. Its pass starts the
//     runner of each Reservation and stops the servers that no
//     Reservation holds.
//   - The status writer counts under the kind of each status that it
//     writes, with the time that it spent on the objects of that kind.
//     It writes the status of every kind except Reservation, whose
//     runner writes its own.
//   - The lock relay counts under Observatory, because it relays the
//     park locks of each Observatory and sets its LocksRelayed
//     condition.
//   - The trigger controller counts under the kind of each resource
//     whose spec.triggers it reads.
//
// A runner runs steps, and has no pass to time. The activation
// duration and the step failures measure its work.

// passTimes adds up one pass of a loop that serves several kinds, by
// kind.
type passTimes struct {
	took   map[string]time.Duration
	failed map[string]bool
	// timing and began are the kind and the start of the resource that
	// start began to time, until stop.
	timing *observatory.Kind
	began  time.Time
}

// start times one resource of a kind, and stops the time of the one
// before. A loop whose body can continue calls start at the top of
// each iteration, and stop after the loop.
func (p *passTimes) start(kind observatory.Kind) {
	p.stop()
	p.timing, p.began = &kind, time.Now()
}

// stop adds the time of the resource that start timed.
func (p *passTimes) stop() {
	if p.timing != nil {
		p.add(*p.timing, p.began, false)
		p.timing = nil
	}
}

func newPassTimes() *passTimes {
	return &passTimes{took: map[string]time.Duration{}, failed: map[string]bool{}}
}

// add counts the time since began against kind.
func (p *passTimes) add(kind observatory.Kind, began time.Time, failed bool) {
	p.took[kind.Name] += time.Since(began)
	p.failed[kind.Name] = p.failed[kind.Name] || failed
}

// observePasses records one pass for each kind that the pass served.
// Every pass counts, a pass that changed nothing too, because the rate
// of passes shows that the loop runs.
func (m *metrics) observePasses(p *passTimes) {
	for kind, took := range p.took {
		m.observePass(kind, took, p.failed[kind])
	}
}

func (m *metrics) observePass(kind string, took time.Duration, failed bool) {
	m.reconcileDuration.WithLabelValues(kind).Observe(took.Seconds())
	if failed {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// watchReopened answers the function that counts the reopened watches
// of one kind (informer.Options.Reopened).
func (m *metrics) watchReopened(kind string) func() {
	counter := m.watchRestarts.WithLabelValues(kind)
	return counter.Inc
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
// which keeps a synctest bubble's clock moving where a real socket
// stops it.
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
		// goroutine until this bound. The handler reads memory alone,
		// so the server needs no other timeout.
		ReadHeaderTimeout: 10 * time.Second,
	}
	context.AfterFunc(ctx, func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return listener.Addr(), nil
}
