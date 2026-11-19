package main

// reconcileFleet runs one reconcile pass over the fleet. It reads every
// Machine and heartbeat Lease, marks the machines that stopped renewing
// their Lease as Lost, grants reboot turns, and writes the result to the
// Cluster's status.
//
// A dead machine cannot report that it is dead, so its last written
// status stays in the API and reads Ready indefinitely. Each machine's
// operator renews a heartbeat lease (see the kubernetes package), the
// way a kubelet renews its node lease, and the pass marks a machine
// Lost when its lease stops being renewed.
//
// Each Machine's status belongs to that machine's operator, so the
// pass writes the Lost phase only to a machine whose heartbeat is
// already stale. A stale heartbeat means the machine's operator has
// stopped writing, so the two writers do not write at the same time.
// When the machine returns, its operator's next pass overwrites the
// Lost phase with what it observes.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// A fleetDecision is what one pass decides about the whole fleet: the
// machines to mark Lost, the ready count for the Cluster's status, the
// MachinesReady condition, and the Cluster's phase. decideFleet
// computes it and reconcileFleet writes it.
type fleetDecision struct {
	lost          []string
	machineCounts cluster.MachineTally
	condition     api.Condition
	phase         api.Phase

	// The counts that the fleet's metrics publish (metrics.go).
	phases    map[api.Phase]int
	approvals int
}

// decideFleet uses each machine's effective phase (effectivePhase), so
// a machine counts as ready only when its phase is Ready and its
// heartbeat is fresh. A machine that is not ready is either updating
// (Updating, UpdatePending, Downloading, or Booting) or degraded (Lost,
// Blocked, or any other phase). When any machine is degraded the fleet
// is Degraded, otherwise when any is updating it is Updating. The
// MachinesReady condition names
// those machines. changedAt maps each lease name to the time this
// program last saw its renewTime change (heartbeatSightings.changeTimes).
func decideFleet(machines []machine.Machine, changedAt map[string]time.Time, now time.Time) fleetDecision {
	s := fleetDecision{machineCounts: cluster.MachineTally{Total: len(machines)}, phases: map[api.Phase]int{}}
	var updating, degraded []string
	for i := range machines {
		m := &machines[i]
		effective := effectivePhase(m, changedAt, now)
		if effective == api.PhaseLost && m.Status.Phase != api.PhaseLost {
			s.lost = append(s.lost, m.Metadata.Name)
		}
		s.phases[effective]++
		if awaitsApproval(m) {
			s.approvals++
		}

		switch effective {
		case api.PhaseReady:
			s.machineCounts.Ready++
		case api.PhaseUpdating, api.PhaseUpdatePending, api.PhaseDownloading, api.PhaseBooting:
			updating = append(updating, m.Metadata.Name)
		default:
			degraded = append(degraded, m.Metadata.Name)
		}
	}
	s.machineCounts.Summary = fmt.Sprintf("%d/%d", s.machineCounts.Ready, s.machineCounts.Total)

	switch {
	case len(degraded) > 0:
		s.phase = api.PhaseDegraded
		s.condition = api.Condition{
			Type: "MachinesReady", Status: api.ConditionFalse, Reason: "MachinesDegraded",
			Message: fmt.Sprintf("%s machines are ready. Not ready and not updating: %s", s.machineCounts.Summary, strings.Join(degraded, ", ")),
		}
	case len(updating) > 0:
		s.phase = api.PhaseUpdating
		s.condition = api.Condition{
			Type: "MachinesReady", Status: api.ConditionFalse, Reason: "MachinesUpdating",
			Message: fmt.Sprintf("%s machines are ready. Updating: %s", s.machineCounts.Summary, strings.Join(updating, ", ")),
		}
	default:
		s.phase = api.PhaseReady
		s.condition = api.Condition{
			Type: "MachinesReady", Status: api.ConditionTrue, Reason: "AllMachinesReady",
			Message: fmt.Sprintf("All %d machines are ready", s.machineCounts.Total),
		}
	}
	return s
}

// awaitsApproval reports whether the machine has a staged change that
// waits for a person to approve the reboot or restart. A machine on
// the Manual reboot policy stages its change and then stops, with the
// reason RebootPending or RestartPending on a condition.
func awaitsApproval(m *machine.Machine) bool {
	for _, c := range m.Status.Conditions {
		if c.Reason == "RebootPending" || c.Reason == "RestartPending" {
			return true
		}
	}
	return false
}

