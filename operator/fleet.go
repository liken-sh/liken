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
// treatment here: every operator renews status.observedAt each pass
// (reconcile.go), and the leaders turn silence into a Lost phase.
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
	"time"

	"github.com/chrisguidry/liken/machine"
)

// heartbeatRenewAfter is how old the heartbeat must be before the
// machine's own operator renews it: shy of the 30-second reconcile
// ticker, so every ticker pass renews but the event-driven passes in
// between leave it alone (reconcile.go explains the feedback loop
// that cadence prevents). heartbeatStaleAfter is how long a machine
// may then go silent before the sweep declares it Lost: three missed
// renewals, the same threshold the time loop uses before declaring
// its NTP sources gone. One missed renewal is a busy moment; three
// is a machine that's down.
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
// and the fleet's phases rolled up into the cluster's one word.
// decideFleetSweep is pure; sweepFleet acts.
type fleetSweep struct {
	lost  []string
	tally machine.MachineTally
	phase string
}

// decideFleetSweep judges every machine by its heartbeat. A machine
// counts toward ready only when it says Ready *and* said so recently:
// a frozen status is only as current as the machine that wrote it,
// and that machine may no longer exist. The sweeping leader exempts
// itself — it is running this very code, so its liveness isn't in
// question, only how recently its status write landed.
//
// The cluster's phase reads the same effective phases (a silent
// machine counts as Lost even before the Lost write lands): Ready
// when everyone is, Updating when the only exceptions are machines
// mid-transition — rebooting into a change, waiting on one, or
// booting — and Degraded the moment anything is Lost, Blocked, or
// otherwise unwell.
func decideFleetSweep(machines []machine.Machine, self string, now time.Time) fleetSweep {
	s := fleetSweep{tally: machine.MachineTally{Total: len(machines)}, phase: machine.PhaseReady}
	for _, m := range machines {
		fresh := m.Metadata.Name == self ||
			(m.Status.ObservedAt != nil && now.Sub(*m.Status.ObservedAt) <= heartbeatStaleAfter)
		if !fresh && m.Status.Phase != machine.PhaseLost {
			s.lost = append(s.lost, m.Metadata.Name)
		}

		effective := m.Status.Phase
		if !fresh {
			effective = machine.PhaseLost
		}
		switch effective {
		case machine.PhaseReady:
			s.tally.Ready++
		case machine.PhaseUpdating, machine.PhaseUpdatePending, machine.PhaseBooting:
			if s.phase != machine.PhaseDegraded {
				s.phase = machine.PhaseUpdating
			}
		default:
			s.phase = machine.PhaseDegraded
		}
	}
	s.tally.Summary = fmt.Sprintf("%d/%d", s.tally.Ready, s.tally.Total)
	return s
}

// sweepFleet is the acting half: list the fleet, decide, mark the
// silent machines Lost, and publish the headcount on the Cluster.
func sweepFleet(c *apiClient, self string, cluster *machine.Cluster, now time.Time) {
	machines, err := listMachines(c)
	if err != nil {
		fmt.Printf("listing machines for the fleet sweep: %v\n", err)
		return
	}
	s := decideFleetSweep(machines, self, now)

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
			Message: "the machine's operator has stopped renewing status.observedAt; the machine is presumed down",
		}, now)
		if err := publishStatus(c, &m, &status); err != nil {
			// A conflict here usually means the machine just came
			// back and wrote first — exactly the outcome we wanted.
			fmt.Printf("marking %s lost: %v\n", m.Metadata.Name, err)
		} else {
			fmt.Printf("machine %s has gone silent; marked Lost\n", m.Metadata.Name)
		}
	}

	if cluster.Status.Machines != s.tally || cluster.Status.Phase != s.phase {
		updated := *cluster
		updated.Status.Machines = s.tally
		updated.Status.Phase = s.phase
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
