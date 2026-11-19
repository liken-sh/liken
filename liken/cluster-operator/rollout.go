package main

// This file decides which machines may reboot, and when.
//
// A change that needs a reboot, such as a new release, makes every
// affected machine request a reboot at once. If they all rebooted
// together, the datastore could lose quorum. So the Cluster's
// spec.disruption limits how many machines are out of service at
// once, the way a PodDisruptionBudget limits pod evictions, and
// cluster-operator gives a machine a reboot turn only while the budget
// has room. Every machine that is not available counts against the
// budget, whether it is rebooting on a turn or Lost, so a fleet that
// already has machines down pauses its own rollout. Leaders have a
// stricter limit that no budget overrides: at most one leader is out
// of service or has a turn, because the datastore keeps quorum only
// while a majority of leaders is up.
//
// A machine requests a turn with an approved Disruption (requests.go).
// The turn is the machine's entry in the Cluster's status.rebootTurns,
// and machine-operator cordons, drains, and reboots the machine when
// it reads the entry (machine-operator/turn.go). cluster-operator also
// sets the RebootApproved condition on the Machine for the length of
// the turn, because a machine-operator of the previous release acts
// only on that condition. This file calls writing that condition a
// grant, and removing it a reclaim. The condition is never set to
// False.
//
// The oldest request goes first, so a machine that requests a reboot
// again after each turn cannot keep another machine waiting. Among
// requests created in the same second, workers go before leaders,
// because each leader is a voting member of the datastore.
//
// While the machine-operator DaemonSet's applied template is older
// than the fleet's target release, a waiting leader goes before every
// waiting worker. Only a leader's boot applies the manifests that
// update the template (steward.go). A worker that reboots into the new
// release before that runs its new binary in the old pod spec until
// cluster-operator deletes the pod and the DaemonSet creates it again.
// awaitingPodRefresh in machine-operator/conditions.go keeps such a
// worker from reporting a fault in the meantime.
//
// The entry exists because leader election alone cannot keep the
// budget. A leader that pauses (a stalled node, or SIGSTOP) can send a
// grant when it resumes, and the API server can commit a write after
// the client gave up on it. If nothing wrote the Machine in the
// meantime, the grant's resourceVersion still matches and the API
// server accepts it, even when a new leader has granted another
// machine since. So a pass adds the entry in its write of the
// Cluster's status, and writes the grant only after the API server
// stored the entry. Two leaders that read the same Cluster cannot both
// add an entry, because the second write gets 409 Conflict. The API
// server does the same for evictions: it records each evicted pod in
// the PodDisruptionBudget's status.disruptedPods before it deletes the
// pod.
//
// An entry counts against the budget until no grant written from it
// can still succeed. That is true when the Machine is gone, or when it
// has no grant and a resourceVersion other than the entry's, because a
// late write from the older version gets 409 Conflict. The pass reads
// such a Machine from the API server and not from the watch cache,
// because a stale cached Machine also differs from the entry
// (fleetReader.readTurns). When the Machine has no grant and is still
// at the entry's version, this program confirms the entry by writing
// the grant from that version. A late grant from the leader that wrote
// the entry sends the same version, so the API server accepts only one
// of the two. A grant with no entry, from an older release, gets an
// entry at the Machine's current version. These rules read only API
// state, so a new leader applies them to the previous leader's entries.
//
// The budget counts a machine as down only by its phase, and
// a new cluster-operator process cannot tell a Lost machine until
// HeartbeatStaleAfter has passed (heartbeats.go). Until then, no turn
// is granted and no grant is reclaimed.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// rolloutStallAfter is how long a granted machine may stay unavailable
// before the rollout stalls. A stalled rollout grants no new turns, so
// a fleet whose machines do not come back from reboots stops rebooting
// more of them.
const rolloutStallAfter = 10 * time.Minute

