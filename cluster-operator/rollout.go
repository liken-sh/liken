package main

// The rollout conductor: how the cluster sequences its fleet's
// reboots.
//
// A staged change that needs a reboot creates drift on every
// affected machine at once. If each machine rebooted the moment it
// was ready, they would all reboot at the same time, taking the
// whole fleet down at once, which risks quorum. Kubernetes workloads
// get protection from this problem through PodDisruptionBudgets and
// kubectl drain. Machines need the same protection, so the Cluster
// carries a machine-level maxUnavailable value (spec.disruption),
// and the conductor hands out reboot turns one budget slot at a
// time.
//
// Turns normally go to workers before leaders, because a worker
// mistake costs little while every leader carries a share of quorum.
// One case reorders that default: a version rollout, while the
// machine-operator DaemonSet's applied template lags the fleet's
// target release. Only a leader's boot rewrites the AddOn manifests
// that produce that template (cluster-operator/steward.go), so a
// follower that reboots into the new release first only extends the
// lag, running its new binary inside the old template until the pod
// steward can refresh it (the guard in machine-operator/conditions.go
// covers that follower in the meantime). Sending a leader first ends
// the lag instead, so while it lasts, the gate holds workers back and
// grants a waiting leader its turn.
//
// The coordination happens through conditions. A machine that needs
// a reboot sets reason AwaitingTurn on its convergence condition and
// waits. This program responds by writing a RebootApproved condition
// onto that Machine. That condition is a grant: it is present while
// the turn is extended, it is removed when the turn is spent, and it
// is never set to False. Writing one condition type that you own
// onto an object that another controller manages is a standard
// arrangement: the scheduler writes PodScheduled onto Pods that the
// kubelet owns. The machine's own operator carries the grant along
// untouched, because its status writes preserve condition types it
// does not set, and it acts on the grant: it cordons, drains, and
// reboots the machine (see drain.go).
//
// The budget counts all unavailability, planned or not. A machine
// that is Lost or Degraded occupies a slot just like a machine that
// is rebooting on request. So a fleet that already has machines down
// pauses its own rollout instead of making things worse. The leaders
// have a stricter floor that no budget can override: only one leader
// may be down or granted a turn at a time. The datastore keeps
// quorum only while a majority of leaders is up, and letting a
// second leader go down could break that majority.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// rolloutStallAfter sets how long a granted machine may stay
// unavailable before the rollout is marked stalled. This time is
// long compared to a normal reboot, which takes a couple of minutes.
// It is short compared to how long a person takes to notice a
// machine that never came back. While the rollout is stalled, it
// grants no new turns. A halted rollout that a person can see is
// better than an automated rollout that keeps granting turns across
// a fleet whose machines are not coming back.
const rolloutStallAfter = 10 * time.Minute

// A rollout holds one sweep's sequencing verdict: which machines to
// grant a reboot turn, which spent grants to take back, and the
// Progressing condition that reports the rollout on the Cluster. The
// condition deliberately reuses Deployment vocabulary: Progressing,
// with False meaning the rollout has stopped making progress.
type rollout struct {
	grant       []string
	revoke      []string
	progressing api.Condition
}

// wantsTurn reports whether any of the machine's conditions carry
// the AwaitingTurn reason. This reason means the machine has a
// staged change and may take its disruption as soon as the cluster
// grants a turn. That readiness comes from either of two paths: the
// machine's rebootPolicy is Auto, or a person approved the change on
// a Manual machine through the liken.sh/approve-disruption
// annotation.
func wantsTurn(m *machine.Machine) bool {
	for _, c := range m.Status.Conditions {
		if c.Reason == "AwaitingTurn" {
			return true
		}
	}
	return false
}

// available reports whether a machine is serving the cluster right
// now. Blocked and UpdatePending machines are up. Their trouble is
// administrative, not operational. Every other machine that is not
// Ready is either absent or unwell.
func available(phase api.Phase) bool {
	switch phase {
	case api.PhaseReady, api.PhaseUpdatePending, api.PhaseBlocked:
		return true
	}
	return false
}

