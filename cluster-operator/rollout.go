package main

// The rollout conductor is how the cluster sequences its fleet's
// reboots.
//
// A staged change that needs a reboot is drift on every affected
// machine at once. If each machine rebooted the moment it was ready,
// they would all reboot together, taking the whole fleet down at
// once, which risks quorum. Kubernetes workloads get protection from
// this through PodDisruptionBudgets and kubectl drain; machines need
// the same protection, so the Cluster carries a
// machine-level maxUnavailable (spec.disruption) and this conductor
// hands out reboot turns one budget-slot at a time.
//
// The coordination happens through conditions. A machine that wants
// to reboot says so with reason AwaitingTurn on its convergence
// condition and waits. The conductor responds by writing a
// RebootApproved condition onto that Machine. That condition is a
// grant: present while the turn is extended, removed when it is
// spent, and never set to False. Writing one condition type you own
// onto an object another controller manages is the native arrangement:
// the scheduler writes PodScheduled onto Pods the kubelet owns. The
// machine's own operator carries the grant along untouched (its status
// writes preserve condition types it doesn't set) and acts on it:
// cordon, drain, reboot (drain.go).
//
// The budget counts all unavailability, planned or not. A machine
// that is Lost or Degraded occupies a slot just like one that is
// rebooting on request, so a fleet that already has machines down
// pauses its own rollout instead of making things worse. The leaders
// have a stricter floor that no budget can override: only one leader
// may be down or granted at a time, because the datastore keeps
// quorum only while a majority of leaders is up, and letting a second
// leader go down could break that majority.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/chrisguidry/liken/kubernetes"
	"github.com/chrisguidry/liken/machine"
)

// rolloutStallAfter is how long a granted machine may be unavailable
// before the rollout declares itself stalled: long compared to a
// normal reboot, which takes a couple of minutes, and short compared
// to how long a person takes to notice a machine that never came
// back. While the rollout is stalled, no new turns are granted. A
// halted rollout someone can see is better than an automated one that
// keeps granting turns across a fleet whose machines are not coming
// back.
const rolloutStallAfter = 10 * time.Minute

// A rollout is one sweep's sequencing verdict: which machines to
// grant a reboot turn, which spent grants to take back, and the
// Progressing condition that reports the rollout on the Cluster. The
// condition deliberately uses the Deployment vocabulary: Progressing,
// with False meaning the rollout has stopped making progress.
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
// now. Blocked and UpdatePending machines are up; their trouble is
// administrative, not operational. Everything else that isn't Ready
// is either absent or unwell.
func available(phase machine.Phase) bool {
	switch phase {
	case machine.PhaseReady, machine.PhaseUpdatePending, machine.PhaseBlocked:
		return true
	}
	return false
}

// decideRollout is the conductor's whole decision, pure over the same
// inputs the fleet sweep reads. The order of granting is workers
// first, then leaders, each in name order. Workers go first because a
// mistake with a worker costs little, while every leader carries a
// share of quorum. Name order makes the decision deterministic, so
// two sweeps of the same fleet agree.
func decideRollout(machines []machine.Machine, renewals map[string]time.Time, cluster *machine.Cluster, now time.Time) rollout {
	var r rollout
	inFlight := 0 // budget slots occupied: unavailable machines and unspent grants
	leaderBusy := false
	var stalled, inProgress, waiting, workers, leaders []string

	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		leader := slices.Contains(cluster.Spec.Leaders, name)
		phase := effectivePhase(m, renewals, now)
		grant := machine.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition)

		switch {
		case grant != nil && available(phase) && !wantsTurn(m):
			// The turn is spent (the machine converged) or was never
			// used (the edit reverted, the policy went Manual): either
			// way the machine is back and no longer asking, so the
			// grant returns to the budget.
			r.revoke = append(r.revoke, name)
		case grant != nil && !available(phase) && now.Sub(grant.LastTransitionTime) > rolloutStallAfter:
			// This machine was granted a turn, went down, and has
			// stayed down too long. That is no longer a reboot in
			// progress; it is an outage.
			stalled = append(stalled, name)
			inFlight++
		case grant != nil || !available(phase):
			// A machine mid-turn or unwell occupies a budget slot
			// either way: the budget counts unavailability itself,
			// not whether it was planned.
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
	for _, name := range workers {
		if len(stalled) > 0 || capacity <= 0 {
			waiting = append(waiting, name)
			continue
		}
		r.grant = append(r.grant, name)
		inProgress = append(inProgress, name)
		capacity--
	}
	for _, name := range leaders {
		if len(stalled) > 0 || capacity <= 0 || leaderBusy {
			waiting = append(waiting, name)
			continue
		}
		r.grant = append(r.grant, name)
		inProgress = append(inProgress, name)
		capacity--
		leaderBusy = true
	}

	switch {
	case len(stalled) > 0:
		r.progressing = machine.Condition{
			Type: "Progressing", Status: machine.ConditionFalse, Reason: "RolloutStalled",
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
			Type: "Progressing", Status: machine.ConditionTrue, Reason: "RollingOut", Message: message,
		}
	default:
		r.progressing = machine.Condition{
			Type: "Progressing", Status: machine.ConditionTrue, Reason: "RolloutComplete",
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
func carryOutRollout(c *kubernetes.Client, machines []machine.Machine, r rollout, now time.Time) {
	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		status := m.Status
		switch {
		case slices.Contains(r.grant, name):
			status.Conditions = machine.SetCondition(slices.Clone(m.Status.Conditions), machine.Condition{
				Type: machine.RebootApprovedCondition, Status: machine.ConditionTrue, Reason: "DisruptionBudgetAllows",
				ObservedGeneration: m.Metadata.Generation,
				Message:            "the cluster's disruption budget allows this machine to take its reboot turn now",
			}, now)
			if err := kubernetes.PublishStatus(c, m, &status); err != nil {
				fmt.Printf("granting %s its reboot turn: %v\n", name, err)
			} else {
				fmt.Printf("granted %s its reboot turn\n", name)
			}
		case slices.Contains(r.revoke, name):
			status.Conditions = machine.RemoveCondition(slices.Clone(m.Status.Conditions), machine.RebootApprovedCondition)
			if err := kubernetes.PublishStatus(c, m, &status); err != nil {
				fmt.Printf("reclaiming %s's reboot turn: %v\n", name, err)
			}
		}
	}
}