// available is the channel poller's latest version. probe and
// overduePods keep state between passes.
//
// It returns an error only when a read that the decision needs fails:
// the Machines, their heartbeat leases, or the Disruptions and their
// budgets. The operator's metrics count that error as a
// failed reconcile (metrics.go).
//
// The Cluster's status is written before any Machine, because a
// Machine gets a reboot grant only after the API server has stored
// the grant's turn in the Cluster's status.rebootTurns (rollout.go).
func reconcileFleet(reads *fleetReader, clusterDoc *cluster.Cluster, available string, probe *fluxProbe,
	overduePods *overduePodReports, cm *clusterMetrics, now time.Time) error {
	c := reads.client
	machines, err := reads.machines()
	if err != nil {
		fmt.Printf("the pass stopped while listing the Machines: %v\n", err)
		return err
	}
	// The rollout releases a turn by comparing its Machine's current
	// resourceVersion with the turn's, so readTurns reads those
	// Machines from the API server instead of the watch cache.
	machines, err = reads.readTurns(clusterDoc.Status.RebootTurns, machines)
	if err != nil {
		fmt.Printf("the pass stopped while reading the Machines that have reboot turns: %v\n", err)
		return err
	}
	changedAt, err := reads.heartbeats(now)
	if err != nil {
		fmt.Printf("the pass stopped while listing the heartbeat Leases: %v\n", err)
		return err
	}
	requests, err := reads.requests()
	if err != nil {
		fmt.Printf("the pass stopped while %v\n", err)
		return err
	}
	s := decideFleet(machines, changedAt, now)

	// appliedVersion is the release in the machine-operator
	// DaemonSet's template. Only a leader's boot updates that
	// template, so while it is older than the target release,
	// decideRollout reboots a leader first. When the DaemonSet cannot
	// be read, the pass grants no turns.
	appliedVersion, err := daemonSetVersion(reads, machineOperatorDaemonSet)
	r := decideRollout(machines, changedAt, reads.sightings.firstReadTime(), clusterDoc, appliedVersion, requests, now)
	if err != nil {
		fmt.Printf("the pass grants no reboot turns, because it cannot read the DaemonSet %s: %v\n", machineOperatorDaemonSet, err)
		r = r.withoutGrants(err)
	}

	// The OS's own pods, the operators and the log relay, keep their
	// old release after a machine upgrades until refreshOSPods evicts
	// them (steward.go).
	refreshOSPods(reads, machines, overduePods, now)

	// k3s does not delete an addon's objects when init removes its
	// manifest file, so this program deletes the workloads of retracted
	// features (janitor.go). tearDownFlux removes Flux one stage per
	// pass, and returns a condition for the Cluster when it refuses
	// because `liken` did not install that Flux.
	deleteRetractedWorkloads(reads, clusterDoc)
	fluxTeardown := tearDownFlux(c, clusterDoc)

	// The deploy key is created once and read on every pass, so the
	// status always has its public key (flux.go). The Flux controllers
	// are checked at most once each fluxProbeInterval, and created
	// again when they are missing.
	publicKey := ensureFluxDeployKey(c, reads.recorder, clusterDoc)
	if manifest, err := fluxManifest(); err != nil {
		if clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
			fmt.Printf("the Flux controllers are not checked, because this binary has no embedded Flux manifest, as in a go build outside make: %v\n", err)
		}
	} else {
		ensureFluxControllers(c, reads.recorder, clusterDoc, manifest, probe, now)
	}

	stored := publishClusterStatus(reads, clusterDoc, s, r, fluxTeardown, available, publicKey, now)
	carryOutRollout(reads, machines, r, stored, now)
	markLost(reads, machines, s.lost, changedAt, now)

	// A Disruption reports Granted only after the API server has stored
	// its turn in status.rebootTurns (requeststatus.go).
	reportRequests(reads, machines, requests, r, stored, now)
	reportBudgets(reads, clusterDoc, requests, r, now)

	cm.observePass(s, r)
	return nil
}

