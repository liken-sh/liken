package main

// The leader sweep is the fleet-level half of the operator's job.
//
// Every machine's operator reports on itself, which leaves one gap
// nothing else covers: a dead machine can't report that it's dead.
// Its last written status sits in the API reading Ready forever,
// which is worse than no status at all. Kubernetes has this exact
// problem with kubelets and solves it with heartbeats: the kubelet
// renews a lease every few seconds, and the node controller turns a
// silent lease into a NotReady Node. The Machine gets the same
// treatment here: every operator renews its machine's lease
// (lease.go), and the leaders mark machines whose leases have gone
// silent with the Lost phase.
//
// Only the leaders sweep, because they are the machines positioned
// to observe the fleet. A follower that can reach the API is by
// definition reaching a leader, so a follower's sweep could never
// report anything a leader's sweep couldn't. The sweep is also where
// the cluster's headcount comes from: the same pass over the Machine
// list yields the ready-out-of-total tally the Cluster's status
// carries.
//
// Writing another machine's status breaks the one-writer-per-object
// rule this operator otherwise keeps, so the sweep is careful about
// when: it only writes a machine whose heartbeat is already stale. A
// stale heartbeat means the machine's own operator has stopped
// writing, so the two writers can never actually contend. The moment
// the machine returns, its own operator's next pass overwrites the
// Lost verdict with fresh observations, no cleanup required.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// heartbeatRenewAfter is how old the heartbeat must be before the
// machine's own operator renews it: just under the ten-second
// reconcile ticker, so every ticker pass renews but the event-driven
// passes in between get by on a read. heartbeatStaleAfter is how
// long a machine may then go silent before the sweep declares it
// Lost. A single missed renewal may just mean a busy moment; several
// missed renewals mean the machine is down.
//
// The numbers are kube-node-lease's: the kubelet renews its lease
// every ten seconds, and the node controller gives a silent kubelet
// forty before its Node goes NotReady. A dead machine silences both
// leases at the same moment, so matching the thresholds means both
// verdicts land together, and `kubectl get nodes` never spends a
// minute contradicting `kubectl get machines` about a machine that
// just died.
const (
	heartbeatRenewAfter = 8 * time.Second
	heartbeatStaleAfter = 40 * time.Second
)

