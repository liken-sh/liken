package main

// The Prometheus registry that the operator serves at /metrics, and the
// HTTP listener that serves it. The code that reads a value records it
// in the registry at that time, so a scrape only reads the registry and
// never calls BlueZ, the kernel, or the API server.
//
// The operator exports no count of watch restarts. client-go's
// reflector reopens a Kubernetes watch that the API server closes
// (watch.go). When the D-Bus connection is lost, godbus closes the
// signal channels and the process exits, so the pod's restart count
// shows that failure.

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

// metricsDeadline is the longest the server waits for a scrape's
// request headers.
const metricsDeadline = 30 * time.Second

// component and version are the labels of liken_build_info. The
// Dockerfile sets version with -ldflags "-X main.version=...", and a
// binary built without that flag reports "dev".
const component = "bluetooth-operator"

var version = "dev"

// sourceBlueZ is the only value of the source label. One
// GetManagedObjects call reads the paired set, the connection state,
// and the BlueZ battery level. The kernel's power_supply class also
// reports battery levels, but discoverHIDDevices returns nil both when
// the sysfs read fails and when there is no device, so the operator
// cannot record whether that read succeeded.
const sourceBlueZ = "bluez"

// peripheralLabel is the label set of the per-Peripheral metrics. Its
// value is the Peripheral's name.
var peripheralLabel = []string{"peripheral"}

// The values of the result label on bluetooth_pair_attempts_total.
const (
	resultPaired  = "paired"
	resultRefused = "refused"
)

// Every method of metrics does nothing on a nil *metrics, so a test
// that does not check metrics leaves the field unset.
type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec

	peripheralConnected *prometheus.GaugeVec
	peripheralClaimed   *prometheus.GaugeVec
	peripheralBattery   *prometheus.GaugeVec
	peripheralBonded    *prometheus.GaugeVec
	disconnects         *prometheus.CounterVec
	wakePresses         *prometheus.CounterVec
	pairAttempts        *prometheus.CounterVec
	adapterPresent      prometheus.Gauge
	inputEvents         *prometheus.CounterVec
	observationValid    *prometheus.GaugeVec
	observationSuccess  *prometheus.GaugeVec
}

func newMetrics() *metrics {
	m := &metrics{registry: prometheus.NewRegistry()}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release this process was built from. Always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(component, version).Set(1)

	m.reconcileDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "bluetooth_reconcile_duration_seconds",
		Help: "How long one reconcile pass over one resource kind took.",
	}, []string{"kind"})
	m.reconcileErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bluetooth_reconcile_errors_total",
		Help: "Reconcile passes over one resource kind that ended in an error.",
	}, []string{"kind"})

	m.peripheralConnected = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bluetooth_peripheral_connected",
		Help: "One while a Peripheral has a link with its adapter, zero while it does not.",
	}, peripheralLabel)
	m.peripheralClaimed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bluetooth_peripheral_claimed",
		Help: "One while a prepared claim holds the Peripheral's controller, zero while none does.",
	}, peripheralLabel)
	m.peripheralBattery = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bluetooth_peripheral_battery_percent",
		Help: "The charge the Peripheral last reported. Absent when no source reports a level.",
	}, peripheralLabel)
	m.peripheralBonded = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bluetooth_peripheral_bonded",
		Help: "One while BlueZ reports a stored link key for the Peripheral, zero while it reports none.",
	}, peripheralLabel)
	m.disconnects = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bluetooth_disconnects_total",
		Help: "Observed transitions of a Peripheral from connected to disconnected, by the reason of the Connected condition.",
	}, []string{"peripheral", "reason"})
	m.wakePresses = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bluetooth_wake_presses_total",
		Help: "KEY_UNKNOWN presses the relay wrote into a Peripheral's virtual device when it connected after a sleep.",
	}, peripheralLabel)
	m.pairAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bluetooth_pair_attempts_total",
		Help: "Device1.Pair calls, counted as paired when bluetoothd made the bond and as refused when it did not.",
	}, []string{"result"})
	m.adapterPresent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bluetooth_adapter_present",
		Help: "One while bluetoothd publishes this node's adapter, zero while it reports none.",
	})
	m.inputEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bluetooth_input_events_total",
		Help: "Input events the relay forwarded from a Peripheral's real node to its virtual one.",
	}, peripheralLabel)
	m.observationValid = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bluetooth_observation_valid",
		Help: "One while the last read of a source succeeded, zero while it did not.",
	}, []string{"source"})
	m.observationSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bluetooth_observation_last_success_timestamp_seconds",
		Help: "When a read of a source last succeeded, in seconds since the epoch.",
	}, []string{"source"})

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo, m.reconcileDuration, m.reconcileErrors,
		m.peripheralConnected, m.peripheralClaimed, m.peripheralBattery, m.peripheralBonded,
		m.disconnects, m.wakePresses, m.pairAttempts,
		m.adapterPresent, m.inputEvents, m.observationValid, m.observationSuccess)
	return m
}