// A rollout is one pass's decision: which machines to grant, which
// grants to reclaim, and the Progressing condition of the Cluster. The
// condition follows Deployment: Progressing False means the rollout
// has stopped making progress.
type rollout struct {
	grant       []string
	revoke      []string
	progressing api.Condition

	// turns is status.rebootTurns as this pass writes it, sorted by
	// machine name. confirm lists the machines whose entry an earlier
	// pass wrote, that have no grant, and that are still at the entry's
	// resourceVersion.
	turns   []cluster.RebootTurn
	confirm []string

	// behind counts the machines that do not run the fleet's target
	// release yet (metrics.go).
	behind int

	// waits maps each machine that requests a turn and gets none in
	// this pass to the reason, which its Disruptions' Granted condition
	// reports.
	waits map[string]string

	// budgets is the status of each MachineDisruptionBudget after this
	// pass's grants.
	budgets map[string]disruption.BudgetStatus
}

// wantsTurn reports whether a machine has an open request: a
// Disruption that its rebootPolicy or approvedHash approves and that
// is not done (requests.go). Turns follow these requests and not the
// AwaitingTurn reason that machine-operator reports, so any program
// that can create a Disruption can request a turn.
func wantsTurn(open []string) bool {
	return len(open) > 0
}

// available reports whether a machine is serving the cluster now.
// Blocked and UpdatePending machines are up and wait for a person. A
// Downloading machine is up too, because it goes down only on a turn.
// Every other phase counts as down.
func available(phase api.Phase) bool {
	switch phase {
	case api.PhaseReady, api.PhaseUpdatePending, api.PhaseBlocked, api.PhaseDownloading:
		return true
	}
	return false
}

// machineNamed returns the machine with the given name from the list,
// or an empty Machine with that name when the list has none.
func machineNamed(machines []machine.Machine, name string) *machine.Machine {
	for i := range machines {
		if machines[i].Metadata.Name == name {
			return &machines[i]
		}
	}
	return &machine.Machine{Metadata: api.ObjectMeta{Name: name}}
}

