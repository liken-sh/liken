package main

// This is the operator's half of liken's Prometheus contract
// (liken/plans/65-prometheus-metrics.md, and this repository's own
// plans/01-prometheus-metrics.md): the runtime layer, the reconcile
// loop layer, and the domain facts a Receiver's status already
// carries, all on one registry a scrape reads from memory.
//
// A scrape never touches the network, the API server, or a receiver.
// Every gauge here is set at the same moment the fact it reports
// reaches a Receiver's status or the loop's own bookkeeping, and
// never from a second read of the world.

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// inputTracker remembers the input last reported for each receiver, so
// setInput knows which series to delete when the input changes. It is
// the only piece of state in this file that a Prometheus type does not
// already hold, because a GaugeVec has no way to ask what value one of
// its own label sets last took.
type inputTracker struct {
	mutex sync.Mutex
	held  map[string]string
}

func newInputTracker() inputTracker {
	return inputTracker{held: map[string]string{}}
}

// set records the input and answers the one it replaces, plus whether
// this is a change at all: a report of the input the receiver already
// holds touches no series.
func (t *inputTracker) set(receiver, input string) (previous string, changed bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	previous, known := t.held[receiver]
	if known && previous == input {
		return "", false
	}
	t.held[receiver] = input
	return previous, true
}

// forget removes the receiver and answers the input it last held, so
// the caller can delete that series too.
func (t *inputTracker) forget(receiver string) (input string, known bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	input, known = t.held[receiver]
	delete(t.held, receiver)
	return input, known
}

// metricsPrefix names every metric this operator publishes of its own,
// under milestone 65's per-repository prefix. liken_build_info is the
// one name every component in the organization shares, so it carries
// no prefix of its own.
const metricsPrefix = "equipment_"

// metricsComponent is this operator's own name, the value
// liken_build_info carries in its component label.
const metricsComponent = "equipment-operator"

// receiverKind is the one resource kind this operator's loop serves.
// The reconcile layer is labeled by kind so a dashboard built across
// several operators reads the same series everywhere; this operator
// only ever reports the one value.
const receiverKind = "Receiver"

// The three outcomes equipment_commands_total counts. The Denon
// protocol carries no acknowledgement tied to a particular command, so
// ok means the command reached the socket, not that the receiver acted
// on it. failed is a command dropped before it went out, or a write
// that errored. timeout is the one wait this operator gives up on: the
// power-on a session sends before it selects the input.
const (
	commandOK      = "ok"
	commandFailed  = "failed"
	commandTimeout = "timeout"
)

// reconcileBuckets bounds the histogram at what a level-triggered pass
// over this operator's own collection actually takes: a pass with
// nothing to do reads in about a millisecond, and a pass that writes
// several statuses takes a few.
var reconcileBuckets = prometheus.ExponentialBuckets(0.001, 4, 8)

// metrics is the registry the listener serves and the whole of what
// this operator reports about itself. One process builds one, and
// hands it to the parts of itself that observe something.
type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	connected   *prometheus.GaugeVec
	claimed     *prometheus.GaugeVec
	obsValid    *prometheus.GaugeVec
	obsLastGood *prometheus.GaugeVec
	power       *prometheus.GaugeVec
	volume      *prometheus.GaugeVec
	inputInfo   *prometheus.GaugeVec
	commands    *prometheus.CounterVec

	inputs inputTracker
}

// newMetrics builds the registry for this operator's one process.
// version is the release the binary was built from, the fact
// liken_build_info carries.
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
			Help: "One while the operator has an answered exchange with the receiver, zero otherwise.",
		}, []string{"receiver"}),
		claimed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "receiver_claimed",
			Help: "One while a Player holds a session on the receiver, zero otherwise.",
		}, []string{"receiver"}),
		obsValid: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "observation_valid",
			Help: "One while the named source's last read of a receiver is current, zero otherwise.",
		}, []string{"source"}),
		obsLastGood: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "observation_last_success_timestamp_seconds",
			Help: "When the named source last answered, in seconds since the epoch.",
		}, []string{"source"}),
		power: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "receiver_power",
			Help: "One while the receiver last reported itself on, zero while on standby.",
		}, []string{"receiver"}),
		volume: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "receiver_volume",
			Help: "The master volume the receiver last reported, in its own scale.",
		}, []string{"receiver"}),
		inputInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "receiver_input_info",
			Help: "The input the receiver last reported selected, as labels on a gauge that is always 1.",
		}, []string{"receiver", "input"}),
		commands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "commands_total",
			Help: "Commands this operator sent a receiver, by outcome.",
		}, []string{"status"}),

		inputs: newInputTracker(),
	}

	// Layer 1. buildInfo always holds 1, and its labels carry the
	// release, which is the Prometheus convention for a string. One
	// panel that reads liken_build_info across a cluster shows every
	// component's release, because every liken process publishes this
	// one name.
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
		m.connected, m.claimed, m.obsValid, m.obsLastGood,
		m.power, m.volume, m.inputInfo, m.commands,
	)

	// Publishing every kind and every outcome at zero from the first
	// scrape is what lets Prometheus compute a rate from the second
	// reading: a counter that appears only when it first increases has
	// no earlier reading to compare against.
	m.reconcileDuration.WithLabelValues(receiverKind)
	m.reconcileErrors.WithLabelValues(receiverKind)
	m.watchRestarts.WithLabelValues(receiverKind)
	for _, status := range []string{commandOK, commandFailed, commandTimeout} {
		m.commands.WithLabelValues(status)
	}
	return m
}

