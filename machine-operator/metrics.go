package main

// Layer 3 for the machine operator: the metrics that make this
// program what it is.
//
// Most of these are Machine status fields, and that is the design.
// The status says what the machine is now, and the metric is the
// same fact over time. Console parity says that what init prints
// must also reach Machine status. This file extends that one step
// further, to Prometheus, under one rule: every metric here reads
// the fact that the status writer reads, and never a second source.
// So observeStatus takes the very status value that reconcile is
// about to publish, and observeDevices takes the very device list
// that the ResourceSlice is about to carry.
//
// Three metrics from the plan's table are missing, because the
// machine holds no fact for them yet:
//
//   - liken_upgrade_duration_seconds. Measuring an upgrade from
//     staged to booted needs a staging time that survives the
//     reboot, and the system release record carries no timestamp.
//   - liken_firmware_info and liken_firmware_update_pending. Status
//     reports the firmware's boot entries and nothing about its
//     version. Reading a BIOS version, and updating one, is
//     plans/33-firmware-updates.md.

import (
	"fmt"
	"os"
	"strings"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
	"github.com/liken-sh/liken/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// serveMetrics builds this operator's whole registry, the contract's
// layer 1 and layer 2 beside this machine's layer 3, and starts the
// listener that answers a scrape. A listener that cannot bind
// reports itself and nothing more: the machine must keep operating
// whether or not anybody watches it. This pod runs on the host
// network, so a port another program already holds is the ordinary
// way that bind fails.
func serveMetrics(address string, f *fetcher) (*metrics.Operator, *machineMetrics) {
	o := metrics.NewOperator(component, machine.Version,
		[]string{machineKind}, []string{machineKind})
	layer := newMachineMetrics(o, f)
	if addr, err := o.Serve(address); err != nil {
		fmt.Fprintf(os.Stderr, "the metrics listener is not serving: %v\n", err)
	} else if addr != nil {
		fmt.Printf("serving metrics on %s/metrics\n", addr)
	}
	return o, layer
}

// machineMetrics holds this operator's layer 3. One instance lives
// for the life of the process, and the reconcile pass hands it the
// facts it has already gathered.
type machineMetrics struct {
	release       *prometheus.GaugeVec
	bootTimestamp optionalGauge
	changePending *prometheus.GaugeVec
	converged     optionalGauge
	devices       *prometheus.GaugeVec
	lastCrash     optionalGauge

	// classes remembers every device class this machine has
	// published. A class whose devices all disappear is set to zero
	// rather than removed, because the plan asks for a drop that a
	// graph can show, and a series that stops reporting draws a gap
	// instead of a fall.
	classes map[string]bool
}

// An optionalGauge is a gauge that a pass may leave unpublished. A
// machine with no crash on record must publish no crash timestamp,
// because zero reads as 1970 on a graph, while an absent series
// reads as "nothing to report", which is the fact. A GaugeVec with
// no labels gives exactly that: one sample while a pass sets it, and
// no metric family at all while nothing does.
type optionalGauge struct{ vec *prometheus.GaugeVec }

func newOptionalGauge(name, help string) optionalGauge {
	return optionalGauge{vec: prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: name, Help: help}, nil)}
}

func (g optionalGauge) set(value float64) { g.vec.WithLabelValues().Set(value) }

func (g optionalGauge) clear() { g.vec.Reset() }

// tierOf turns a staged disruption's kind into the tier word the
// contract publishes. liken has four tiers of convergence
// (cluster/changes.go), and only two of them ever wait: an in-place
// change applies on the pass that finds it, and a next-boot change
// waits for no disruption of its own.
func tierOf(kind machine.DisruptionKind) string {
	return strings.ToLower(string(kind))
}

// pendingTiers are the tiers that can hold a change back. Both are
// published on every pass, so a fleet that is waiting for nothing
// reports zero rather than nothing at all.
var pendingTiers = []machine.DisruptionKind{machine.DisruptionReboot, machine.DisruptionRestart}

// newMachineMetrics registers layer 3 on the operator's registry.
// The fetcher is a parameter because two of these metrics are
// counters that only the fetcher can total: it is the one thing that
// downloads a release, and it keeps those totals across the passes
// that ask it for its state (fetch.go).
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
			"1 while the machine runs the spec the cluster holds, and 0 while it does not."),
		devices: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.Prefix + "devices",
			Help: "Devices this node offers to workloads, by class.",
		}, []string{"class"}),
		lastCrash: newOptionalGauge(metrics.Prefix+"last_crash_timestamp_seconds",
			"When the newest kernel crash this machine still holds records for happened, in seconds since the epoch."),
		classes: map[string]bool{},
	}

	// The two download counters read the fetcher's own totals at
	// scrape time. A counter function suits them because the fetcher
	// already keeps these numbers for its own use, and copying them
	// into a second place on every pass would let the two disagree.
	// Reading them costs one mutex and no I/O, so a scrape still
	// touches nothing but memory.
	downloadBytes := prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: metrics.Prefix + "release_download_bytes_total",
		Help: "Release artifact bytes this machine has downloaded and verified onto a slot.",
	}, func() float64 { return float64(f.DownloadedBytes()) })
	downloadFailures := prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: metrics.Prefix + "release_download_failures_total",
		Help: "Release downloads that ended without a complete slot.",
	}, func() float64 { return float64(f.DownloadFailures()) })

	o.Registry().MustRegister(m.release, m.bootTimestamp.vec, m.changePending,
		m.converged.vec, m.devices, m.lastCrash.vec, downloadBytes, downloadFailures)

	for _, kind := range pendingTiers {
		m.changePending.WithLabelValues(tierOf(kind)).Set(0)
	}
	return m
}

// observeStatus reads the status that this pass is about to publish.
// Every value here comes from that one struct, so the graph and the
// `kubectl get machine -o yaml` output can never disagree.
func (m *machineMetrics) observeStatus(status *machine.MachineStatus) {
	// Reset before the set, because a machine that upgrades or falls
	// back changes both labels. Without the reset, the old release
	// would keep reporting 1 beside the new one, and a fleet panel
	// would count the machine twice.
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

	// SpecConverged is the condition that answers "does this machine
	// run the spec the cluster holds". A pass that could not judge it
	// publishes nothing, because Unknown is not 0.
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

// observeDevices counts the devices this node offers to workloads,
// by class. It takes the list that the ResourceSlice carries, so the
// count and the offer come from the one sysfs walk the pass made.
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

// deviceClass reads the class word a slice device publishes. The
// buses name a class in a table with a fixed set of words
// (hardware/names.go), so this label stays bounded. A device on a
// bus whose table has no word for it counts as unknown, rather than
// leaving the totals short of the slice.
func deviceClass(d kubernetes.SliceDevice) string {
	if attr, held := d.Attributes["class"]; held && attr.String != nil && *attr.String != "" {
		return *attr.String
	}
	return "unknown"
}

// machineKind is the resource kind this operator's loop reconciles,
// and the label every layer 2 series carries.
const machineKind = "Machine"