// decideRollout computes the whole rollout decision, over the same
// inputs that the fleet sweep reads. It grants turns to workers
// first, then to leaders, each group in name order. Workers go first
// because a mistake with a worker costs little, while every leader
// carries a share of quorum. Name order makes the decision
// deterministic, so two sweeps of the same fleet agree.
//
// One exception reorders that default. appliedVersion is the release
// the machine-operator DaemonSet's template actually carries
// (daemonSetVersion, steward.go); an empty value, meaning no
// DaemonSet has applied anything yet, never triggers the exception.
// While appliedVersion lags clusterDoc.Spec.Version, and a leader
// either waits for a turn or holds an unspent grant, every waiting
// worker sits out the sweep: granting one would send it into the
// very template lag the leader's boot is about to end. The hold
// lasts through the leader's whole turn, because each sweep
// recomputes it from the grant that is still outstanding. When no
// leader waits and none holds a grant, the workers proceed in the
// default order, because holding them for a leader that cannot take
// or use a turn would stall the rollout; the guard in
// machine-operator/conditions.go is what makes a worker's lag
// survivable in that case.
func decideRollout(machines []machine.Machine, renewals map[string]time.Time, clusterDoc *cluster.Cluster, appliedVersion string, now time.Time) rollout {
	var r rollout
	inFlight := 0 // budget slots occupied: unavailable machines and unspent grants
	leaderBusy := false
	leaderAdvancing := false // a leader holds an unspent grant; its boot advances the template
	var stalled, inProgress, waiting, workers, leaders []string

	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		leader := slices.Contains(clusterDoc.Spec.Leaders, name)
		phase := effectivePhase(m, renewals, now)
		grant := api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition)

		switch {
		case grant != nil && available(phase) && !wantsTurn(m):
			// The turn is spent, because the machine converged, or it
			// was never used, because the edit was reverted or the
			// policy changed to Manual. Either way, the machine is
			// back and no longer asking, so the grant returns to the
			// budget.
			r.revoke = append(r.revoke, name)
		case grant != nil && !available(phase) && now.Sub(grant.LastTransitionTime) > rolloutStallAfter:
			// This machine was granted a turn, went down, and has
			// stayed down too long. That is no longer a reboot in
			// progress. It is an outage.
			stalled = append(stalled, name)
			inFlight++
		case grant != nil || !available(phase):
			// A machine that is mid-turn or unwell occupies a budget
			// slot either way. The budget counts unavailability
			// itself, not whether the unavailability was planned.
			inFlight++
			if grant != nil {
				inProgress = append(inProgress, name)
			}
			if leader {
				leaderBusy = true
				// A leader that holds an unspent grant is mid-turn:
				// its boot is the event that advances the applied
				// template, and the gate below keys on that. A leader
				// that is merely unavailable, with no grant, advances
				// nothing.
				leaderAdvancing = leaderAdvancing || grant != nil
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
	capacity := clusterDoc.Spec.Disruption.MaxUnavailableOrDefault() - inFlight

	// The gate: while the applied template lags the fleet's target,
	// advancing it is worth more than a worker's turn. The hold must
	// outlive the sweep that grants the leader, because every sweep
	// recomputes this decision from scratch, and the grant's own
	// status write triggers the next sweep within milliseconds. So
	// the hold keys on durable state: a leader that can be granted
	// now, or a leader whose granted turn is still running. When
	// neither exists, because every leader is Manual or a leader is
	// stuck without a grant, the workers proceed: holding them for a
	// leader that cannot take or use a turn would stall the rollout,
	// and the machine operator's guard makes a worker's lag
	// survivable.
	gate := appliedVersion != "" && clusterDoc.Spec.Version != "" && appliedVersion != clusterDoc.Spec.Version
	holdWorkers := gate && ((len(leaders) > 0 && !leaderBusy) || leaderAdvancing)

	if !holdWorkers {
		for _, name := range workers {
			if len(stalled) > 0 || capacity <= 0 {
				waiting = append(waiting, name)
				continue
			}
			r.grant = append(r.grant, name)
			inProgress = append(inProgress, name)
			capacity--
		}
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
	if holdWorkers {
		// Every waiting worker sits out the sweep: the gate holds the
		// whole group back rather than admit some of them ahead of the
		// leader turn that ends the lag.
		waiting = append(waiting, workers...)
	}

	switch {
	case len(stalled) > 0:
		r.progressing = api.Condition{
			Type: "Progressing", Status: api.ConditionFalse, Reason: "RolloutStalled",
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
		if holdWorkers && len(workers) > 0 {
			message += fmt.Sprintf("; the applied system-pod template (%s) lags the fleet's target (%s); "+
				"a leader goes first to advance the template, and workers wait", appliedVersion, clusterDoc.Spec.Version)
		}
		r.progressing = api.Condition{
			Type: "Progressing", Status: api.ConditionTrue, Reason: "RollingOut", Message: message,
		}
	default:
		r.progressing = api.Condition{
			Type: "Progressing", Status: api.ConditionTrue, Reason: "RolloutComplete",
			Message: "no machines are waiting for a reboot turn",
		}
	}
	return r
}

// carryOutRollout writes the verdict onto the fleet: it adds grants
// to the named Machines, and removes spent grants. These are writes
// to other machines' statuses, and they are safe for the same reason
// the Lost write is: each write touches only the one condition type
// that this writer owns. When a 409 happens because of a crossing
// write, this function simply waits for the next sweep.
func carryOutRollout(c *kubernetes.Client, machines []machine.Machine, r rollout, now time.Time) {
	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		status := m.Status
		switch {
		case slices.Contains(r.grant, name):
			status.Conditions = api.SetCondition(slices.Clone(m.Status.Conditions), api.Condition{
				Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows",
				ObservedGeneration: m.Metadata.Generation,
				Message:            "the cluster's disruption budget allows this machine to take its reboot turn now",
			}, now)
			if err := kubernetes.PublishStatus(c, m, &status); err != nil {
				fmt.Printf("granting %s its reboot turn: %v\n", name, err)
			} else {
				fmt.Printf("granted %s its reboot turn\n", name)
			}
		case slices.Contains(r.revoke, name):
			status.Conditions = api.RemoveCondition(slices.Clone(m.Status.Conditions), machine.RebootApprovedCondition)
			if err := kubernetes.PublishStatus(c, m, &status); err != nil {
				fmt.Printf("reclaiming %s's reboot turn: %v\n", name, err)
			}
		}
	}
}
