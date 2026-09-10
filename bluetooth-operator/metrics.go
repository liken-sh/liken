package main

// The Prometheus registry this operator serves at /metrics, and the
// listener that answers a scrape.
//
// liken milestone 65 sets the contract every process in the
// organization follows: a build_info gauge, a reconcile loop's
// duration and errors by resource kind, and the domain's own facts
// under a prefix of its own. This operator's prefix is bluetooth_, on
// port 9250, and plans/07-prometheus-metrics.md states which facts.
//
// This file holds the registry and the two layers every process
// shares. The domain layer is recorded where each fact is already
// read: peripheral.go, relay.go, and inventory.go each call one
// narrow method here, so a scrape reads the same snapshot the
// reconcile loop already took and never touches BlueZ, the kernel, or
// the API server itself.
//
// bluetooth_watch_restarts_total is not here. Milestone 65 defines a
// watch restart as the API server closing a Kubernetes watch that the
// operator reopens. This operator holds no such watch: it polls the
// PairingRequests on a timer and wakes on D-Bus signals and kernel
// uevents, and a lost signal channel ends the process rather than
// reopening, so the pod's own restart is the fact and no metric of
// this operator's could ever carry a different value.

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

// metricsDeadline bounds a scrape's headers and the listener's stop.
const metricsDeadline = 30 * time.Second

// component names this process on liken_build_info. version is a
// build-time stamp: the Dockerfile links it in with -ldflags, and a
// binary built with no such argument reports "dev".
const component = "bluetooth-operator"

var version = "dev"

// sourceBlueZ is the one source this operator's observation gauges
// name. bluetoothd is the single point every fact in this file passes
// through: the paired set, the connection state, and the BlueZ half
// of the battery reading all come from one GetManagedObjects call, so
// one source covers all of them. The kernel's power supply class is
// the other battery source, but discoverHIDDevices treats a sysfs
// read it cannot do the same as an empty result, so there is no
// failure to distinguish it from an ordinary read, and this operator
// reports no separate source for it.
const sourceBlueZ = "bluez"

// peripheralLabel names the Peripheral resource every layer 3
// per-device metric carries.
var peripheralLabel = []string{"peripheral"}

// metrics holds the registry and every collector this operator
// writes to. A nil *metrics records nothing, which is what every
// test that has no reason to check a metric constructs by leaving the
// field unset.
type metrics struct {
	registry *prometheus.Registry

	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec

	peripheralConnected *prometheus.GaugeVec
	peripheralClaimed   *prometheus.GaugeVec
	peripheralBattery   *prometheus.GaugeVec
	disconnects         *prometheus.CounterVec
	adapterPresent      prometheus.Gauge
	inputEvents         *prometheus.CounterVec
	observationValid    *prometheus.GaugeVec
	observationSuccess  *prometheus.GaugeVec
}

func newMetrics() *metrics {
	m := &metrics{registry: prometheus.NewRegistry()}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release this process runs. Always 1; component and version carry the fact.",
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
		Help: "One while a Peripheral holds a link to its adapter, zero while it does not.",
	}, peripheralLabel)
	m.peripheralClaimed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bluetooth_peripheral_claimed",
		Help: "One while a prepared claim holds the Peripheral's controller, zero while none does.",
	}, peripheralLabel)
	m.peripheralBattery = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bluetooth_peripheral_battery_percent",
		Help: "The charge the Peripheral last reported. Absent when no source reports a level.",
	}, peripheralLabel)
	m.disconnects = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bluetooth_disconnects_total",
		Help: "Observed transitions of a Peripheral from connected to disconnected.",
	}, peripheralLabel)
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

	// Layer 1 is the runtime's own numbers beside the build info.
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo, m.reconcileDuration, m.reconcileErrors,
		m.peripheralConnected, m.peripheralClaimed, m.peripheralBattery, m.disconnects,
		m.adapterPresent, m.inputEvents, m.observationValid, m.observationSuccess)
	return m
}

