package main

// The rollout conductor: how the cluster sequences its fleet's reboots.
//
// A staged change that needs a reboot is drift on every affected
// machine at once, and machines that reboot the moment they're ready
// reboot together — a whole fleet down simultaneously, quorum
// surviving on luck. Kubernetes workloads get protection from this
// through PodDisruptionBudgets and kubectl drain; machines deserve the
// same, so the Cluster carries a machine-level maxUnavailable
// (spec.disruption) and the sweep leader hands out reboot turns one
// budget-slot at a time.
//
// The conversation is held in conditions. A machine that wants to
// reboot says so with reason AwaitingTurn on its convergence condition
// and waits. The conductor answers by writing a RebootApproved
// condition onto that Machine — a grant, present while extended and
// removed when spent, never False. Writing one condition type you own
// onto an object another controller manages is the native arrangement:
// the scheduler writes PodScheduled onto Pods the kubelet owns. The
// machine's own operator carries the grant along untouched (its status
// writes preserve condition types it doesn't set) and acts on it:
// cordon, drain, reboot (drain.go).
//
// The budget counts *all* unavailability, planned or not. A machine
// that is Lost or Degraded occupies a slot just like one that is
// rebooting on request, so a fleet that is already hurting pauses its
// own rollout instead of making things worse. The leaders have a
// stricter floor that no budget can override: only one leader may be
// down or granted at a time, because the datastore's quorum is
// arithmetic, and this is the arithmetic.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// rolloutStallAfter is how long a granted machine may be unavailable
// before the rollout declares itself stalled: generous next to a
// normal reboot (minutes), short next to a human noticing a machine
// that never came back. While stalled, no new turns are granted — a
// halted rollout someone can see beats an automated one that keeps
// marching through a fleet that isn't coming back.
const rolloutStallAfter = 10 * time.Minute

// rebootApprovedCondition is the grant's condition type, owned by the
// conductor: the machine's own operator never sets or clears it.
const rebootApprovedCondition = "RebootApproved"

// A rollout is one sweep's sequencing verdict: which machines to
// grant a reboot turn, which spent grants to take back, and the
// Progressing condition that tells the Cluster's rollout story —
// deliberately the Deployment vocabulary (Progressing, with False
// meaning the rollout has stopped making progress).
type rollout struct {
	grant       []string
	revoke      []string
	progressing machine.Condition
}

// wantsTurn reports whether any of the machine's conditions carry the
// AwaitingTurn reason: staged, willing (rebootPolicy Auto), and
// waiting only for the cluster's go-ahead.
func wantsTurn(m *machine.Machine) bool {
	for _, c := range m.Status.Conditions {
		if c.Reason == "AwaitingTurn" {
			return true
		}
	}
	return false
}

// available reports whether a machine is serving the cluster right
// now. Blocked and UpdatePending machines are up — their trouble is
// administrative, not operational — while everything else that isn't
// Ready is some flavor of absent or unwell.
func available(phase machine.Phase) bool {
	switch phase {
	case machine.PhaseReady, machine.PhaseUpdatePending, machine.PhaseBlocked:
		return true
	}
	return false
}

