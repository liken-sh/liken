package main

// The operator's Prometheus registry holds liken_build_info, the
// duration, error, and watch-restart series of the reconcile loop, and
// the series of plans/completed/08-prometheus-metrics.md: whether each
// endpoint is connected, ready, and claimed, whether the reads of
// PipeWire and BlueZ succeeded, and which writes to hardware failed.
//
// The recording methods take facts that a reconcile pass already read,
// so a scrape reads memory and opens no card, PipeWire graph, or D-Bus
// connection.
import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// readHeaderTimeout bounds how long every HTTP server in this package,
// the audio-api and capture servers included, waits for a request's
// headers, so a client that connects and sends nothing cannot keep the
// connection open.
const readHeaderTimeout = 30 * time.Second

// component is the value of the component label on liken_build_info.
const component = "audio-operator"

// The values of the source label of audio_observation_valid. bluez is
// only the media bus that this pod reads paired speakers from, because
// bluetooth-operator owns the radio link and the battery.
const (
	sourcePipeWire = "pipewire"
	sourceBlueZ    = "bluez"
)

// The values of the operation label of audio_control_failures_total,
// one for each kind of write that apply makes to hardware.
const (
	operationVolume  = "volume"
	operationControl = "control"
	operationCodec   = "codec"
)

// metrics holds the registry that the listener serves and the
// instruments registered on it. Every recording method does nothing on
// a nil *metrics.
type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	endpointConnected *prometheus.GaugeVec
	endpointReady     *prometheus.GaugeVec
	endpointClaimed   *prometheus.GaugeVec

	observationValid       *prometheus.GaugeVec
	observationLastSuccess *prometheus.GaugeVec

	controlFailures *prometheus.CounterVec

	// now is a field so that a test can fix the time of a successful
	// read.
	now func() time.Time
}

// newMetrics creates the registry, with version as the version label
// of liken_build_info.
func newMetrics(version string) *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "audio_reconcile_duration_seconds",
			Help: "How long one reconcile pass of one endpoint's resource took.",
		}, []string{"kind"}),
		reconcileErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audio_reconcile_errors_total",
			Help: "Reconcile passes of one endpoint's resource that ended in an error.",
		}, []string{"kind"}),
		watchRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audio_watch_restarts_total",
			Help: "Watches the API server closed that this operator opened again.",
		}, []string{"kind"}),
		endpointConnected: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "audio_endpoint_connected",
			Help: "One while the endpoint's physical link is present, zero while it is not.",
		}, []string{"endpoint"}),
		endpointReady: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "audio_endpoint_ready",
			Help: "One while PipeWire has a node for the endpoint, zero while it does not.",
		}, []string{"endpoint"}),
		endpointClaimed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "audio_endpoint_claimed",
			Help: "One while a claim holds the endpoint, zero while none does.",
		}, []string{"endpoint"}),
		observationValid: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "audio_observation_valid",
			Help: "One while the last read of the source succeeded, zero while it did not.",
		}, []string{"source"}),
		observationLastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "audio_observation_last_success_timestamp_seconds",
			Help: "When a read of the source last succeeded, in seconds since the epoch.",
		}, []string{"source"}),
		controlFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audio_control_failures_total",
			Help: "Writes to hardware that failed, by the kind of write.",
		}, []string{"operation"}),
		now: time.Now,
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release this process was built from. Always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(component, version).Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		m.reconcileDuration, m.reconcileErrors, m.watchRestarts,
		m.endpointConnected, m.endpointReady, m.endpointClaimed,
		m.observationValid, m.observationLastSuccess,
		m.controlFailures,
	)
	return m
}

// reconciled records the duration of one reconcile pass over one
// endpoint's resource, and counts an error when err is not nil. A pass
// that changes nothing is still recorded.
func (m *metrics) reconciled(kind string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	m.reconcileDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// watchRestarted counts a watch that the API server closed and this
// operator opened again. Callers do not call it for the first open.
func (m *metrics) watchRestarted(kind string) {
	if m == nil {
		return
	}
	m.watchRestarts.WithLabelValues(kind).Inc()
}

// endpoint sets the connected, ready, and claimed gauges of one
// endpoint. Connected and Ready are the facts that sinkstatus.go writes
// into the resource's conditions.
//
// A pass whose graph read failed does not call endpoint, so the gauges
// keep the values of the last successful pass, because a failed read
// does not show that the endpoint disconnected.
func (m *metrics) endpoint(name string, connected, ready, claimed bool) {
	if m == nil {
		return
	}
	m.endpointConnected.WithLabelValues(name).Set(gauge(connected))
	m.endpointReady.WithLabelValues(name).Set(gauge(ready))
	m.endpointClaimed.WithLabelValues(name).Set(gauge(claimed))
}

// observationSucceeded marks the latest read of a source as valid and
// records its time. Prometheus computes the age of the read at query
// time, so the operator writes nothing between scrapes.
func (m *metrics) observationSucceeded(source string) {
	if m == nil {
		return
	}
	m.observationValid.WithLabelValues(source).Set(1)
	m.observationLastSuccess.WithLabelValues(source).Set(float64(m.now().Unix()))
}

// observationFailed marks the latest read of a source as invalid. It
// leaves the last-success timestamp unchanged, so the age of the last
// valid read stays visible.
func (m *metrics) observationFailed(source string) {
	if m == nil {
		return
	}
	m.observationValid.WithLabelValues(source).Set(0)
}

func (m *metrics) controlFailed(operation string) {
	if m == nil {
		return
	}
	m.controlFailures.WithLabelValues(operation).Inc()
}

// gauge converts a bool to the 1 or 0 of a boolean gauge.
func gauge(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

// listen opens a TCP listener on address, the value of METRICS_ADDRESS.
// An empty address returns a nil listener and no error.
func (m *metrics) listen(address string) (net.Listener, error) {
	if address == "" {
		return nil, nil
	}
	return net.Listen("tcp", address)
}

// handler serves the registry at /metrics and no other path.
func (m *metrics) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return mux
}

// serveMetrics serves HTTP on listener until ctx ends. A failure is
// logged and does not exit the process, because the reconcile loop does
// not depend on a scraper reaching it.
func serveMetrics(ctx context.Context, listener net.Listener, m *metrics) {
	server := &http.Server{
		Handler:           m.handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	if err := server.Serve(listener); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "the metrics listener stopped: %v\n", err)
	}
}
