package main

// The machine operator's own metrics, which the liken/metrics package
// calls layer 3. Most are fields of the Machine's status exported as
// gauges. They are set from the same status and device list that the
// pass publishes, so a graph agrees with `kubectl get machine -o yaml`
// and with the ResourceSlice.
//
// The firmware version and its offered updates are status of the
// Firmware objects, so firmware-operator exports them. There is no
// upgrade duration, because it would need a staging time that survives
// the reboot, and the system release record has no timestamp.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// serveMetrics builds the operator's registry with layers 1 and 2 and
// this machine's layer 3, and starts the HTTP listener that serves
// /metrics and /healthz, which reports the liveness check. When the
// listener cannot bind, it logs the error and returns, because the
// machine must keep running without monitoring. The pod uses the host
// network, so another program on the machine can already hold the port.
func serveMetrics(address string, f *fetcher, health func() error) (*metrics.Operator, *machineMetrics) {
	o := metrics.NewOperator(component, machine.Version,
		[]string{machineKind}, watchKinds)
	o.SetHealth(health)
	layer := newMachineMetrics(o, f)
	o.Registry().MustRegister(clockCollector{dir: machine.ClockDir, now: time.Now})
	o.Registry().MustRegister(wirelessCollector{dir: machine.WirelessDir})
	if addr, err := o.Serve(address); err != nil {
		fmt.Fprintf(os.Stderr, "the metrics listener is not serving: %v\n", err)
	} else if addr != nil {
		fmt.Printf("serving metrics on %s/metrics\n", addr)
	}
	return o, layer
}

// machineMetrics holds the layer 3 metrics for the life of the process.
type machineMetrics struct {
	release       *prometheus.GaugeVec
	bootTimestamp optionalGauge
	changePending *prometheus.GaugeVec
	converged     optionalGauge
	devices       *prometheus.GaugeVec
	lastCrash     optionalGauge
	longestPass   prometheus.Gauge
	longest       float64
	repairs       *prometheus.CounterVec
	passes        *prometheus.CounterVec

	// classes holds every device class this machine has published. A
	// class whose devices all disappear is set to zero and not
	// removed, because a series that stops reporting shows as a gap in
	// a graph, and a series at zero shows the drop.
	classes map[string]bool
}

// An optionalGauge is a gauge that a pass can leave out of the scrape.
// A machine with no crash on record must not publish a crash
// timestamp, because 0 plots as 1970. A GaugeVec with no labels has
// one sample after set and no metric family after clear.
type optionalGauge struct{ vec *prometheus.GaugeVec }

func newOptionalGauge(name, help string) optionalGauge {
	return optionalGauge{vec: prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: name, Help: help}, nil)}
}

func (g optionalGauge) set(value float64) { g.vec.WithLabelValues().Set(value) }

func (g optionalGauge) clear() { g.vec.Reset() }

func tierOf(kind machine.DisruptionKind) string {
	return strings.ToLower(string(kind))
}

// pendingTiers are the kinds of change that wait for a disruption
// (cluster/changes.go). The pending gauge sets each one on every pass,
// so a machine with nothing pending reports 0, not a missing series.
var pendingTiers = []machine.DisruptionKind{machine.DisruptionReboot, machine.DisruptionRestart}

func newMachineMetrics(o *metrics.Operator, f *fetcher) *machineMetrics {
	m := &machineMetrics{
		release: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.Prefix + "release_info",
			Help: "The release this machine runs and the slot it booted from, as labels on a gauge that is always 1.",
		}, []string{"version", "slot"}),
		bootTimestamp: newOptionalGauge(metrics.Prefix+"machine_boot_timestamp_seconds",
			"When this machine booted, in seconds since the epoch."),
		changePending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.Prefix + "machine_change_pending",
			Help: "Staged changes that wait for a disruption, by the tier that applies them.",
		}, []string{"tier"}),
		converged: newOptionalGauge(metrics.Prefix+"machine_converged",
			"1 while the machine runs the spec in its Machine, and 0 while it does not."),
		devices: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.Prefix + "devices",
			Help: "Devices this node offers to workloads, by class.",
		}, []string{"class"}),
		lastCrash: newOptionalGauge(metrics.Prefix+"last_crash_timestamp_seconds",
			"When the newest kernel crash this machine still holds records for happened, in seconds since the epoch."),
		longestPass: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: metrics.Prefix + "machine_longest_pass_seconds",
			Help: "The longest reconcile pass since the operator started. The heartbeat stops once the loop is busy for 60 seconds.",
		}),
		repairs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metrics.Prefix + "machine_backstop_repairs_total",
			Help: "Writes by a pass that only the backstop started, by step. Each one shows a code path that changed state without starting a pass.",
		}, []string{"step"}),
		passes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metrics.Prefix + "machine_passes_total",
			Help: "Reconcile passes, by what started each one: start, watch, machine event, facts, retry, sysctl drift, disk drift, or backstop.",
		}, []string{"cause"}),
		classes: map[string]bool{},
	}

	// The download counters read the fetcher's totals at each scrape,
	// so no second copy can disagree with it. Each read takes the
	// fetcher's mutex and does no I/O (fetch.go).
	downloadBytes := prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: metrics.Prefix + "release_download_bytes_total",
		Help: "Release artifact bytes this machine has downloaded and verified onto a slot.",
	}, func() float64 { return float64(f.DownloadedBytes()) })
	downloadFailures := prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: metrics.Prefix + "release_download_failures_total",
		Help: "Release downloads that ended without a complete slot.",
	}, func() float64 { return float64(f.DownloadFailures()) })

	o.Registry().MustRegister(m.release, m.bootTimestamp.vec, m.changePending,
		m.converged.vec, m.devices, m.lastCrash.vec, downloadBytes, downloadFailures,
		m.longestPass, m.repairs, m.passes)

	for _, kind := range pendingTiers {
		m.changePending.WithLabelValues(tierOf(kind)).Set(0)
	}
	return m
}