// decideRollout decides which machines get a reboot turn in this
// pass. Machines with requests of the same age go in name order, so
// two passes over the same fleet agree.
//
// appliedVersion is the release of the machine-operator DaemonSet's
// template (daemonSetVersion, steward.go). It is "" when no DaemonSet
// has been applied, and then leaders do not go first.
//
// leasesFirstRead is when this program first read the heartbeat
// Leases (heartbeatSightings.firstReadTime). The zero time skips the wait for
// HeartbeatStaleAfter.
func decideRollout(machines []machine.Machine, heartbeats map[string]time.Time, leasesFirstRead time.Time,
	clusterDoc *cluster.Cluster, appliedVersion string, q fleetRequests, now time.Time) rollout {
	r := rollout{waits: map[string]string{}}
	heartbeatsTrustedAt := leasesFirstRead.Add(kubernetes.HeartbeatStaleAfter)
	heartbeatsTooNew := !leasesFirstRead.IsZero() && !now.After(heartbeatsTrustedAt)
	var outOfService []string  // unavailable machines and machines with a turn
	leaderBusy := false        // a leader is out of service or has a turn
	leaderTurnRunning := false // a leader has a turn whose boot will update the template
	var stalled, inProgress, waiting, workers, leaders, awaitingDownload, regranting []string
	recordedTurns := map[string]cluster.RebootTurn{}
	for _, turn := range clusterDoc.Status.RebootTurns {
		recordedTurns[turn.Machine] = turn
	}
	versions := map[string]string{}
	since := now.UTC().Truncate(time.Second)

	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		versions[name] = m.Metadata.ResourceVersion
		leader := slices.Contains(clusterDoc.Spec.Leaders, name)
		phase := effectivePhase(m, heartbeats, now)
		grant := api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition)
		if clusterDoc.Spec.Version != "" && m.Status.Version.Liken != clusterDoc.Spec.Version {
			r.behind++
		}

		// Drop an entry whose Machine has no grant and has moved past
		// the entry's resourceVersion, unless its Disruptions keep it
		// (requestsKeepTurn). Such a Machine was read from the API server
		// (fleetReader.readTurns), so its version is current. A grant
		// with no entry gets one at the current version.
		open := q.open(m)
		turn, recorded := recordedTurns[name]
		if recorded && grant == nil && m.Metadata.ResourceVersion != turn.MachineResourceVersion &&
			!requestsKeepTurn(turn, open, phase) {
			recorded = false
		}
		if !recorded && grant != nil {
			turn, recorded = cluster.RebootTurn{Machine: name, MachineResourceVersion: m.Metadata.ResourceVersion, Since: since}, true
		}
		if recorded {
			// A request that opens during the turn joins it, and shares
			// its drain and reboot.
			turn.Disruptions = withOpenRequests(turn.Disruptions, open)
			r.turns = append(r.turns, turn)
		}
		// A pending entry has no grant on the Machine yet. The grant
		// from the leader that wrote the entry can still arrive.
		pending := recorded && grant == nil

		switch {
		case grant != nil && available(phase) && !wantsTurn(open):
			// The machine is up and requests nothing: its turn is done,
			// or its change was reverted or its policy set to Manual
			// before it rebooted. The entry still counts against the
			// budget until a later pass reads the Machine without the
			// grant.
			r.revoke = append(r.revoke, name)
			outOfService = append(outOfService, name)
			leaderBusy = leaderBusy || leader
		case grant != nil && !available(phase) && now.Sub(grant.LastTransitionTime) > rolloutStallAfter:
			stalled = append(stalled, name)
			outOfService = append(outOfService, name)
		case pending && m.Metadata.ResourceVersion != turn.MachineResourceVersion && available(phase) && wantsTurn(open):
			// The grant was reclaimed, but the entry stayed because one
			// of its requests is still open, and the machine requests a
			// turn again. It keeps the entry, and the loop below writes
			// the grant again at the current resourceVersion. A machine
			// whose release is still downloading waits for the download.
			outOfService = append(outOfService, name)
			inProgress = append(inProgress, name)
			leaderBusy = leaderBusy || leader
			leaderTurnRunning = leaderTurnRunning || leader
			if downloading(m) {
				awaitingDownload = append(awaitingDownload, name)
				r.waits[name] = "The machine's release is still downloading, and the machine is granted this turn again when the download finishes"
				continue
			}
			regranting = append(regranting, name)
		case grant != nil || !available(phase) || pending:
			outOfService = append(outOfService, name)
			if grant != nil || pending {
				inProgress = append(inProgress, name)
			}
			// A machine that is down gets no confirm, because the grant
			// would hide its Lost phase, and the write that sets Lost
			// changes the version anyway.
			if pending && available(phase) && !heartbeatsTooNew {
				r.confirm = append(r.confirm, name)
			}
			if leader {
				leaderBusy = true
				// A leader with a grant or an entry will reboot and
				// update the applied template. A leader that is only
				// down updates nothing.
				leaderTurnRunning = leaderTurnRunning || grant != nil || pending
			}
		case wantsTurn(open) && downloading(m):
			awaitingDownload = append(awaitingDownload, name)
			r.waits[name] = "The machine's release is still downloading, and the turn waits for it so that the release installs in the same reboot"
		case wantsTurn(open):
			if leader {
				leaders = append(leaders, name)
			} else {
				workers = append(workers, name)
			}
		}
	}

	slices.Sort(workers)
	slices.Sort(leaders)
	budgetUse := newBudgetCounts(q.budgetsOf(), machines, outOfService)
	wait := func(name, why string) {
		waiting = append(waiting, name)
		r.waits[name] = why
	}
	// noTurnBecause returns why no machine can get a turn in this pass,
	// or "".
	noTurnBecause := func() string {
		switch {
		case len(stalled) > 0:
			return "The rollout is stalled, because " + strings.Join(stalled, ", ") + " took a reboot turn and is not back"
		case heartbeatsTooNew:
			return "cluster-operator started reading the heartbeat Leases at " + leasesFirstRead.Format(time.RFC3339) +
				" and grants no turn until " + heartbeatsTrustedAt.Format(time.RFC3339)
		}
		return ""
	}

	// A machine that is granted again already counts against the
	// budget, so only a stall or a young heartbeat record stops it.
	for _, name := range regranting {
		if why := noTurnBecause(); why != "" {
			r.waits[name] = why
			continue
		}
		r.grant = append(r.grant, name)
		for i := range r.turns {
			if r.turns[i].Machine == name {
				r.turns[i].MachineResourceVersion = versions[name]
			}
		}
	}

	// While the template lags, workers wait for a leader. Each pass
	// decides from scratch, and a grant's status write starts the next
	// pass at once, so the wait depends on state that lasts across
	// passes: a leader that can get a turn now, or a leader whose turn
	// is running. When there is neither, because every leader is Manual
	// or a down leader blocks the others, workers go ahead, because
	// waiting would stall the rollout.
	templateLags := appliedVersion != "" && clusterDoc.Spec.Version != "" && appliedVersion != clusterDoc.Spec.Version
	holdWorkers := templateLags && ((len(leaders) > 0 && !leaderBusy) || leaderTurnRunning)

	grantTurn := func(name string) {
		r.grant = append(r.grant, name)
		r.turns = append(r.turns, cluster.RebootTurn{Machine: name, MachineResourceVersion: versions[name], Since: since,
			Disruptions: q.open(machineNamed(machines, name))})
		inProgress = append(inProgress, name)
		budgetUse.add(name)
	}
	queue := slices.Concat(workers, leaders)
	oldestRequest := map[string]time.Time{}
	for _, name := range queue {
		oldestRequest[name] = q.oldestOpen(machineNamed(machines, name))
	}
	slices.SortStableFunc(queue, func(a, b string) int {
		if c := oldestRequest[a].Compare(oldestRequest[b]); c != 0 {
			return c
		}
		return compareLeader(slices.Contains(leaders, a), slices.Contains(leaders, b))
	})
	for _, name := range queue {
		leader := slices.Contains(leaders, name)
		switch {
		case !leader && holdWorkers:
			wait(name, fmt.Sprintf("The machine-operator DaemonSet template is at %s and the fleet's target is %s, so a leader reboots first to update the template",
				appliedVersion, clusterDoc.Spec.Version))
			continue
		case noTurnBecause() != "":
			wait(name, noTurnBecause())
			continue
		case leader && leaderBusy:
			wait(name, "Another leader is out of service, and only one leader is out of service at a time")
			continue
		}
		if why := budgetUse.refusal(name); why != "" {
			wait(name, why)
			continue
		}
		grantTurn(name)
		leaderBusy = leaderBusy || leader
	}
	slices.SortFunc(r.turns, func(a, b cluster.RebootTurn) int { return strings.Compare(a.Machine, b.Machine) })
	r.budgets = budgetUse.statuses(q.budgets)
	if heartbeatsTooNew {
		// A machine that reads available may be down, so no grant is
		// reclaimed either.
		r.revoke = nil
	}

	switch {
	case len(stalled) > 0:
		r.progressing = api.Condition{
			Type: "Progressing", Status: api.ConditionFalse, Reason: "RolloutStalled",
			Message: fmt.Sprintf("No machine gets a reboot turn until %s is back, because it took a turn more than %s ago and is not available",
				strings.Join(stalled, ", "), rolloutStallAfter),
		}
	case len(inProgress)+len(waiting)+len(awaitingDownload) > 0:
		message := "Machines with a reboot turn: " + strings.Join(inProgress, ", ")
		switch {
		case len(inProgress) > 0:
		case heartbeatsTooNew:
			message = "Reboot turns wait until the heartbeats show which machines are down"
		case len(waiting) > 0:
			message = "Reboot turns wait for room in the disruption budgets"
		default:
			message = "Reboot turns wait for a release to finish downloading"
		}
		if len(waiting) > 0 {
			message += ". Waiting: " + strings.Join(waiting, ", ")
		}
		if len(awaitingDownload) > 0 {
			message += ". Downloading: " + strings.Join(awaitingDownload, ", ")
		}
		if holdWorkers && len(workers) > 0 {
			message += fmt.Sprintf(". The machine-operator DaemonSet template is at %s and the fleet's target is %s, "+
				"so a leader reboots first to update the template, and workers wait", appliedVersion, clusterDoc.Spec.Version)
		}
		if heartbeatsTooNew {
			message += fmt.Sprintf(". cluster-operator started reading the heartbeat Leases at %s "+
				"and grants no turn until %s, when a machine that is down shows as Lost",
				leasesFirstRead.Format(time.RFC3339), heartbeatsTrustedAt.Format(time.RFC3339))
		}
		r.progressing = api.Condition{
			Type: "Progressing", Status: api.ConditionTrue, Reason: "RollingOut", Message: message,
		}
	default:
		r.progressing = api.Condition{
			Type: "Progressing", Status: api.ConditionTrue, Reason: "RolloutComplete",
			Message: "No machine waits for a reboot turn",
		}
	}
	return r
}