// observeReconcile records one finished pass over the whole
// collection. Every pass counts, including a pass that changed
// nothing, because the rate of passes is what says the loop still
// runs.
func (m *metrics) observeReconcile(took time.Duration, err error) {
	m.reconcileDuration.WithLabelValues(receiverKind).Observe(took.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(receiverKind).Inc()
	}
}

// watchRestarted records one watch the operator opened again after the
// API server closed the one before it.
func (m *metrics) watchRestarted() {
	m.watchRestarts.WithLabelValues(receiverKind).Inc()
}

// recordObservation puts one receiver's freshly read state on the
// gauges, from the same denonState that builds its status. connected
// and observation_valid move together here, because the Denon
// connection is this operator's one source of a receiver's facts: a
// dropped connection is itself a fresh, valid observation that the
// receiver is gone, and not a missing reading. now is the moment the
// state was read, which is what the timestamp gauge reports.
func (m *metrics) recordObservation(receiver string, state denonState, now time.Time) {
	connected := state.Reachable == ConditionTrue
	m.connected.WithLabelValues(receiver).Set(gaugeValue(connected))
	m.obsValid.WithLabelValues(receiver).Set(gaugeValue(connected))
	if connected {
		m.obsLastGood.WithLabelValues(receiver).Set(float64(now.Unix()))
	}
	if state.Power != "" {
		m.power.WithLabelValues(receiver).Set(gaugeValue(state.Power == powerOn))
	}
	if state.Volume != unknownHalves {
		m.volume.WithLabelValues(receiver).Set(float64(state.Volume) / 2)
	}
	if state.Input != "" {
		m.setInput(receiver, state.Input)
	}
}

// setInput moves the info gauge to the input named, deleting the
// series for whichever input it held before. A gauge holds one row per
// label set forever unless something deletes it, so the receiver's
// previous input would otherwise linger on the scrape beside the one
// that replaced it.
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

// setClaimed puts whether a Player holds a session on the receiver now
// on the gauge, the moment the reconcile loop decides it, and not on
// the next debounced status write.
func (m *metrics) setClaimed(receiver string, claimed bool) {
	m.claimed.WithLabelValues(receiver).Set(gaugeValue(claimed))
}

// reportCommand counts one command by its outcome. m is nil for a
// denonClient a test builds with no metrics, and a nil receiver reports
// nothing rather than crash.
func (m *metrics) reportCommand(status string) {
	if m == nil {
		return
	}
	m.commands.WithLabelValues(status).Inc()
}

// forgetReceiver takes a receiver off every gauge this operator scopes
// to one, so a Receiver that is gone stops being reported. The
// counters are left alone: a command already sent still happened.
func (m *metrics) forgetReceiver(receiver string) {
	m.connected.DeleteLabelValues(receiver)
	m.claimed.DeleteLabelValues(receiver)
	m.obsValid.DeleteLabelValues(receiver)
	m.obsLastGood.DeleteLabelValues(receiver)
	m.power.DeleteLabelValues(receiver)
	m.volume.DeleteLabelValues(receiver)

	if input, known := m.inputs.forget(receiver); known {
		m.inputInfo.DeleteLabelValues(receiver, input)
	}
}

// gaugeValue carries a state as one or zero, the shape every boolean
// gauge in this file takes.
func gaugeValue(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

// Handler answers a scrape from this operator's registry. It is
// exported so a test can scrape without a socket.
func (m *metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		// A collector that fails is reported as a broken scrape, rather
		// than served as a document with a gap in it.
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// Serve starts the /metrics listener and returns at once, with the
// address the listener holds so a caller can report the real port
// after asking for port 0. An empty address turns the listener off,
// which is what lets an owner who runs no Prometheus give up the port.
//
// The listen call happens here, in the caller's own goroutine, so a
// port already in use is an error the caller can report. Serving runs
// on its own goroutine, because the work this operator exists for must
// never wait on a scraper, and it stops when ctx ends.
func (m *metrics) Serve(ctx context.Context, address string) (net.Addr, error) {
	if address == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	server := &http.Server{
		Handler: mux,
		// A client that opens a connection and sends no headers holds a
		// goroutine for as long as it stays. This bound releases that
		// goroutine, and it is the one timeout a metrics endpoint needs:
		// the handler itself only reads memory.
		ReadHeaderTimeout: 10 * time.Second,
	}
	// denon.go and bus.go use the same context.AfterFunc shape to
	// unblock a read that has nowhere else to hear that ctx ended; here
	// it closes a listener with nothing to read. The registration is
	// left running for the life of the process, because Serve returns
	// at once and nothing later stops watching ctx.
	context.AfterFunc(ctx, func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return listener.Addr(), nil
}