// decideRollout is the conductor's whole decision, pure over the same
// inputs the fleet sweep reads. The order of granting is workers
// first, then leaders, each in name order: workers are cheap to be
// wrong about, leaders each carry a share of quorum, and determinism
// means two sweeps of the same fleet agree.
func decideRollout(machines []machine.Machine, renewals map[string]time.Time, cluster *machine.Cluster, self string, now time.Time) rollout {
	var r rollout
	inFlight := 0 // budget slots occupied: unavailable machines and unspent grants
	leaderBusy := false
	var stalled, inProgress, waiting, workers, leaders []string

	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		leader := slices.Contains(cluster.Spec.Leaders, name)
		phase := effectivePhase(m, renewals, self, now)
		grant := machine.FindCondition(m.Status.Conditions, rebootApprovedCondition)

		switch {
		case grant != nil && available(phase) && !wantsTurn(m):
			// The turn is spent (the machine converged) or was never
			// used (the edit reverted, the policy went Manual): either
			// way the machine is back and no longer asking, so the
			// grant returns to the budget.
			r.revoke = append(r.revoke, name)
		case grant != nil && !available(phase) && now.Sub(grant.LastTransitionTime) > rolloutStallAfter:
			// Granted, gone, and gone too long: this is no longer a
			// reboot, it's an outage wearing a grant.
			stalled = append(stalled, name)
			inFlight++
		case grant != nil || !available(phase):
			// A machine mid-turn or unwell occupies a budget slot
			// either way — the budget counts absence, not intent.
			inFlight++
			if grant != nil {
				inProgress = append(inProgress, name)
			}
			if leader {
				leaderBusy = true
			}
		case wantsTurn(m):
			if leader {
				leaders = append(leaders, name)
			} else {
				workers = append(workers, name)
			}
		}
	}

	slices.Sort(workers)
	slices.Sort(leaders)
	capacity := cluster.Spec.Disruption.MaxUnavailableOrDefault() - inFlight
	for _, name := range slices.Concat(workers, leaders) {
		leader := slices.Contains(cluster.Spec.Leaders, name)
		switch {
		case len(stalled) > 0 || capacity <= 0 || (leader && leaderBusy):
			waiting = append(waiting, name)
		default:
			r.grant = append(r.grant, name)
			inProgress = append(inProgress, name)
			capacity--
			if leader {
				leaderBusy = true
			}
		}
	}

	switch {
	case len(stalled) > 0:
		r.progressing = machine.Condition{
			Type: "Progressing", Status: "False", Reason: "RolloutStalled",
			Message: fmt.Sprintf("granted a reboot turn more than %s ago and not back: %s; no further turns until it returns",
				rolloutStallAfter, strings.Join(stalled, ", ")),
		}
	case len(inProgress)+len(waiting) > 0:
		message := "taking a reboot turn: " + strings.Join(inProgress, ", ")
		if len(inProgress) == 0 {
			message = "reboot turns are waiting on the disruption budget"
		}
		if len(waiting) > 0 {
			message += "; waiting: " + strings.Join(waiting, ", ")
		}
		r.progressing = machine.Condition{
			Type: "Progressing", Status: "True", Reason: "RollingOut", Message: message,
		}
	default:
		r.progressing = machine.Condition{
			Type: "Progressing", Status: "True", Reason: "RolloutComplete",
			Message: "no machines are waiting for a reboot turn",
		}
	}
	return r
}

// carryOutRollout writes the verdict onto the fleet: grants appear on
// the named Machines, spent grants disappear. These are writes to
// other machines' statuses, safe for the same reason the Lost write
// is: each touches only the one condition type this writer owns, and
// a 409 from a crossing write just waits for the next sweep.
func carryOutRollout(c *apiClient, machines []machine.Machine, r rollout, now time.Time) {
	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		status := m.Status
		switch {
		case slices.Contains(r.grant, name):
			status.Conditions = machine.SetCondition(slices.Clone(m.Status.Conditions), machine.Condition{
				Type: rebootApprovedCondition, Status: "True", Reason: "DisruptionBudgetAllows",
				ObservedGeneration: m.Metadata.Generation,
				Message:            "the cluster's disruption budget allows this machine to take its reboot turn now",
			}, now)
			if err := publishStatus(c, m, &status); err != nil {
				fmt.Printf("granting %s its reboot turn: %v\n", name, err)
			} else {
				fmt.Printf("granted %s its reboot turn\n", name)
			}
		case slices.Contains(r.revoke, name):
			status.Conditions = machine.RemoveCondition(slices.Clone(m.Status.Conditions), rebootApprovedCondition)
			if err := publishStatus(c, m, &status); err != nil {
				fmt.Printf("reclaiming %s's reboot turn: %v\n", name, err)
			}
		}
	}
}
