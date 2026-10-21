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
// Turns go to the oldest request first, so a machine that asks again
// after each turn cannot keep another machine waiting. Among requests
// made in the same second, workers go before leaders, because a worker
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
// A machine asks for a turn with an approved Disruption (requests.go).
// This program grants the turn with the machine's entry in the
// ledger below, and the machine's own operator acts on that entry: it
// cordons, drains, and reboots the machine
// (machine-operator/turn.go). This program also writes a
// RebootApproved condition onto the Machine, because a machine-operator
// of the release before acts on that condition alone. The condition is
// present while the turn is extended, it is removed when the turn is
// spent, and it is never set to False.
//
// The budget counts all unavailability, planned or not. A machine
// that is Lost or Degraded occupies a slot just like a machine that
// is rebooting on request. So a fleet that already has machines down
// pauses its own rollout instead of making things worse. The leaders
// have a stricter floor that no budget can override: only one leader
// may be down or granted a turn at a time. The datastore keeps
// quorum only while a majority of leaders is up, and letting a
// second leader go down could break that majority.
//
// The cluster records each turn before it grants it. The Cluster's
// status holds the grant ledger, status.rebootTurns: each machine that
// holds a turn or is about to receive one, with the resourceVersion of
// the Machine that the grant write names. A sweep adds an entry in its
// Cluster status write, which names the Cluster's resourceVersion, and
// writes the grant only after the API server stored the entry. Two
// copies of this program that read the same Cluster cannot both add
// an entry, because the second write gets 409 Conflict, so the budget
// is one compare-and-swap on one object. The PodDisruptionBudget does
// the same for evictions: the API server records each evicted pod in
// status.disruptedPods before it deletes the pod.
//
// The election alone cannot keep the budget. A leader that pauses
// after its write guard allowed a grant, on a stalled node or under
// SIGSTOP, sends the grant when it resumes, and the API server can
// commit a write after the client gave up on it. That grant names the
// Machine's version from before the pause, and nothing else wrote the
// Machine since, so the API server accepts it. Without the ledger, a
// new leader could grant another machine in the meantime, and the two
// grants together would exceed the budget.
//
// An entry counts against the budget until no grant that it covers can
// land. That is true when the Machine is gone, or holds no grant at a
// version other than the recorded one, because a resourceVersion never
// returns to an earlier value. The sweep reads such a Machine from the
// API server, not from its watch's copy, because an older copy also
// differs from the entry (fleetReader.readTurns). An entry whose
// Machine holds no grant at the recorded version is confirmed: this
// program writes the grant itself, from that copy. A late grant from
// the copy that recorded the entry names the same version, so the API
// server accepts one of the two writes. A grant that no entry names,
// from a release before the ledger or from a ledger that an older
// schema pruned, enters the ledger at the Machine's current version.
// These rules read only the API's state, so a new leader applies them
// to the entries of the leader before it the same way.
//
// The budget counts a machine as down only when its liveness verdict
// says so. A new process of this program records each heartbeat Lease
// as seen when it starts (heartbeats.go), so for its first
// HeartbeatStaleAfter it reads a machine that is already down by that
// machine's last written status, which is usually Ready. So the conductor grants no turn and
// reclaims no grant until its record of the heartbeats is that old,
// and a machine that was already down reads Lost before the first
// grant.

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

	// turns is the grant ledger that the sweep writes into the
	// Cluster's status before any grant, in name order. confirm names
	// each machine whose entry an earlier sweep recorded, whose grant
	// is absent, and whose Machine is still at the recorded version.
	turns   []cluster.RebootTurn
	confirm []string

	// behind counts the machines that do not run the fleet's target
	// release yet. It belongs to the rollout, because the rollout is
	// what closes the gap, and this loop already reads every
	// machine's version. A cluster with no target counts nobody
	// behind: there is nothing to be behind (metrics.go).
	behind int

	// waits holds, for each machine that asks for a turn and gets none
	// on this sweep, what it waits for, in the words its Disruptions'
	// Granted condition reports.
	waits map[string]string

	// budgets holds what each served MachineDisruptionBudget counts
	// after this sweep's grants.
	budgets map[string]disruption.BudgetStatus
}

// wantsTurn reports whether the machine has a request that may take a
// turn: a Disruption that its rebootPolicy or its approvedHash
// approves, and that is not done (requests.go). The reason
// AwaitingTurn on a machine's condition still tells a person that the
// machine waits, and a machine-operator of the release before still
// sets it, but the conductor grants on the request, so any program
// that may create a Disruption asks the same way.
func wantsTurn(open []string) bool {
	return len(open) > 0
}

