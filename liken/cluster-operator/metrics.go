package main

// The fleet gauges are the cluster operator's domain metrics. The
// liken/metrics package serves the runtime and reconcile series of
// every operator, and each program registers its own domain gauges.
// machine-operator publishes the metrics of its own machine, and these
// gauges count across every Machine. They are set from the same fleetDecision and rollout that the
// pass writes to the Cluster's status, so a graph and
// `kubectl get cluster` agree.

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// clusterKind is the `kind` label of the reconcile and watch series,
// and the Kind of an Event about the Cluster.
const clusterKind = "Cluster"

// machineKind is the `kind` label of the Machine watch series, and the
// Kind of an Event about a Machine.
const machineKind = "Machine"

// serveMetrics builds the operator's registry with the runtime,
// reconcile, and fleet series and starts the listener that serves it. When the listener cannot
// bind, it prints the error and the operator keeps running, because
// the fleet must converge whether or not anything scrapes it.
func serveMetrics(address string) (*metrics.Operator, *clusterMetrics) {
	o := metrics.NewOperator(component, machine.Version,
		[]string{clusterKind}, watchKinds)
	layer := newClusterMetrics(o)
	if addr, err := o.Serve(address); err != nil {
		fmt.Fprintf(os.Stderr, "the metrics listener is not serving: %v\n", err)
	} else if addr != nil {
		fmt.Printf("serving metrics on %s/metrics\n", addr)
	}
	return o, layer
}

type clusterMetrics struct {
	machines  *prometheus.GaugeVec
	approvals prometheus.Gauge
	behind    prometheus.Gauge
}

// newClusterMetrics registers the fleet gauges and sets the machine
// gauge to zero for every phase. Without the zeros, a
// phase with no machines has no series, so the first machine to enter
// it appears with no earlier sample to compare against.
func newClusterMetrics(o *metrics.Operator) *clusterMetrics {
	m := &clusterMetrics{
		machines: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.Prefix + "machines",
			Help: "Machines in the fleet, by the phase that cluster-operator uses for each.",
		}, []string{"phase"}),
		approvals: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: metrics.Prefix + "disruption_approvals_pending",
			Help: "Machines with a staged change that waits for a person to approve the reboot or restart.",
		}),
		behind: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: metrics.Prefix + "machines_behind_target",
			Help: "Machines that do not run the release in the Cluster's spec.version.",
		}),
	}
	o.Registry().MustRegister(m.machines, m.approvals, m.behind)
	for _, phase := range fleetPhases {
		m.machines.WithLabelValues(string(phase)).Set(0)
	}
	return m
}

// fleetPhases lists every phase a machine can have. The `phase` label
// takes only these values, because the api package defines them and
// the CRD's enum enforces them.
var fleetPhases = []api.Phase{
	api.PhaseReady,
	api.PhaseBooting,
	api.PhaseDownloading,
	api.PhaseUpdating,
	api.PhaseUpdatePending,
	api.PhaseBlocked,
	api.PhaseDegraded,
	api.PhaseLost,
	api.PhaseUnknown,
}

// observePass sets the gauges. The phases in s are effective phases,
// which decideFleet derives from each Machine's status and
// heartbeat, so a machine with an old heartbeat counts as Lost on the
// graph and on the Cluster.
func (m *clusterMetrics) observePass(s fleetDecision, r rollout) {
	counts := map[api.Phase]int{}
	for phase, count := range s.phases {
		// A Machine that has never published a status has an empty
		// phase.
		if phase == "" {
			phase = api.PhaseUnknown
		}
		counts[phase] += count
	}
	for _, phase := range fleetPhases {
		m.machines.WithLabelValues(string(phase)).Set(float64(counts[phase]))
	}
	m.approvals.Set(float64(s.approvals))
	m.behind.Set(float64(r.behind))
}
