package main

// Layer 3 for the cluster operator: the metrics about the fleet.
//
// Each machine's operator publishes its own facts, and this program
// publishes the three that only a program which sees every machine
// at once can state. Every one of them comes from the sweep's own
// verdict, the value that this same pass writes onto the Cluster's
// status, so a graph and a `kubectl get cluster` can never disagree
// (fleet.go and rollout.go).

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
	"github.com/liken-sh/liken/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// clusterKind is the resource this operator's loop reconciles, and
// the label its layer 2 duration and error series carry.
const clusterKind = "Cluster"

// machineKind is what this operator watches. The watch spans the
// whole fleet, because any machine's transition can change the
// Cluster's phase, so the watch restart counter carries this kind
// and the reconcile counters carry Cluster.
const machineKind = "Machine"

// serveMetrics builds this operator's whole registry, the contract's
// layer 1 and layer 2 beside the fleet's layer 3, and starts the
// listener that answers a scrape. A listener that cannot bind
// reports itself and nothing more: the fleet must keep converging
// whether or not anybody watches it.
func serveMetrics(address string) (*metrics.Operator, *clusterMetrics) {
	o := metrics.NewOperator(component, machine.Version,
		[]string{clusterKind}, []string{machineKind})
	layer := newClusterMetrics(o)
	if addr, err := o.Serve(address); err != nil {
		fmt.Fprintf(os.Stderr, "the metrics listener is not serving: %v\n", err)
	} else if addr != nil {
		fmt.Printf("serving metrics on %s/metrics\n", addr)
	}
	return o, layer
}

// clusterMetrics holds this operator's layer 3.
type clusterMetrics struct {
	machines  *prometheus.GaugeVec
	approvals prometheus.Gauge
	behind    prometheus.Gauge
}

// newClusterMetrics registers layer 3 on the operator's registry.
// The machine gauge starts with a zero for every phase liken defines.
// A fleet reports nothing at all in a phase that no machine holds, so
// without these zeros the first machine to go Lost would draw its
// first point with no earlier point to fall from.
func newClusterMetrics(o *metrics.Operator) *clusterMetrics {
	m := &clusterMetrics{
		machines: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metrics.Prefix + "machines",
			Help: "Machines in the fleet, by the phase the sweep judged them to hold.",
		}, []string{"phase"}),
		approvals: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: metrics.Prefix + "disruption_approvals_pending",
			Help: "Machines that hold a staged change and wait for a person to approve it.",
		}),
		behind: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: metrics.Prefix + "machines_behind_target",
			Help: "Machines that do not run the release the Cluster's spec targets.",
		}),
	}
	o.Registry().MustRegister(m.machines, m.approvals, m.behind)
	for _, phase := range fleetPhases {
		m.machines.WithLabelValues(string(phase)).Set(0)
	}
	return m
}

// fleetPhases is the phase vocabulary a machine can hold. The api
// package defines it and the CRD's enum enforces it, so this label
// is bounded by a list that only a schema change can grow.
var fleetPhases = []api.Phase{
	api.PhaseReady,
	api.PhaseBooting,
	api.PhaseUpdating,
	api.PhaseUpdatePending,
	api.PhaseBlocked,
	api.PhaseDegraded,
	api.PhaseLost,
	api.PhaseUnknown,
}

// observeSweep publishes the verdict that this pass is about to
// write onto the Cluster. The phases are the effective phases, the
// ones the sweep judged from each machine's status and its
// heartbeat, so a machine that has gone silent counts as Lost here
// at the same moment it counts as Lost in the headcount.
func (m *clusterMetrics) observeSweep(s fleetSweep, r rollout) {
	counts := map[api.Phase]int{}
	for phase, count := range s.phases {
		// A Machine that exists and has never published a status
		// holds no phase at all. Unknown is what that machine is:
		// the fleet has an object for it and no observation of it.
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
