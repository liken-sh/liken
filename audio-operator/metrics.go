package main

// metrics.go serves the operator's Prometheus registry.
//
// The registry carries three layers. Layer 1 is liken's own
// liken_build_info, the one gauge every process in the organization
// carries under its own component name. Layer 2 is the reconcile loop:
// how long a pass over one Sink or Source took, whether it failed, and
// how often a watch reopened. Layer 3 is this operator's own plan: the
// hardware triple for an endpoint, whether PipeWire and BlueZ still
// answer, and the writes to hardware that fail.
//
// Every reading here comes from a fact sinkstatus.go or reconcile.go
// already derived for the resource or the slice. A scrape reads the
// registry in memory; it opens no card, no PipeWire graph, and no
// D-Bus connection.
import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsDeadline bounds a scrape's headers and the listener's stop.
const metricsDeadline = 30 * time.Second

// component is this repository's name on liken_build_info, the label
// that lets one panel list every process's release in the cluster.
const component = "audio-operator"

// The two sources this operator observes. bluetooth-operator owns the
// radio link and the battery; this name covers only the media bus this
// pod reads paired speakers over.
const (
	sourcePipeWire = "pipewire"
	sourceBlueZ    = "bluez"
)

// The fixed vocabulary audio_control_failures_total takes: the three
// writes apply makes to hardware. A call site that forgets to name one
// costs that failure a place on the graph, not a label nobody chose.
const (
	operationVolume  = "volume"
	operationControl = "control"
	operationCodec   = "codec"
)

// metrics holds the registry the listener serves. Every field but the
// registry itself is one instrument from the plan; grouping them here,
// rather than behind a stand-alone Prometheus client, is what makes a
// scrape reachable from a real registry in a test with no HTTP server
// behind it.
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

	// now is when a successful observation is timestamped. A field
	// rather than a call to time.Now lets a test read a fixed value
	// off the gauge instead of a moving one.
	now func() time.Time
}

// newMetrics builds the registry and sets liken_build_info once, to
// the version this binary was built from. version is "dev" outside a
// release build; main.go says why.
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
			Help: "One while PipeWire holds a node for the endpoint, zero while it does not.",
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
			Help: "Writes to hardware that failed, by the write's own kind.",
		}, []string{"operation"}),
		now: time.Now,
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release this process was built from. Always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(component, version).Set(1)

	m.registry.MustRegister(
		buildInfo,
		m.reconcileDuration, m.reconcileErrors, m.watchRestarts,
		m.endpointConnected, m.endpointReady, m.endpointClaimed,
		m.observationValid, m.observationLastSuccess,
		m.controlFailures,
	)
	return m
}

// reconciled records one pass over one endpoint's resource: how long
// it took, and whether it ended in an error. A pass that changes
// nothing still counts as a run, so the duration is observed whether
// or not err is nil.
func (m *metrics) reconciled(kind string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	m.reconcileDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// watchRestarted counts one watch the API server closed that this
// operator opened again. The first open of a watch is not a restart,
// so the caller counts only a reopen.
func (m *metrics) watchRestarted(kind string) {
	if m == nil {
		return
	}
	m.watchRestarts.WithLabelValues(kind).Inc()
}

// endpoint puts what one pass read about one endpoint's presence on
// the hardware triple's gauges: the same Connected and Ready facts
// sinkstatus.go composed into the resource's conditions, and whether a
// claim holds the endpoint now.
//
// A pass that never runs for an endpoint, because the observation
// behind it was invalid, calls this for no endpoint, and the gauges
// keep the value the last pass set: unknown is not disconnected.
func (m *metrics) endpoint(name string, connected, ready, claimed bool) {
	if m == nil {
		return
	}
	m.endpointConnected.WithLabelValues(name).Set(gauge(connected))
	m.endpointReady.WithLabelValues(name).Set(gauge(ready))
	m.endpointClaimed.WithLabelValues(name).Set(gauge(claimed))
}

// observationSucceeded marks one source's read as valid, and stamps
// the moment it succeeded. The timestamp is the event's own time, so a
// reader computes the age itself instead of reading one this operator
// counted up between scrapes.
func (m *metrics) observationSucceeded(source string) {
	if m == nil {
		return
	}
	m.observationValid.WithLabelValues(source).Set(1)
	m.observationLastSuccess.WithLabelValues(source).Set(float64(m.now().Unix()))
}

// observationFailed marks one source's read as invalid. The last
// success timestamp is left alone, so a reader can see how old the
// standing observation is.
func (m *metrics) observationFailed(source string) {
	if m == nil {
		return
	}
	m.observationValid.WithLabelValues(source).Set(0)
}

// controlFailed counts one write to hardware that failed.
func (m *metrics) controlFailed(operation string) {
	if m == nil {
		return
	}
	m.controlFailures.WithLabelValues(operation).Inc()
}

// gauge carries a state as one or zero, the form every boolean gauge
// in the registry reports.
func gauge(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

// listen opens the address METRICS_ADDRESS names. An empty address
// serves no metrics, which is what a pod under test runs with.
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
// here is reported and never fatal: the reconcile loop this operator
// exists for does not depend on a scraper reaching it.
func serveMetrics(ctx context.Context, listener net.Listener, m *metrics) {
	serving := &http.Server{
		Handler:           m.handler(),
		ReadHeaderTimeout: metricsDeadline,
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "the metrics listener stopped: %v\n", err)
	}
}
