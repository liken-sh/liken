package main

// observatory-operator exports Go runtime and process metrics,
// reconcile metrics for each loop, and series for the telescopes,
// cameras, and guiders, all from one registry. This file holds the
// registry, the /metrics listener, and the runtime and reconcile
// metrics. series.go sets the series of each namespace.
//
// A scrape reads only memory and never contacts INDI, PHD2, or the API
// server. The operator sets each series when its own work reads the
// fact: while it builds a status, runs a step, or receives an INDI or
// PHD2 event.

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
// Dockerfile sets it with -ldflags "-X main.version=...". A build
// without that flag, such as go test, reports dev.
var version = "dev"

// metricsPrefix starts the name of every series of this operator,
// except liken_build_info, which every liken component exports under
// the same name.
const metricsPrefix = "observatory_"

// metricsComponent is the value of liken_build_info's component label.
const metricsComponent = "observatory-operator"

// ownedKinds are the kinds of the objects that the operator creates
// for its servers. Each has a watch, and watch_restarts_total counts
// its reopens under the kind's name.
var ownedKinds = []string{"Pod", "Service", "ConfigMap", "Job"}

// reconcileBuckets run from 1 ms to about 16 s in steps of 4, to cover
// a pass that only reads the stores and a pass that writes several
// statuses.
var reconcileBuckets = prometheus.ExponentialBuckets(0.001, 4, 8)

// activationBuckets run from 10 s to 40 minutes. An activation runs
// several steps, and a camera's cool-down action alone has a default
// timeout of 20 minutes (observatory.CoolTimeout).
var activationBuckets = []float64{10, 30, 60, 120, 300, 600, 1200, 2400}

// metrics is the registry that the listener serves. The process builds
// one, and the operator of each namespace sets its series through a
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
// binary was built from, for liken_build_info.
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
		reconcileErrors: counter("reconcile_errors_total", "Reconcile passes that returned an error, by the kind of resource that the loop reconciles.", "kind"),
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

	// The value of liken_build_info is always 1 and the release is in
	// its labels, so one query shows the release of every component in
	// a cluster.
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

	// Create each kind's series now, so that the first scrape reports
	// them at zero. Prometheus computes rate() and increase() between
	// two samples, so a counter that first appears with a value of 1
	// hides that first increment.
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

// The reconcile metrics label each loop's passes with the kind of the
// resource that the loop serves:
//
//   - The supervisor uses Reservation. Each pass starts a runner for
//     each Reservation that has work left and deletes the servers
//     that no Reservation holds.
//   - The status writer uses the kind of each status that it writes,
//     and times the objects of each kind separately. It writes every
//     kind's status except Reservation, whose runner writes its own.
//   - The lock relay uses Observatory, because it relays the park
//     locks of each Observatory and sets its LocksRelayed condition.
//   - The trigger controller uses the kind of each resource whose
//     spec.triggers it reads.
//
// A runner executes steps and has no passes. The activation duration
// and step failure series measure its work instead.

// passTimes accumulates, by kind, the time and failures of one pass of
// a loop that serves several kinds.
type passTimes struct {
	took   map[string]time.Duration
	failed map[string]bool
	// timing and began are the kind and start time of the resource that
	// is being timed, from start until stop.
	timing *observatory.Kind
	began  time.Time
}

// start begins timing one resource and first ends the timing of the
// previous one. A loop whose body can use continue calls start at the
// top of each iteration and stop after the loop, so every exit path
// is timed.
func (p *passTimes) start(kind observatory.Kind) {
	p.stop()
	p.timing, p.began = &kind, time.Now()
}

// stop adds the time since start to the kind that start was given.
func (p *passTimes) stop() {
	if p.timing != nil {
		p.add(*p.timing, p.began, false)
		p.timing = nil
	}
}

func newPassTimes() *passTimes {
	return &passTimes{took: map[string]time.Duration{}, failed: map[string]bool{}}
}

// add adds the time since began to kind, and records a failure.
func (p *passTimes) add(kind observatory.Kind, began time.Time, failed bool) {
	p.took[kind.Name] += time.Since(began)
	p.failed[kind.Name] = p.failed[kind.Name] || failed
}

// observePasses records one pass for each kind that p timed. A pass
// that changed nothing counts too, because the rate of passes shows
// that the loop is running.
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

// watchReopened returns a function that counts one reopened watch of
// kind. It is the Reopened option of the informers.
func (m *metrics) watchReopened(kind string) func() {
	counter := m.watchRestarts.WithLabelValues(kind)
	return counter.Inc
}

// handler serves the registry in the Prometheus text format.
func (m *metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		// A failed collector fails the whole scrape, so a scrape never
		// returns a document with series missing.
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// listen is net.Listen. A test replaces it with an in-memory listener,
// because a real socket stops the fake clock of a synctest bubble.
var listen = net.Listen

// serve starts the /metrics server and returns the address it listens
// on. An empty address disables the server and returns a nil address.
// It listens before it returns, so a port that is in use is an error to
// the caller. The server stops when ctx ends.
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
		// A client that connects and sends no headers keeps a goroutine
		// until this timeout. The handler only reads memory, so the
		// server needs no other timeout.
		ReadHeaderTimeout: 10 * time.Second,
	}
	context.AfterFunc(ctx, func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return listener.Addr(), nil
}