// gauge converts a boolean to the 1 or 0 that a boolean gauge holds.
func gauge(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

func (m *metrics) timeReconcile(kind string, since, now time.Time) {
	if m == nil {
		return
	}
	m.reconcileDuration.WithLabelValues(kind).Observe(now.Sub(since).Seconds())
}

// countReconcileError counts a reconcile of one resource kind that
// ended in an error. bluetooth_reconcile_duration_seconds counts every
// reconcile, failed or not.
func (m *metrics) countReconcileError(kind string) {
	if m == nil {
		return
	}
	m.reconcileErrors.WithLabelValues(kind).Inc()
}

// recordObservation records whether the latest read of a source
// succeeded, and the time of the latest success. After a failed read,
// the connected and battery gauges keep the last good reading, and the
// timestamp shows how old it is.
func (m *metrics) recordObservation(source string, ok bool, now time.Time) {
	if m == nil {
		return
	}
	m.observationValid.WithLabelValues(source).Set(gauge(ok))
	if ok {
		m.observationSuccess.WithLabelValues(source).Set(float64(now.Unix()))
	}
}

// setAdapterPresent records whether bluetoothd published this node's
// adapter. The caller calls it only after a read that succeeded, so
// after a failed read the gauge keeps its previous value.
func (m *metrics) setAdapterPresent(present bool) {
	if m == nil {
		return
	}
	m.adapterPresent.Set(gauge(present))
}

func (m *metrics) setPeripheralConnected(peripheral string, connected bool) {
	if m == nil {
		return
	}
	m.peripheralConnected.WithLabelValues(peripheral).Set(gauge(connected))
}

// countDisconnect counts a status write that changed a Peripheral's
// Connected condition from True to False, under the new reason. The
// caller does not count the first Connected condition of a new
// Peripheral, because the operator did not see that link go down.
func (m *metrics) countDisconnect(peripheral, reason string) {
	if m == nil {
		return
	}
	m.disconnects.WithLabelValues(peripheral, reason).Inc()
}

func (m *metrics) countWakePress(peripheral string) {
	if m == nil {
		return
	}
	m.wakePresses.WithLabelValues(peripheral).Inc()
}

func (m *metrics) setPeripheralBonded(peripheral string, bonded bool) {
	if m == nil {
		return
	}
	m.peripheralBonded.WithLabelValues(peripheral).Set(gauge(bonded))
}

func (m *metrics) countPairAttempt(result string) {
	if m == nil {
		return
	}
	m.pairAttempts.WithLabelValues(result).Inc()
}

// setPeripheralBattery deletes the series when percentage is nil, so
// that a battery at 0 percent differs from a controller that reports
// no battery.
func (m *metrics) setPeripheralBattery(peripheral string, percentage *int) {
	if m == nil {
		return
	}
	if percentage == nil {
		m.peripheralBattery.DeleteLabelValues(peripheral)
		return
	}
	m.peripheralBattery.WithLabelValues(peripheral).Set(float64(*percentage))
}

func (m *metrics) setPeripheralClaimed(peripheral string, claimed bool) {
	if m == nil {
		return
	}
	m.peripheralClaimed.WithLabelValues(peripheral).Set(gauge(claimed))
}

func (m *metrics) countInputEvents(peripheral string, count int) {
	if m == nil || count == 0 {
		return
	}
	m.inputEvents.WithLabelValues(peripheral).Add(float64(count))
}

// forgetPeripheral deletes every series of a Peripheral when unpair
// deletes the object. Without it, the series stay in the scrape at
// their last values.
func (m *metrics) forgetPeripheral(peripheral string) {
	if m == nil {
		return
	}
	m.peripheralConnected.DeleteLabelValues(peripheral)
	m.peripheralClaimed.DeleteLabelValues(peripheral)
	m.peripheralBattery.DeleteLabelValues(peripheral)
	m.peripheralBonded.DeleteLabelValues(peripheral)
	m.disconnects.DeletePartialMatch(prometheus.Labels{"peripheral": peripheral})
	m.wakePresses.DeleteLabelValues(peripheral)
	m.inputEvents.DeleteLabelValues(peripheral)
}

// listen returns no listener and no error for an empty address, which
// turns metrics off.
func (m *metrics) listen(address string) (net.Listener, error) {
	if address == "" {
		return nil, nil
	}
	return net.Listen("tcp", address)
}

func (m *metrics) handler() http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return served
}

// serveMetrics serves scrapes until ctx ends. When the listener fails,
// it logs the error and returns, and the rest of the operator keeps
// running.
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