// markLost writes the Lost phase to each machine in lost. Each write
// that succeeds posts a MachineLost Event. The Event's message has the
// time of the last renewal that this program saw, which the Ready
// condition's message does not.
func markLost(reads *fleetReader, machines []machine.Machine, lost []string, changedAt map[string]time.Time, now time.Time) {
	for _, m := range machines {
		if !slices.Contains(lost, m.Metadata.Name) {
			continue
		}
		// Only the phase and the Ready condition change. The other
		// fields stay as the machine last wrote them.
		status := m.Status
		status.Phase = api.PhaseLost
		// SetCondition changes an existing entry in place, and
		// status.Conditions shares its array with the machines slice.
		status.Conditions = api.SetCondition(slices.Clone(m.Status.Conditions), api.Condition{
			Type: "Ready", Status: api.ConditionUnknown, Reason: "HeartbeatStale",
			ObservedGeneration: m.Metadata.Generation,
			Message:            "The heartbeat Lease of this machine's operator is stale, so the machine is presumed down",
		}, now)
		// A conflict means the machine's operator wrote its own status
		// first, so the machine is back. The pass skips it and does
		// not retry.
		if err := reads.publishStatus(&m, &status); errors.Is(err, apiclient.ErrConflict) {
			continue
		} else if err != nil {
			fmt.Printf("writing the Lost phase to Machine %s: %v\n", m.Metadata.Name, err)
		} else {
			fmt.Printf("Machine %s stopped renewing its heartbeat Lease and is now Lost\n", m.Metadata.Name)
			reads.recorder.Warning(machineReference(&m), reasonMachineLost, lostMessage(changedAt, m.Metadata.Name))
		}
	}
}

// lostMessage returns the message of a MachineLost Event. The time in
// it is when this program last saw the lease's renewTime change, on
// this program's clock, not the renewTime itself. A machine is Lost
// only when that time is more than HeartbeatStaleAfter old.
func lostMessage(changedAt map[string]time.Time, name string) string {
	renewed, ok := changedAt[name]
	if !ok {
		return "Marked the machine Lost. cluster-operator has never seen it renew a heartbeat Lease"
	}
	return "Marked the machine Lost. cluster-operator last saw its heartbeat Lease renewed at " + renewed.UTC().Format(time.RFC3339)
}

// publishClusterStatus writes the pass's result to the Cluster's
// status: the MachinesReady condition, the Progressing condition from
// the rollout, the FluxTeardown condition while tearDownFlux refuses,
// the phase, the ready count, the newest catalog version, the
// channel's available version, the flux public key, and the reboot
// turns. The pass is the only writer of these fields, and it writes
// only when one of them differs. It posts an Event for each condition
// that changed only after the write succeeds, so the next pass tries
// a failed change again with its Events.
//
// It returns the reboot turns that the API server has stored: the
// Cluster's existing turns when nothing changed, the turns in the
// write's response, or nil when the write failed. A 409 means another
// copy of the operator wrote the status after this pass read it, so
// nil makes this pass grant no turns.
func publishClusterStatus(reads *fleetReader, clusterDoc *cluster.Cluster, s fleetDecision, r rollout, fluxTeardown *api.Condition, available, publicKey string, now time.Time) []cluster.RebootTurn {
	newest := cluster.NewestVersion(clusterDoc.Spec.Releases.Catalog)
	s.condition.ObservedGeneration = clusterDoc.Metadata.Generation
	r.progressing.ObservedGeneration = clusterDoc.Metadata.Generation
	conditions := api.SetCondition(slices.Clone(clusterDoc.Status.Conditions), s.condition, now)
	conditions = api.SetCondition(conditions, r.progressing, now)
	// FluxTeardown is present only while tearDownFlux refuses.
	if fluxTeardown != nil {
		fluxTeardown.ObservedGeneration = clusterDoc.Metadata.Generation
		conditions = api.SetCondition(conditions, *fluxTeardown, now)
	} else {
		conditions = api.RemoveCondition(conditions, fluxTeardownCondition)
	}
	// An empty publicKey with the feature enabled means this pass could
	// not read the Secret, for example because its Role is not applied
	// yet or another copy of the operator created it first. The status
	// then keeps the last published key. When the feature is retracted,
	// the status drops the key, but the Secret keeps it for when the
	// feature is enabled again.
	flux := clusterDoc.Status.Flux
	if !clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
		flux = cluster.ClusterFluxStatus{}
	} else if publicKey != "" {
		flux = cluster.ClusterFluxStatus{PublicKey: publicKey}
	}
	if clusterDoc.Status.Machines != s.machineCounts || clusterDoc.Status.Phase != s.phase ||
		clusterDoc.Status.Releases.Newest != newest ||
		clusterDoc.Status.Releases.Available != available ||
		clusterDoc.Status.Flux != flux ||
		clusterDoc.Status.ObservedGeneration != clusterDoc.Metadata.Generation ||
		!sameTurns(r.turns, clusterDoc.Status.RebootTurns) ||
		!slices.Equal(conditions, clusterDoc.Status.Conditions) {
		updated := *clusterDoc
		updated.Status.Machines = s.machineCounts
		updated.Status.Phase = s.phase
		updated.Status.Releases.Newest = newest
		updated.Status.Releases.Available = available
		updated.Status.Flux = flux
		updated.Status.ObservedGeneration = clusterDoc.Metadata.Generation
		updated.Status.Conditions = conditions
		updated.Status.RebootTurns = r.turns
		stored, err := reads.publishClusterStatus(&updated)
		if err != nil {
			fmt.Printf("writing the status of Cluster %s: %v\n", clusterDoc.Metadata.Name, err)
			return nil
		}
		postClusterTransitions(reads.recorder, clusterDoc, clusterDoc.Status.Conditions, conditions)
		if stored == nil {
			return nil
		}
		if !sameTurnsIgnoringDisruptions(r.turns, stored.Status.RebootTurns) {
			return schemaPrunedTurns(reads, stored, now)
		}
		return stored.Status.RebootTurns
	}
	return clusterDoc.Status.RebootTurns
}