// gauge carries a state as one or zero, the form every boolean gauge
// in this file takes.
func gauge(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

// timeReconcile records how long one resource kind's slice of a pass
// took. inventory.go calls it around each of the Adapter, Peripheral,
// and PairingRequest phases of reconcile.
func (m *metrics) timeReconcile(kind string, since, now time.Time) {
	if m == nil {
		return
	}
	m.reconcileDuration.WithLabelValues(kind).Observe(now.Sub(since).Seconds())
}

// countReconcileError counts one resource kind's phase ending in an
// error. A reconcile that changes nothing still counts as a run on
// the duration histogram; this counts only the runs that did not
// finish what they set out to do.
func (m *metrics) countReconcileError(kind string) {
	if m == nil {
		return
	}
	m.reconcileErrors.WithLabelValues(kind).Inc()
}

// recordObservation records whether one read of a source succeeded,
// and when it last did. While valid stays at zero, a reader knows the
// connected and battery gauges carry a reading from before the source
// went stale, and the timestamp says how stale.
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
// adapter on the pass that just read it. The caller writes this only
// when the read itself succeeded; a read that failed keeps the
// last-known value, the same rule the hardware triple states for
// every other gauge in the organization.
func (m *metrics) setAdapterPresent(present bool) {
	if m == nil {
		return
	}
	m.adapterPresent.Set(gauge(present))
}

// setPeripheralConnected records one Peripheral's link state, and
// counts a disconnect when this pass observed the link go from up to
// down. first is true only for a Peripheral this pass is reporting on
// for the first time, which is a creation and never a transition: a
// disconnect measures an edge this operator watched happen, not the
// gap between not knowing and knowing.
func (m *metrics) setPeripheralConnected(peripheral string, connected, wasConnected, first bool) {
	if m == nil {
		return
	}
	m.peripheralConnected.WithLabelValues(peripheral).Set(gauge(connected))
	if !first && wasConnected && !connected {
		m.disconnects.WithLabelValues(peripheral).Inc()
	}
}

// setPeripheralBattery records the charge a Peripheral's status
// reports, and clears the series for one that reports none. Deleting
// the series rather than writing zero keeps an empty battery, which
// is a real reading, distinct from a controller with no battery to
// report at all.
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

// setPeripheralClaimed records whether a prepared claim holds this
// Peripheral's controller right now, from the same CDI read the
// unpair teardown already makes.
func (m *metrics) setPeripheralClaimed(peripheral string, claimed bool) {
	if m == nil {
		return
	}
	m.peripheralClaimed.WithLabelValues(peripheral).Set(gauge(claimed))
}

// countInputEvents counts events the relay forwarded from one
// Peripheral's real node to its virtual one.
func (m *metrics) countInputEvents(peripheral string, count int) {
	if m == nil || count == 0 {
		return
	}
	m.inputEvents.WithLabelValues(peripheral).Add(float64(count))
}

// forgetPeripheral takes a Peripheral off every per-device gauge and
// counter, once its unpair has removed the object. A series for a
// bond that no longer exists would read as a controller nobody can
// see in the API, sitting at whatever value its last pass wrote.
func (m *metrics) forgetPeripheral(peripheral string) {
	if m == nil {
		return
	}
	m.peripheralConnected.DeleteLabelValues(peripheral)
	m.peripheralClaimed.DeleteLabelValues(peripheral)
	m.peripheralBattery.DeleteLabelValues(peripheral)
	m.disconnects.DeleteLabelValues(peripheral)
	m.inputEvents.DeleteLabelValues(peripheral)
}

// listen opens the address the operator's settings name. An empty
// address serves no metrics, which is every test and a cluster owner
// who runs no Prometheus.
func (m *metrics) listen(address string) (net.Listener, error) {
	if address == "" {
		return nil, nil
	}
	return net.Listen("tcp", address)
}

// handler serves the registry at /metrics and nothing else. A scrape
// reads the in-memory registry this file's methods already wrote to;
// it makes no BlueZ, kernel, or API call of its own.
func (m *metrics) handler() http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return served
}

// serveMetrics answers scrapes on the listener until the run ends. A
// failure here reaches the log and stops nothing else: pairing, bond
// restoration, and the input relay do not depend on a scrape
// succeeding.
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
