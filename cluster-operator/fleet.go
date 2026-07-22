package main

// The fleet sweep is the cluster operator's main task.
//
// Each machine's operator reports on itself. This leaves one gap
// that nothing else covers: a dead machine cannot report that it is
// dead. Its last written status stays in the API and reads Ready
// forever, which is worse than no status at all. Kubernetes has this
// same problem with kubelets, and it solves the problem with
// heartbeats. The kubelet renews a lease every few seconds, and the
// node controller turns a silent lease into a NotReady Node. The
// Machine object gets the same treatment here. Each machine's
// operator renews its own heartbeat lease (see the kubernetes
// package), and this sweep marks a machine with the Lost phase when
// its lease goes silent. The sweep also produces the cluster's
// headcount. The same pass over the Machine list produces the
// ready-out-of-total tally that the Cluster's status carries.
//
// Writing another machine's status breaks the one-writer-per-object
// rule that the machine operators otherwise follow. So the sweep is
// careful about when it writes: it only writes a machine whose
// heartbeat is already stale. A stale heartbeat means the machine's
// own operator has stopped writing, so the two writers can never
// actually write to the same object at the same time. The moment the
// machine returns, its own operator's next pass overwrites the Lost
// verdict with fresh observations. No cleanup step is needed.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// A fleetSweep holds one pass's verdict over the whole fleet: which
// machines to declare Lost, the headcount for the Cluster's status,
// the MachinesReady condition that carries the full detail, and the
// phase that summarizes the verdict in one word. Every Machine uses
// this same conditions-then-phase arrangement, applied here to the
// fleet. The function decideFleetSweep only computes the verdict; the
// function sweepFleet carries it out.
type fleetSweep struct {
	lost      []string
	tally     cluster.MachineTally
	condition api.Condition
	phase     api.Phase
}

// decideFleetSweep judges each machine by its effective phase. A
// machine counts toward ready only when its phase is Ready and its
// heartbeat is fresh. The verdict sorts each machine that is not
// Ready into one of two groups: mid-transition (rebooting into a
// change, waiting for a reboot, or booting) or unwell (Lost, Blocked,
// or otherwise degraded). Unwell outranks mid-transition. The
// MachinesReady condition names the affected machines, so nobody has
// to search for them.
func decideFleetSweep(machines []machine.Machine, renewals map[string]time.Time, now time.Time) fleetSweep {
	s := fleetSweep{tally: cluster.MachineTally{Total: len(machines)}}
	var transitioning, unwell []string
	for i := range machines {
		m := &machines[i]
		effective := effectivePhase(m, renewals, now)
		if effective == api.PhaseLost && m.Status.Phase != api.PhaseLost {
			s.lost = append(s.lost, m.Metadata.Name)
		}

		switch effective {
		case api.PhaseReady:
			s.tally.Ready++
		case api.PhaseUpdating, api.PhaseUpdatePending, api.PhaseBooting:
			transitioning = append(transitioning, m.Metadata.Name)
		default:
			unwell = append(unwell, m.Metadata.Name)
		}
	}
	s.tally.Summary = fmt.Sprintf("%d/%d", s.tally.Ready, s.tally.Total)

	switch {
	case len(unwell) > 0:
		s.phase = api.PhaseDegraded
		s.condition = api.Condition{
			Type: "MachinesReady", Status: api.ConditionFalse, Reason: "MachinesDegraded",
			Message: fmt.Sprintf("%s machines ready; unwell: %s", s.tally.Summary, strings.Join(unwell, ", ")),
		}
	case len(transitioning) > 0:
		s.phase = api.PhaseUpdating
		s.condition = api.Condition{
			Type: "MachinesReady", Status: api.ConditionFalse, Reason: "MachinesUpdating",
			Message: fmt.Sprintf("%s machines ready; mid-transition: %s", s.tally.Summary, strings.Join(transitioning, ", ")),
		}
	default:
		s.phase = api.PhaseReady
		s.condition = api.Condition{
			Type: "MachinesReady", Status: api.ConditionTrue, Reason: "AllMachinesReady",
			Message: fmt.Sprintf("all %d machines are ready", s.tally.Total),
		}
	}
	return s
}