// available reports whether a machine is serving the cluster right
// now. Blocked and UpdatePending machines are up. Their trouble is
// administrative, not operational. A Downloading machine is up too:
// the release downloads in the background, and the machine goes
// down only on the turn that this conductor grants. Every other
// machine that is not Ready is either absent or unwell.
func available(phase api.Phase) bool {
	switch phase {
	case api.PhaseReady, api.PhaseUpdatePending, api.PhaseBlocked, api.PhaseDownloading:
		return true
	}
	return false
}

// machineNamed answers the machine of a name from the sweep's list.
func machineNamed(machines []machine.Machine, name string) *machine.Machine {
	for i := range machines {
		if machines[i].Metadata.Name == name {
			return &machines[i]
		}
	}
	return &machine.Machine{Metadata: api.ObjectMeta{Name: name}}
}

// decideRollout computes the whole rollout decision, over the same
// inputs that the fleet sweep reads. It grants turns to the oldest
// request first, and among requests made in the same second, to
// workers before leaders and then in name order. Workers go first
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
//
// heardSince is when this program first read the heartbeat Leases
// (heartbeatSightings.since). Until HeartbeatStaleAfter has passed
// since then, a machine that is already down still reads its last
// status, so the decision grants no turn and reclaims no grant, and
// the Progressing message says why. A grant and a reclaim both trust
// that a machine reads available only when it is up. The zero time
// means the record is old enough.
func decideRollout(machines []machine.Machine, heard map[string]time.Time, heardSince time.Time,
	clusterDoc *cluster.Cluster, appliedVersion string, q fleetRequests, now time.Time) rollout {
	r := rollout{waits: map[string]string{}}
	judgedFrom := heardSince.Add(kubernetes.HeartbeatStaleAfter)
	listening := !heardSince.IsZero() && !now.After(judgedFrom)
	var occupying []string // machines out of service: unavailable, or holding a turn
	leaderBusy := false
	leaderAdvancing := false // a leader holds an unspent grant; its boot advances the template
	var stalled, inProgress, waiting, workers, leaders, fetching, rejoining []string
	ledger := map[string]cluster.RebootTurn{}
	for _, turn := range clusterDoc.Status.RebootTurns {
		ledger[turn.Machine] = turn
	}
	versions := map[string]string{}
	since := now.UTC().Truncate(time.Second)

	for i := range machines {
		m := &machines[i]
		name := m.Metadata.Name
		versions[name] = m.Metadata.ResourceVersion
		leader := slices.Contains(clusterDoc.Spec.Leaders, name)
		phase := effectivePhase(m, heard, now)
		grant := api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition)
		if clusterDoc.Spec.Version != "" && m.Status.Version.Liken != clusterDoc.Spec.Version {
			r.behind++
		}

		// The ledger. The sweep reads a machine whose entry shows no
		// grant from the API server (fleetReader.readTurns), so its
		// version here is current. At another version, no grant that
		// the entry covers can land, so the entry leaves and its slot
		// is free, unless the entry's Disruptions hold it
		// (holdsEntry). A grant that no entry names, from an older
		// binary or from a ledger that an older schema pruned, enters
		// the ledger at the Machine's current version.
		open := q.open(m)
		turn, recorded := ledger[name]
		if recorded && grant == nil && m.Metadata.ResourceVersion != turn.MachineResourceVersion &&
			!holdsEntry(turn, open, phase) {
			recorded = false
		}
		if !recorded && grant != nil {
			turn, recorded = cluster.RebootTurn{Machine: name, MachineResourceVersion: m.Metadata.ResourceVersion, Since: since}, true
		}
		if recorded {
			// Each open request joins the turn, so it shares the
			// turn's drain.
			turn.Disruptions = joined(turn.Disruptions, open)
			r.turns = append(r.turns, turn)
		}
		// A pending entry records a turn whose grant this sweep does
		// not see. A grant write from the copy that recorded it can
		// still be in flight.
		pending := recorded && grant == nil

		switch {
		case grant != nil && available(phase) && !wantsTurn(open):
			// The turn is spent, because the machine converged, or it
			// was never used, because the edit was reverted or the
			// policy changed to Manual. Either way, the machine is
			// back and no longer asking, so the grant returns to the
			// budget. Its entry keeps the slot until a later sweep
			// reads the Machine without the grant.
			r.revoke = append(r.revoke, name)
			occupying = append(occupying, name)
			leaderBusy = leaderBusy || leader
		case grant != nil && !available(phase) && now.Sub(grant.LastTransitionTime) > rolloutStallAfter:
			// This machine was granted a turn, went down, and has
			// stayed down too long. That is no longer a reboot in
			// progress. It is an outage.
			stalled = append(stalled, name)
			occupying = append(occupying, name)
		case pending && m.Metadata.ResourceVersion != turn.MachineResourceVersion && available(phase) && wantsTurn(open):
			// The entry outlived its grant, because one of its
			// requests is still open, and the machine asks for a turn
			// again: a request that arrived as the last grant was
			// reclaimed. The machine still holds its turn and its slot,
			// so the grant goes out again in the same entry, at the
			// Machine's current version, once the stall and the
			// heartbeats allow it (below). A release that is still
			// downloading waits, so it joins this grant too.
			occupying = append(occupying, name)
			inProgress = append(inProgress, name)
			leaderBusy = leaderBusy || leader
			leaderAdvancing = leaderAdvancing || leader
			if downloading(m) {
				fetching = append(fetching, name)
				r.waits[name] = "the machine's release is still downloading, and it joins this turn when it finishes"
				continue
			}
			rejoining = append(rejoining, name)
		case grant != nil || !available(phase) || pending:
			// A machine that is mid-turn, recorded for a turn, or
			// unwell occupies a budget slot either way. The budget
			// counts unavailability itself, not whether the
			// unavailability was planned.
			occupying = append(occupying, name)
			if grant != nil || pending {
				inProgress = append(inProgress, name)
			}
			// The confirm writes the grant from the copy at the
			// recorded version. A late grant from the copy that
			// recorded the entry names the same version, so the API
			// server accepts one of the two. A machine that is down
			// gets no confirm, because its grant would hide the Lost
			// verdict, and that verdict's own write moves the version.
			if pending && available(phase) && !listening {
				r.confirm = append(r.confirm, name)
			}
			if leader {
				leaderBusy = true
				// A leader that holds an unspent grant, or is recorded
				// for one, is mid-turn: its boot is the event that
				// advances the applied template, and the gate below
				// keys on that. A leader that is merely unavailable,
				// with no grant, advances nothing.
				leaderAdvancing = leaderAdvancing || grant != nil || pending
			}
		case wantsTurn(open) && downloading(m):
			// The release is still on its way, so the turn waits for
			// it, and the machine takes one drain and one reboot for
			// both.
			fetching = append(fetching, name)
			r.waits[name] = "the machine's release is still downloading, and the turn waits so the release shares its reboot"
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
	book := newBudgetBook(q.budgetsOf(), machines, occupying)
	wait := func(name, why string) {
		waiting = append(waiting, name)
		r.waits[name] = why
	}
	// held answers why no machine takes a new turn on this sweep, or
	// the empty string when turns can go out.
	held := func() string {
		switch {
		case len(stalled) > 0:
			return "the rollout is stalled: " + strings.Join(stalled, ", ") + " took a turn and did not come back"
		case listening:
			return "cluster-operator started reading the heartbeat leases at " + heardSince.Format(time.RFC3339) +
				", and grants no turn until " + judgedFrom.Format(time.RFC3339)
		}
		return ""
	}

	// A machine whose entry outlived its grant takes the grant again
	// in the same entry. It already holds its slot, so no budget
	// applies, but a stalled rollout and a conductor that cannot judge
	// the heartbeats yet grant nothing.
	for _, name := range rejoining {
		if why := held(); why != "" {
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

	give := func(name string) {
		r.grant = append(r.grant, name)
		r.turns = append(r.turns, cluster.RebootTurn{Machine: name, MachineResourceVersion: versions[name], Since: since,
			Disruptions: q.open(machineNamed(machines, name))})
		inProgress = append(inProgress, name)
		book.take(name)
	}
	// One queue holds every machine that waits for a turn, oldest
	// request first, so a machine that asks again after each turn never
	// keeps one that asked before it waiting. Requests made in the same
	// second put workers ahead of leaders, then go in name order.
	queue := slices.Concat(workers, leaders)
	asked := map[string]time.Time{}
	for _, name := range queue {
		asked[name] = q.oldestOpen(machineNamed(machines, name))
	}
	slices.SortStableFunc(queue, func(a, b string) int {
		if c := asked[a].Compare(asked[b]); c != 0 {
			return c
		}
		return compareLeader(slices.Contains(leaders, a), slices.Contains(leaders, b))
	})
	for _, name := range queue {
		leader := slices.Contains(leaders, name)
		switch {
		case !leader && holdWorkers:
			// Every waiting worker sits out the sweep: the gate holds
			// the whole group back rather than admit some of them ahead
			// of the leader turn that ends the lag.
			wait(name, fmt.Sprintf("the applied system-pod template (%s) lags the fleet's target (%s), and a leader goes first to advance it",
				appliedVersion, clusterDoc.Spec.Version))
			continue
		case held() != "":
			wait(name, held())
			continue
		case leader && leaderBusy:
			wait(name, "another leader is out of service, and only one leader is out at a time")
			continue
		}
		if why := book.refusal(name); why != "" {
			wait(name, why)
			continue
		}
		give(name)
		leaderBusy = leaderBusy || leader
	}
	slices.SortFunc(r.turns, func(a, b cluster.RebootTurn) int { return strings.Compare(a.Machine, b.Machine) })
	r.budgets = book.statuses(q.budgets)
	if listening {
		// A reclaim waits too: the machine reads available from a
		// status that may be older than the machine's last power-off.
		r.revoke = nil
	}

	switch {
	case len(stalled) > 0:
		r.progressing = api.Condition{
			Type: "Progressing", Status: api.ConditionFalse, Reason: "RolloutStalled",
			Message: fmt.Sprintf("granted a reboot turn more than %s ago and not back: %s; no further turns until it returns",
				rolloutStallAfter, strings.Join(stalled, ", ")),
		}
	case len(inProgress)+len(waiting)+len(fetching) > 0:
		message := "taking a reboot turn: " + strings.Join(inProgress, ", ")
		switch {
		case len(inProgress) > 0:
		case listening:
			message = "reboot turns are waiting on the heartbeats"
		case len(waiting) > 0:
			message = "reboot turns are waiting on the disruption budgets"
		default:
			message = "reboot turns are waiting for a release to finish downloading"
		}
		if len(waiting) > 0 {
			message += "; waiting: " + strings.Join(waiting, ", ")
		}
		if len(fetching) > 0 {
			message += "; waiting for a release to finish downloading: " + strings.Join(fetching, ", ")
		}
		if holdWorkers && len(workers) > 0 {
			message += fmt.Sprintf("; the applied system-pod template (%s) lags the fleet's target (%s); "+
				"a leader goes first to advance the template, and workers wait", appliedVersion, clusterDoc.Spec.Version)
		}
		if listening {
			message += fmt.Sprintf("; cluster-operator started reading the heartbeat leases at %s, "+
				"and grants no turn until %s, when a machine that is down reads Lost",
				heardSince.Format(time.RFC3339), judgedFrom.Format(time.RFC3339))
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

// withoutGrants holds every new turn of a sweep that could not read
// the machine-operator DaemonSet, because the sweep cannot tell whether
// the applied template lags the fleet's target. The held turns leave
// the ledger too, so they take no slot. Reclaiming a spent grant does
// not depend on the template, so the reclaims stand. A confirm stands
// too: its entry records a decision that an earlier sweep made with
// that read.
func (r rollout) withoutGrants(err error) rollout {
	if len(r.grant) == 0 {
		return r
	}
	r.progressing = api.Condition{
		Type: "Progressing", Status: api.ConditionTrue, Reason: "RollingOut",
		Message: fmt.Sprintf("reboot turns are waiting on a read of the applied system-pod template: %v; waiting: %s",
			err, strings.Join(r.grant, ", ")),
	}
	r.turns = slices.DeleteFunc(slices.Clone(r.turns), func(turn cluster.RebootTurn) bool {
		return slices.Contains(r.grant, turn.Machine)
	})
	r.grant = nil
	return r
}

// carryOutRollout writes the verdict onto the fleet: it adds grants
// to the named Machines, confirms the pending entries, and removes
// spent grants. These are writes to other machines' statuses, and they
// are safe for the same reason the Lost write is: each write touches
// only the one condition type that this writer owns. When a 409
// happens because of a crossing write, this function simply waits for
// the next sweep. Each grant, confirm, and reclaim that lands posts
// one Event on the machine.
//
// stored is the ledger that the API server holds after this sweep's
// Cluster write. A grant or a confirm is written only when stored
// holds its machine at the version of the copy the write sends. A
// Cluster write that got a 409, or whose answer an older schema
// pruned, stores no entry, so the sweep grants nothing. A reclaim
// adds no disruption, so it needs no entry.
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
				Message:            "the cluster's disruption budget allows this machine to take its reboot turn now",
			}, now)
			message := "the cluster's disruption budget allows this machine to take its reboot turn now"
			if slices.Contains(r.confirm, name) {
				message = "the cluster recorded this machine's reboot turn in an earlier sweep, and grants it now"
			}
			if err := reads.publishStatus(m, &status); err != nil {
				fmt.Printf("granting %s its reboot turn: %v\n", name, err)
			} else {
				fmt.Printf("granted %s its reboot turn\n", name)
				reads.recorder.Normal(machineReference(m), reasonRebootTurnGranted, message)
			}
		case slices.Contains(r.revoke, name):
			status.Conditions = api.RemoveCondition(slices.Clone(m.Status.Conditions), machine.RebootApprovedCondition)
			if err := reads.publishStatus(m, &status); err != nil {
				fmt.Printf("reclaiming %s's reboot turn: %v\n", name, err)
			} else {
				reads.recorder.Normal(machineReference(m), reasonRebootTurnReclaimed,
					"the machine is available and asks for no reboot, so its turn returns to the disruption budget")
			}
		}
	}
}