func listMachines(c *apiClient) ([]machine.Machine, error) {
	var list struct {
		Items []machine.Machine `json:"items"`
	}
	if err := c.requestJSON(http.MethodGet, machinesPath, nil, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// A fleetSweep is one pass's verdict over the whole fleet: which
// machines to declare Lost, the headcount for the Cluster's status,
// the MachinesReady condition that carries the full detail, and the
// phase that summarizes it in one word. This is the same
// conditions-then-phase arrangement every Machine has, applied to
// the fleet. decideFleetSweep is pure; sweepFleet acts.
type fleetSweep struct {
	lost      []string
	tally     machine.MachineTally
	condition machine.Condition
	phase     machine.Phase
}

// decideFleetSweep judges every machine by its effective phase: a
// machine counts toward ready only when its phase is Ready and its
// heartbeat is fresh. The verdict sorts every machine that isn't
// Ready into one of two groups: mid-transition (rebooting into a
// change, waiting on one, or booting), or unwell (Lost, Blocked, or
// otherwise degraded). Unwell outranks mid-transition, and the
// MachinesReady condition names the machines so nobody has to go
// looking.
func decideFleetSweep(machines []machine.Machine, renewals map[string]time.Time, self string, now time.Time) fleetSweep {
	s := fleetSweep{tally: machine.MachineTally{Total: len(machines)}}
	var transitioning, unwell []string
	for i := range machines {
		m := &machines[i]
		effective := effectivePhase(m, renewals, self, now)
		if effective == machine.PhaseLost && m.Status.Phase != machine.PhaseLost {
			s.lost = append(s.lost, m.Metadata.Name)
		}

		switch effective {
		case machine.PhaseReady:
			s.tally.Ready++
		case machine.PhaseUpdating, machine.PhaseUpdatePending, machine.PhaseBooting:
			transitioning = append(transitioning, m.Metadata.Name)
		default:
			unwell = append(unwell, m.Metadata.Name)
		}
	}
	s.tally.Summary = fmt.Sprintf("%d/%d", s.tally.Ready, s.tally.Total)

	switch {
	case len(unwell) > 0:
		s.phase = machine.PhaseDegraded
		s.condition = machine.Condition{
			Type: "MachinesReady", Status: machine.ConditionFalse, Reason: "MachinesDegraded",
			Message: fmt.Sprintf("%s machines ready; unwell: %s", s.tally.Summary, strings.Join(unwell, ", ")),
		}
	case len(transitioning) > 0:
		s.phase = machine.PhaseUpdating
		s.condition = machine.Condition{
			Type: "MachinesReady", Status: machine.ConditionFalse, Reason: "MachinesUpdating",
			Message: fmt.Sprintf("%s machines ready; mid-transition: %s", s.tally.Summary, strings.Join(transitioning, ", ")),
		}
	default:
		s.phase = machine.PhaseReady
		s.condition = machine.Condition{
			Type: "MachinesReady", Status: machine.ConditionTrue, Reason: "AllMachinesReady",
			Message: fmt.Sprintf("all %d machines are ready", s.tally.Total),
		}
	}
	return s
}

// sweepFleet is the acting half: list the fleet and its heartbeats,
// decide, mark the silent machines Lost, and publish the verdict on
// the Cluster.
func sweepFleet(c *apiClient, self string, cluster *machine.Cluster, now time.Time) {
	machines, err := listMachines(c)
	if err != nil {
		fmt.Printf("listing machines for the fleet sweep: %v\n", err)
		return
	}
	renewals, err := listMachineHeartbeats(c)
	if err != nil {
		fmt.Printf("listing heartbeats for the fleet sweep: %v\n", err)
		return
	}
	s := decideFleetSweep(machines, renewals, self, now)

	// The rollout is decided from the same listing: which machines may
	// take their reboot turn now, and which spent grants come back
	// (rollout.go). Sequencing belongs here for the same reason the
	// tally does. The sweep is the one place with the whole fleet in
	// view, and the lease already guarantees a single conductor.
	r := decideRollout(machines, renewals, cluster, self, now)
	carryOutRollout(c, machines, r, now)

	// The OS's own pods (the operator's and the log relays') are
	// fleet state too: upgraded machines carry pods from before their
	// upgrade until the steward refreshes them (steward.go).
	stewardOSPods(c, machines)

	for _, m := range machines {
		if !slices.Contains(s.lost, m.Metadata.Name) {
			continue
		}
		// Everything else in the status stays as the machine last
		// wrote it: those are still its last known facts, and only
		// the machine can revise them. The phase and the Ready
		// condition flip because they are claims about the present,
		// and the machine has stopped making them.
		status := m.Status
		status.Phase = machine.PhaseLost
		status.Conditions = machine.SetCondition(status.Conditions, machine.Condition{
			Type: "Ready", Status: machine.ConditionUnknown, Reason: "HeartbeatStale",
			ObservedGeneration: m.Metadata.Generation,
			Message:            "the machine's operator has stopped renewing its heartbeat lease; the machine is presumed down",
		}, now)
		// A conflict here means the machine just came back and wrote
		// first, which is the outcome the sweep wanted anyway, so it
		// concedes silently; the sweeper never retries a write onto
		// another machine's status.
		if err := publishStatus(c, &m, &status); errors.Is(err, errConflict) {
			continue
		} else if err != nil {
			fmt.Printf("marking %s lost: %v\n", m.Metadata.Name, err)
		} else {
			fmt.Printf("machine %s has gone silent; marked Lost\n", m.Metadata.Name)
		}
	}

	// Publish the cluster's status. The MachinesReady condition
	// carries the observation (stamped with the generation of the
	// spec it judged), Progressing reports the rollout, the phase
	// summarizes both, and the tally is the headcount the printer
	// shows. The catalog's newest version is derived here too: the
	// sweep is the one writer the Cluster's status has, so every
	// derived field is its job. The write happens only when something
	// actually changed, so a settled fleet writes nothing.
	newest := machine.NewestVersion(cluster.Spec.Releases.Catalog)
	s.condition.ObservedGeneration = cluster.Metadata.Generation
	r.progressing.ObservedGeneration = cluster.Metadata.Generation
	conditions := machine.SetCondition(slices.Clone(cluster.Status.Conditions), s.condition, now)
	conditions = machine.SetCondition(conditions, r.progressing, now)
	if cluster.Status.Machines != s.tally || cluster.Status.Phase != s.phase ||
		cluster.Status.Releases.Newest != newest ||
		!slices.Equal(conditions, cluster.Status.Conditions) {
		updated := *cluster
		updated.Status.Machines = s.tally
		updated.Status.Phase = s.phase
		updated.Status.Releases.Newest = newest
		updated.Status.Conditions = conditions
		body, err := json.Marshal(&updated)
		if err != nil {
			fmt.Printf("rendering cluster status: %v\n", err)
			return
		}
		path := clustersPath + "/" + cluster.Metadata.Name + "/status"
		if err := c.requestJSON(http.MethodPut, path, body, nil); err != nil {
			fmt.Printf("publishing cluster status: %v\n", err)
		}
	}
}
