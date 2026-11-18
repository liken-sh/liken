package main

// The operator's Prometheus metrics. A scrape reads only memory and
// never calls the API server or a receiver, because the reconcile loop
// sets each receiver gauge from the same reading that it writes to the
// Receiver's status.

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// inputTracker records the input last reported for each receiver, so
// setInput can delete the old series when the input changes. A
// GaugeVec cannot list its own label sets.
type inputTracker struct {
	mutex      sync.Mutex
	byReceiver map[string]string
}

func newInputTracker() inputTracker {
	return inputTracker{byReceiver: map[string]string{}}
}

// set records the receiver's input and returns the input it replaces.
// changed is false when the input is the one already recorded.
func (t *inputTracker) set(receiver, input string) (previous string, changed bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	previous, known := t.byReceiver[receiver]
	if known && previous == input {
		return "", false
	}
	t.byReceiver[receiver] = input
	return previous, true
}

// forget removes the receiver and returns its last recorded input.
func (t *inputTracker) forget(receiver string) (input string, known bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	input, known = t.byReceiver[receiver]
	delete(t.byReceiver, receiver)
	return input, known
}

// metricsPrefix starts the name of every metric this operator
// publishes, except liken_build_info, which every liken component
// publishes under the same name.
const metricsPrefix = "equipment_"

// metricsComponent is the component label of liken_build_info.
const metricsComponent = "equipment-operator"

// receiverKind is the kind label of the reconcile metrics and of the
// Receiver watch restarts. The CECBus controller also watches
// Receivers, and its restarts count under this kind too.
const receiverKind = "Receiver"

const (
	cecBusKind     = "CECBus"
	televisionKind = "Television"
	displayKind    = "Display"
	podKind        = "Pod"
)

// The status label of equipment_commands_total. The Denon protocol does
// not acknowledge a command, so ok means the command was written to the
// socket, not that the receiver acted on it. failed means the command
// was dropped before it was sent or the write returned an error.
// timeout means a session sent power on and the receiver did not report
// power on within sessionPowerWait. The session sends the input anyway.
const (
	commandOK      = "ok"
	commandFailed  = "failed"
	commandTimeout = "timeout"
)

// reconcileBuckets run from 1 ms to about 16 s, in steps of 4.
var reconcileBuckets = prometheus.ExponentialBuckets(0.001, 4, 8)

// metrics holds the registry that the listener serves and every metric
// on it.
type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	connected   *prometheus.GaugeVec
	obsValid    *prometheus.GaugeVec
	obsLastGood *prometheus.GaugeVec
	power       *prometheus.GaugeVec
	volume      *prometheus.GaugeVec
	inputInfo   *prometheus.GaugeVec
	commands    *prometheus.CounterVec

	inputs inputTracker

	wiim wiimGauges
}

// newMetrics builds the registry. liken_build_info reports version.
func newMetrics(version string) *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),

		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metricsPrefix + "reconcile_duration_seconds",
			Help:    "How long one reconcile pass takes, by resource kind.",
			Buckets: reconcileBuckets,
		}, []string{"kind"}),
		reconcileErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "reconcile_errors_total",
			Help: "Reconcile passes that failed, by resource kind.",
		}, []string{"kind"}),
		watchRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "watch_restarts_total",
			Help: "Watches that closed and opened again, by resource kind.",
		}, []string{"kind"}),

		connected: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "receiver_connected",
			Help: "1 while the operator's last exchange with the receiver succeeded, 0 otherwise.",
		}, []string{"receiver"}),
		obsValid: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "observation_valid",
			Help: "1 while the receiver that the source label names is reachable, 0 otherwise.",
		}, []string{"source"}),
		obsLastGood: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "observation_last_success_timestamp_seconds",
			Help: "The last time the operator found the receiver that the source label names reachable, in seconds since the Unix epoch.",
		}, []string{"source"}),
		power: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "receiver_power",
			Help: "1 while the receiver last reported power On, 0 otherwise.",
		}, []string{"receiver"}),
		volume: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "receiver_volume",
			Help: "The master volume that the receiver last reported, in the receiver's display units.",
		}, []string{"receiver"}),
		inputInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "receiver_input_info",
			Help: "The input that the receiver last reported as selected, as labels on a gauge whose value is always 1.",
		}, []string{"receiver", "input"}),
		commands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "commands_total",
			Help: "Commands this operator sent a receiver, by outcome.",
		}, []string{"status"}),

		inputs: newInputTracker(),
		wiim:   newWiimGauges(),
	}

	// Prometheus has no string metric, so the release is a label on a
	// gauge whose value is always 1.
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release each liken component runs, as labels on a gauge that is always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(metricsComponent, version).Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		m.reconcileDuration, m.reconcileErrors, m.watchRestarts,
		m.connected, m.obsValid, m.obsLastGood,
		m.power, m.volume, m.inputInfo, m.commands,
	)
	m.registry.MustRegister(m.wiim.collectors()...)

	// A series that first appears when it increases has no earlier
	// sample, so rate() misses that first increase. Creating each series
	// here publishes it at zero from the first scrape.
	m.reconcileDuration.WithLabelValues(receiverKind)
	m.reconcileErrors.WithLabelValues(receiverKind)
	m.watchRestarts.WithLabelValues(receiverKind)
	for _, status := range []string{commandOK, commandFailed, commandTimeout} {
		m.commands.WithLabelValues(status)
	}
	return m
}