// withoutGrants removes every new turn, and its entry, from the result
// of a pass that could not read the machine-operator DaemonSet,
// because that pass cannot tell whether the template lags. Reclaims
// stay, because they do not depend on the template. Confirms stay,
// because an earlier pass decided them with a successful read.
func (r rollout) withoutGrants(err error) rollout {
	if len(r.grant) == 0 {
		return r
	}
	r.progressing = api.Condition{
		Type: "Progressing", Status: api.ConditionTrue, Reason: "RollingOut",
		Message: fmt.Sprintf("Reboot turns wait for a read of the machine-operator DaemonSet. Waiting: %s. The read returned: %v",
			strings.Join(r.grant, ", "), err),
	}
	r.turns = slices.DeleteFunc(slices.Clone(r.turns), func(turn cluster.RebootTurn) bool {
		return slices.Contains(r.grant, turn.Machine)
	})
	r.grant = nil
	return r
}

// carryOutRollout writes the grants, confirms, and reclaims of
// decideRollout to the Machines. Each write changes only the
// RebootApproved condition, which this program owns, so writing
// another operator's Machine status is safe. A write that gets 409
// Conflict is not retried, because the next pass decides again.
//
// stored is status.rebootTurns as the API server returned it after
// this pass's write of the Cluster. A grant or confirm is written only
// when stored has an entry for the machine at the resourceVersion that
// the write sends. When the Cluster write got 409 Conflict, or an
// older schema pruned the entry from the response, the pass grants
// nothing. A reclaim needs no entry, because it does not add to the
// budget.
func carryOutRollout(reads *fleetReader, machines []machine.Machine, r rollout, stored []cluster.RebootTurn, now time.Time) {
	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		status := m.Status
		recorded := slices.ContainsFunc(stored, func(turn cluster.RebootTurn) bool {
			return turn.Machine == name && turn.MachineResourceVersion == m.Metadata.ResourceVersion
		})
		switch {
		case recorded && (slices.Contains(r.grant, name) || slices.Contains(r.confirm, name)):
			status.Conditions = api.SetCondition(slices.Clone(m.Status.Conditions), api.Condition{
				Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows",
				ObservedGeneration: m.Metadata.Generation,
				Message:            "The disruption budgets allow this machine a reboot turn now",
			}, now)
			message := "Granted a reboot turn, because the disruption budgets allow one"
			if slices.Contains(r.confirm, name) {
				message = "Granted the reboot turn that an earlier pass recorded in the Cluster's status.rebootTurns"
			}
			if err := reads.publishStatus(m, &status); err != nil {
				fmt.Printf("writing the reboot turn grant to the status of Machine %s: %v\n", name, err)
			} else {
				fmt.Printf("granted a reboot turn to Machine %s\n", name)
				reads.recorder.Normal(machineReference(m), reasonRebootTurnGranted, message)
			}
		case slices.Contains(r.revoke, name):
			status.Conditions = api.RemoveCondition(slices.Clone(m.Status.Conditions), machine.RebootApprovedCondition)
			if err := reads.publishStatus(m, &status); err != nil {
				fmt.Printf("removing the reboot turn grant from the status of Machine %s: %v\n", name, err)
			} else {
				reads.recorder.Normal(machineReference(m), reasonRebootTurnReclaimed,
					"Removed the reboot turn grant, because the machine is available and requests no reboot")
			}
		}
	}
}