// schemaPrunedTurns handles a Cluster write whose response does not
// have the reboot turns that it sent. The served CRD then does not
// declare status.rebootTurns, and the API server dropped the field.
// This happens when a newer cluster-operator runs against an older
// CRD: just after the first leader boots a new release, or when a
// leader on an older release restarts k3s and applies its own CRD.
// It writes a Progressing message that says turns wait for the newer
// schema, and returns the stored turns, so the pass grants no turns
// until the schema is served. The reason stays RollingOut, so this
// write is not a condition transition and posts no Event.
func schemaPrunedTurns(reads *fleetReader, stored *cluster.Cluster, now time.Time) []cluster.RebootTurn {
	fmt.Println("the served Cluster schema has no status.rebootTurns, so the pass grants no reboot turn")
	updated := *stored
	updated.Status.Conditions = api.SetCondition(slices.Clone(stored.Status.Conditions), api.Condition{
		Type: "Progressing", Status: api.ConditionTrue, Reason: "RollingOut",
		ObservedGeneration: stored.Metadata.Generation,
		Message: "Reboot turns wait for a Cluster schema that stores status.rebootTurns. " +
			"The served CustomResourceDefinition is from an older release",
	}, now)
	if _, err := reads.publishClusterStatus(&updated); err != nil {
		fmt.Printf("writing the status of Cluster %s: %v\n", stored.Metadata.Name, err)
	}
	return stored.Status.RebootTurns
}

// sameTurns reports whether two lists of reboot turns have the same
// entries in the same order, including each entry's Disruptions. It
// compares times with Equal, because a time decoded from the API
// server can have a different location from one the pass created.
func sameTurns(a, b []cluster.RebootTurn) bool {
	return slices.EqualFunc(a, b, func(x, y cluster.RebootTurn) bool {
		return sameTurn(x, y) && slices.Equal(x.Disruptions, y.Disruptions)
	})
}

// sameTurnsIgnoringDisruptions reports whether two lists of reboot turns have the same
// entries, ignoring each entry's Disruptions. A CRD without the
// Disruptions field stores the entries and drops their Disruptions.
// The entries alone enforce the disruption budget, so the pass still
// grants turns, and it writes the Disruptions again on each pass
// until a leader boots and serves the newer schema.
func sameTurnsIgnoringDisruptions(a, b []cluster.RebootTurn) bool {
	return slices.EqualFunc(a, b, sameTurn)
}

// sameTurn reports whether two reboot turns are for the same Machine
// at the same resourceVersion and start time.
func sameTurn(x, y cluster.RebootTurn) bool {
	return x.Machine == y.Machine && x.MachineResourceVersion == y.MachineResourceVersion && x.Since.Equal(y.Since)
}