// observeReconcile records one reconcile pass over all Receivers. It
// counts passes that change nothing too, so the rate of passes shows
// that the loop still runs.
func (m *metrics) observeReconcile(took time.Duration, err error) {
	m.reconcileDuration.WithLabelValues(receiverKind).Observe(took.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(receiverKind).Inc()
	}
}

// watchRestarted records that a Receiver watch was opened again after
// the API server closed the previous one.
func (m *metrics) watchRestarted() {
	m.watchRestarts.WithLabelValues(receiverKind).Inc()
}

// cecBusWatchRestarted records a restart of the CECBus watch.
func (m *metrics) cecBusWatchRestarted() {
	m.watchRestarts.WithLabelValues(cecBusKind).Inc()
}

// televisionWatchRestarted records a restart of the Television watch.
func (m *metrics) televisionWatchRestarted() {
	m.watchRestarts.WithLabelValues(televisionKind).Inc()
}

// displayWatchRestarted records a restart of the Display watch.
func (m *metrics) displayWatchRestarted() {
	m.watchRestarts.WithLabelValues(displayKind).Inc()
}

// podWatchRestarted records a restart of the watch of the node
// workload's pods.
func (m *metrics) podWatchRestarted() {
	m.watchRestarts.WithLabelValues(podKind).Inc()
}

// recordObservation sets a receiver's gauges from the state that the
// reconcile loop writes to its status. receiver_connected and
// observation_valid are always equal, because the driver connection is
// the only source of a receiver's state. now is the time of the read.
func (m *metrics) recordObservation(receiver string, state equipment.State, resolution int, now time.Time) {
	main, _ := state.Zone(equipment.MainZone)
	connected := state.Reachable == ConditionTrue
	m.connected.WithLabelValues(receiver).Set(gaugeValue(connected))
	m.obsValid.WithLabelValues(receiver).Set(gaugeValue(connected))
	if connected {
		m.obsLastGood.WithLabelValues(receiver).Set(float64(now.Unix()))
	}
	if main.Power != "" {
		m.power.WithLabelValues(receiver).Set(gaugeValue(main.Power == equipment.PowerOn))
	}
	if main.Volume != equipment.Unknown && resolution > 0 {
		m.volume.WithLabelValues(receiver).Set(float64(main.Volume) / float64(resolution))
	}
	if main.Input != "" {
		m.setInput(receiver, main.Input)
	}
}

// setInput sets the info gauge for the receiver's current input and
// deletes the series of its previous input. Without the delete, a
// scrape reports both inputs.
func (m *metrics) setInput(receiver, input string) {
	previous, changed := m.inputs.set(receiver, input)
	if !changed {
		return
	}
	if previous != "" {
		m.inputInfo.DeleteLabelValues(receiver, previous)
	}
	m.inputInfo.WithLabelValues(receiver, input).Set(1)
}

// reportCommand counts one command by outcome. It does nothing on a
// nil *metrics, so a controller or session built without metrics can
// still call it.
func (m *metrics) reportCommand(status string) {
	if m == nil {
		return
	}
	m.commands.WithLabelValues(status).Inc()
}

// forgetReceiver deletes the gauges of a deleted Receiver.
// equipment_commands_total has no receiver label, so it stays.
func (m *metrics) forgetReceiver(receiver string) {
	m.connected.DeleteLabelValues(receiver)
	m.obsValid.DeleteLabelValues(receiver)
	m.obsLastGood.DeleteLabelValues(receiver)
	m.power.DeleteLabelValues(receiver)
	m.volume.DeleteLabelValues(receiver)

	if input, known := m.inputs.forget(receiver); known {
		m.inputInfo.DeleteLabelValues(receiver, input)
	}
	m.wiim.forget(receiver)
}

func gaugeValue(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

// Handler serves the registry in the Prometheus text format.
func (m *metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		// Fail the scrape when a collector fails, instead of serving
		// output with that collector's metrics missing.
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// listen is net.Listen. A test replaces it with an in-memory listener,
// because a real socket stops the fake clock of a synctest bubble.
var listen = net.Listen

// Serve starts the /metrics listener in its own goroutine and returns
// its address, so a caller that passed port 0 learns the port. A port
// already in use returns an error. An empty address starts no
// listener. The server stops when ctx ends.
func (m *metrics) Serve(ctx context.Context, address string) (net.Addr, error) {
	if address == "" {
		return nil, nil
	}
	listener, err := listen("tcp", address)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	server := &http.Server{
		Handler: mux,
		// Without a timeout, a client that connects and sends no headers
		// keeps a goroutine forever. The handler only reads memory, so no
		// other timeout is needed.
		ReadHeaderTimeout: 10 * time.Second,
	}
	// server.Serve does not watch ctx, so close the server when ctx ends.
	context.AfterFunc(ctx, func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return listener.Addr(), nil
}