// observePass raises the longest pass when took is longer. Only the
// loop's goroutine calls it, so m.longest needs no lock. A nil
// machineMetrics observes nothing.
func (m *machineMetrics) observePass(took time.Duration) {
	if m == nil {
		return
	}
	if took.Seconds() > m.longest {
		m.longest = took.Seconds()
		m.longestPass.Set(m.longest)
	}
}

// observeWake counts one pass by its cause.
func (m *machineMetrics) observeWake(cause string) {
	if m == nil {
		return
	}
	m.passes.WithLabelValues(cause).Inc()
}

// backstopRepaired counts one write of a backstop pass.
func (m *machineMetrics) backstopRepaired(step string) {
	if m == nil {
		return
	}
	m.repairs.WithLabelValues(step).Inc()
}

func (m *machineMetrics) observeStatus(status *machine.MachineStatus) {
	// The reset removes the series of the previous version and slot.
	// Without it, a machine that upgraded or fell back would report 1
	// for both releases and a fleet panel would count it twice.
	m.release.Reset()
	if status.Version.Liken != "" {
		m.release.WithLabelValues(status.Version.Liken, status.Boot.Slot).Set(1)
	}

	m.bootTimestamp.clear()
	if status.Boot.Time != nil && !status.Boot.Time.IsZero() {
		m.bootTimestamp.set(float64(status.Boot.Time.Unix()))
	}

	pending := map[string]float64{}
	for _, p := range status.Pending {
		pending[tierOf(p.Kind)]++
	}
	for _, kind := range pendingTiers {
		tier := tierOf(kind)
		m.changePending.WithLabelValues(tier).Set(pending[tier])
	}

	// SpecConverged is 1 when True and 0 when False. When the
	// condition is Unknown or missing, the series is absent, because
	// Unknown is not the same as not converged.
	m.converged.clear()
	if c := api.FindCondition(status.Conditions, "SpecConverged"); c != nil {
		switch c.Status {
		case api.ConditionTrue:
			m.converged.set(1)
		case api.ConditionFalse:
			m.converged.set(0)
		}
	}

	m.lastCrash.clear()
	if status.LastCrash != nil && status.LastCrash.Time != nil && !status.LastCrash.Time.IsZero() {
		m.lastCrash.set(float64(status.LastCrash.Time.Unix()))
	}
}

func (m *machineMetrics) observeDevices(devices []kubernetes.SliceDevice) {
	counts := map[string]float64{}
	for _, d := range devices {
		counts[deviceClass(d)]++
	}
	for class := range m.classes {
		if _, held := counts[class]; !held {
			counts[class] = 0
		}
	}
	for class, count := range counts {
		m.classes[class] = true
		m.devices.WithLabelValues(class).Set(count)
	}
}

// deviceClass returns the device's class attribute. The class names
// come from a fixed table (hardware/names.go), so the label has a
// bounded set of values. A device with no class counts as "unknown",
// so the counts add up to the number of devices in the slice.
func deviceClass(d kubernetes.SliceDevice) string {
	if attr, held := d.Attributes["class"]; held && attr.String != nil && *attr.String != "" {
		return *attr.String
	}
	return "unknown"
}

// machineKind is the resource kind that the loop reconciles. It is the
// kind label of the layer 2 metrics.
const machineKind = "Machine"
