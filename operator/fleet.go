package main

// The leader sweep: the fleet-level half of the operator's job.
//
// Every machine's operator reports on itself, which leaves one gap
// nothing else covers: a dead machine can't report that it's dead.
// Its last written status sits in the API reading Ready forever,
// which is worse than no status at all. Kubernetes has this exact
// problem with kubelets and solves it with heartbeats: the kubelet
// renews a lease every few seconds, and the node controller turns a
// silent lease into a NotReady Node. The Machine gets the same
// treatment here: every operator renews its machine's lease
// (lease.go), and the leaders turn silence into a Lost phase.
//
// Only the leaders sweep. They're the machines positioned to observe
// the fleet — a follower that can reach the API is by definition
// reaching a leader, so follower sweeps could never report anything
// a leader sweep couldn't. The sweep is also where the cluster's
// headcount comes from: the same pass over the Machine list yields
// the ready-out-of-total tally the Cluster's status carries.
//
// Writing another machine's status breaks the one-writer-per-object
// rule this operator otherwise keeps, so the sweep is careful about
// when: it only writes a machine whose heartbeat is already stale,
// which means its own operator has stopped writing — the two writers
// can never actually contend. The moment the machine returns, its
// own operator's next pass overwrites the Lost verdict with fresh
// observations, no cleanup required.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// heartbeatRenewAfter is how old the heartbeat must be before the
// machine's own operator renews it: shy of the 30-second reconcile
// ticker, so every ticker pass renews but the event-driven passes in
// between get by on a read. heartbeatStaleAfter is how long a
// machine may then go silent before the sweep declares it Lost:
// three missed renewals, the same threshold the time loop uses
// before declaring its NTP sources gone. One missed renewal is a
// busy moment; three is a machine that's down.
const (
	heartbeatRenewAfter = 20 * time.Second
	heartbeatStaleAfter = 90 * time.Second
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
// the MachinesReady condition that carries the full story, and the
// phase that summarizes it in one word — the same
// conditions-then-phase arrangement every Machine has, applied to
// the fleet. decideFleetSweep is pure; sweepFleet acts.
type fleetSweep struct {
	lost      []string
	tally     machine.MachineTally
	condition machine.Condition
	phase     machine.Phase
}

// effectivePhase is a machine's phase as the fleet should read it: its
// own claim when its heartbeat is fresh, and Lost when it has gone
// silent — a frozen status is only as current as the machine that
// wrote it, and that machine may no longer exist. A machine with no
// lease at all has never been heard from. The sweeping leader exempts
// itself: it is running this very code, so its liveness isn't in
// question, only how recently its renewal landed.
//
// One silence is not suspicious: a machine holding a reboot grant
// (rollout.go) was *told* to go down, so until the grant is old enough
// to be a stall, its silence reads as the reboot in progress.
func effectivePhase(m *machine.Machine, renewals map[string]time.Time, self string, now time.Time) machine.Phase {
	renewed, heard := renewals[m.Metadata.Name]
	if m.Metadata.Name == self || (heard && now.Sub(renewed) <= heartbeatStaleAfter) {
		return m.Status.Phase
	}
	if grant := machine.FindCondition(m.Status.Conditions, rebootApprovedCondition); grant != nil &&
		now.Sub(grant.LastTransitionTime) <= rolloutStallAfter {
		return machine.PhaseUpdating
	}
	return machine.PhaseLost
}

// decideFleetSweep judges every machine by its effective phase: a
// machine counts toward ready only when it says Ready *and* its lease
// says so recently. The verdict sorts every machine that isn't Ready
// into one of two stories: mid-transition (rebooting into a change,
// waiting on one, or booting), or unwell (Lost, Blocked, or otherwise
// degraded). Unwell outranks mid-transition, and the MachinesReady
// condition names the machines so nobody has to go looking.
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
			Type: "MachinesReady", Status: "False", Reason: "MachinesDegraded",
			Message: fmt.Sprintf("%s machines ready; unwell: %s", s.tally.Summary, strings.Join(unwell, ", ")),
		}
	case len(transitioning) > 0:
		s.phase = machine.PhaseUpdating
		s.condition = machine.Condition{
			Type: "MachinesReady", Status: "False", Reason: "MachinesUpdating",
			Message: fmt.Sprintf("%s machines ready; mid-transition: %s", s.tally.Summary, strings.Join(transitioning, ", ")),
		}
	default:
		s.phase = machine.PhaseReady
		s.condition = machine.Condition{
			Type: "MachinesReady", Status: "True", Reason: "AllMachinesReady",
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
	// tally does — the sweep is the one place with the whole fleet in
	// view, and the lease already guarantees a single conductor.
	r := decideRollout(machines, renewals, cluster, self, now)
	carryOutRollout(c, machines, r, now)

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
			Type: "Ready", Status: "Unknown", Reason: "HeartbeatStale",
			ObservedGeneration: m.Metadata.Generation,
			Message:            "the machine's operator has stopped renewing its heartbeat lease; the machine is presumed down",
		}, now)
		if err := publishStatus(c, &m, &status); err != nil {
			// A conflict here usually means the machine just came
			// back and wrote first — exactly the outcome we wanted.
			fmt.Printf("marking %s lost: %v\n", m.Metadata.Name, err)
		} else {
			fmt.Printf("machine %s has gone silent; marked Lost\n", m.Metadata.Name)
		}
	}

	// The cluster's status: the MachinesReady condition carries the
	// observation (stamped with the generation of the spec it
	// judged), Progressing carries the rollout's story, the phase
	// summarizes, and the tally is the headcount the printer shows.
	// The catalog's newest version is derived here too — the sweep is
	// the one writer the Cluster's status has, so every derived field
	// is its job. Written only when something actually changed, so a
	// settled fleet writes nothing.
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