// sweepFleet carries out the sweep. It lists the fleet and its
// heartbeats, decides the verdict, marks the silent machines Lost,
// and publishes the verdict on the Cluster. The available parameter
// is the channel poller's last answer. The caller passes it in as a
// plain value, so the sweep itself stays a function of its
// arguments.
func sweepFleet(c *kubernetes.Client, clusterDoc *cluster.Cluster, available string, now time.Time) {
	machines, err := kubernetes.ListMachines(c)
	if err != nil {
		fmt.Printf("listing machines for the fleet sweep: %v\n", err)
		return
	}
	renewals, err := kubernetes.ListHeartbeats(c)
	if err != nil {
		fmt.Printf("listing heartbeats for the fleet sweep: %v\n", err)
		return
	}
	s := decideFleetSweep(machines, renewals, now)

	// The rollout decision uses the same listing: which machines may
	// take their reboot turn now, and which spent grants return to
	// the budget (see rollout.go). This sequencing happens here for
	// the same reason the tally does: the sweep is the one place that
	// sees the whole fleet at once.
	r := decideRollout(machines, renewals, clusterDoc, now)
	carryOutRollout(c, machines, r, now)

	// The OS's own pods, the operator's pods and the log relay pods,
	// are also fleet state. An upgraded machine keeps pods from
	// before its upgrade until the steward refreshes them (see
	// steward.go).
	stewardOSPods(c, machines)

	// A retracted feature leaves its workloads behind. k3s only
	// deletes an addon when it sees the addon's manifest disappear
	// while k3s is running, but retraction removes the manifest while
	// k3s is down (see janitor.go).
	janitorFeatureWorkloads(c, clusterDoc)

	markLost(c, machines, s.lost, now)
	publishClusterStatus(c, clusterDoc, s, r, available, now)
}

// markLost writes the Lost verdict onto each machine that the sweep
// found silent.
func markLost(c *kubernetes.Client, machines []machine.Machine, lost []string, now time.Time) {
	for _, m := range machines {
		if !slices.Contains(lost, m.Metadata.Name) {
			continue
		}
		// Everything else in the status stays as the machine last
		// wrote it. Those fields are still the machine's last known
		// facts, and only the machine can revise them. The phase and
		// the Ready condition change because they are claims about
		// the present, and the machine has stopped making those
		// claims.
		status := m.Status
		status.Phase = api.PhaseLost
		// The clone matters here. status.Conditions still shares its
		// backing array with the machines slice, and SetCondition
		// rewrites an existing entry in place.
		status.Conditions = api.SetCondition(slices.Clone(m.Status.Conditions), api.Condition{
			Type: "Ready", Status: api.ConditionUnknown, Reason: "HeartbeatStale",
			ObservedGeneration: m.Metadata.Generation,
			Message:            "the machine's operator has stopped renewing its heartbeat lease; the machine is presumed down",
		}, now)
		// A conflict here means the machine just came back and wrote
		// its own status first. That is the exact outcome this write
		// exists to allow, so the sweep skips this machine. The sweep
		// never retries a write onto another machine's status.
		if err := kubernetes.PublishStatus(c, &m, &status); errors.Is(err, kubernetes.ErrConflict) {
			continue
		} else if err != nil {
			fmt.Printf("marking %s lost: %v\n", m.Metadata.Name, err)
		} else {
			fmt.Printf("machine %s has gone silent; marked Lost\n", m.Metadata.Name)
		}
	}
}

// publishClusterStatus publishes the sweep's verdict on the Cluster.
// The MachinesReady condition carries the observation, stamped with
// the generation of the spec it judged. The Progressing condition
// reports the rollout. The phase summarizes both conditions, and the
// tally is the headcount that the printer shows. This function also
// derives the release fields: the catalog's newest version, and the
// channel's last polled version. The sweep is the only writer of the
// Cluster's status, so deriving every field is its job. The function
// writes the status only when something actually changed, so a
// settled fleet causes no write.
func publishClusterStatus(c *kubernetes.Client, clusterDoc *cluster.Cluster, s fleetSweep, r rollout, available string, now time.Time) {
	newest := cluster.NewestVersion(clusterDoc.Spec.Releases.Catalog)
	s.condition.ObservedGeneration = clusterDoc.Metadata.Generation
	r.progressing.ObservedGeneration = clusterDoc.Metadata.Generation
	conditions := api.SetCondition(slices.Clone(clusterDoc.Status.Conditions), s.condition, now)
	conditions = api.SetCondition(conditions, r.progressing, now)
	if clusterDoc.Status.Machines != s.tally || clusterDoc.Status.Phase != s.phase ||
		clusterDoc.Status.Releases.Newest != newest ||
		clusterDoc.Status.Releases.Available != available ||
		clusterDoc.Status.ObservedGeneration != clusterDoc.Metadata.Generation ||
		!slices.Equal(conditions, clusterDoc.Status.Conditions) {
		updated := *clusterDoc
		updated.Status.Machines = s.tally
		updated.Status.Phase = s.phase
		updated.Status.Releases.Newest = newest
		updated.Status.Releases.Available = available
		updated.Status.ObservedGeneration = clusterDoc.Metadata.Generation
		updated.Status.Conditions = conditions
		if err := kubernetes.PublishClusterStatus(c, &updated); err != nil {
			fmt.Printf("publishing cluster status: %v\n", err)
		}
	}
}
