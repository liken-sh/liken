package main

// The hold: a requester's work on the drained machine, between the
// drain and the reboot.
//
// Some changes must be made while no workload runs on the machine and
// take effect at the next boot. firmware-operator's Secure Boot
// database update is one: the kernel reads db and dbx once at boot,
// so the write and the reboot belong in the same turn, or the machine
// runs for days with variables that its kernel does not enforce. A
// requester asks for this with spec.hold on its Disruption. The turn
// drains the machine as usual, then stops before the reboot.
// machine-operator sets the request's Held condition, which tells the
// requester that the machine is drained, and the requester's pod,
// which tolerates the cordon, does the work. The requester removes
// spec.hold when it has checked the work, and the next pass reboots.
//
// Once a request is Held, the drain is over. The pass does not drain
// again, because the requester's own pod now runs on the cordoned node
// and a drain would evict it.
//
// The hold has no deadline. A requester that never removes it keeps
// the machine drained and its budgets full, the same as a machine that
// does not come back from its reboot, and Held records when the hold
// began, for an alert to read. A person who removes spec.hold releases
// the turn.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

// holdingRequests answers the requests that hold this machine's turn:
// each one that asks for a hold, is approved, and is not done, in name
// order. A request that is not approved takes no part in the turn, so
// its hold does not stop the reboot.
func holdingRequests(requests []disruption.Disruption, policy machine.RebootPolicy, bootID string) []disruption.Disruption {
	var holding []disruption.Disruption
	for _, d := range requests {
		if d.Spec.Hold && d.Approved(policy) && !d.Done(bootID) {
			holding = append(holding, d)
		}
	}
	slices.SortFunc(holding, func(a, b disruption.Disruption) int { return strings.Compare(a.Metadata.Name, b.Metadata.Name) })
	return holding
}

// handoverRequest answers the request whose requester set BootNext for
// the turn's reboot: one that asks to keep BootNext, no longer holds,
// is approved, and is not done. Its requester set BootNext while it
// held the drained machine, as firmware-operator does for a capsule,
// and the reboot's intent names it so that init leaves BootNext alone
// (init/handover.go). Two such requests name the same reboot, so the
// first by name speaks for both.
func handoverRequest(requests []disruption.Disruption, policy machine.RebootPolicy, bootID string) string {
	var names []string
	for _, d := range requests {
		if d.Spec.KeepBootNext && !d.Spec.Hold && d.Approved(policy) && !d.Done(bootID) {
			names = append(names, d.Metadata.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return slices.Min(names)
}

// holdForRequesters withholds the reboot while a request holds the
// turn, and reports the wait on the document's own condition.
func holdForRequesters(conv convergence, holding []disruption.Disruption) convergence {
	var names []string
	for _, d := range holding {
		names = append(names, fmt.Sprintf("%s (%s)", d.Metadata.Name, d.Spec.Requester))
	}
	conv.requestReboot = false
	conv.condition = api.Condition{
		Type: conv.condition.Type, Status: api.ConditionFalse, Reason: "Held",
		Message: "this node is drained, and the reboot waits for the requester to remove spec.hold from " + strings.Join(names, ", "),
	}
	return conv
}

// settleHolds keeps each request's Held condition current: True on a
// holding request once the drain is done, and False on a request whose
// requester removed spec.hold. Each transition posts its Event on the
// Disruption.
func settleHolds(r *reader, requests []disruption.Disruption, holding []disruption.Disruption, drained bool, now time.Time) {
	for _, d := range requests {
		var held api.Condition
		switch {
		case drained && !d.Held() && slices.ContainsFunc(holding, func(h disruption.Disruption) bool { return h.Metadata.Name == d.Metadata.Name }):
			held = api.Condition{Type: disruption.HeldCondition, Status: api.ConditionTrue, Reason: "Drained",
				Message: fmt.Sprintf("%s is drained and waits for %s to remove spec.hold", d.Spec.Machine, d.Spec.Requester)}
		case d.Held() && !d.Spec.Hold:
			held = api.Condition{Type: disruption.HeldCondition, Status: api.ConditionFalse, Reason: "Released",
				Message: "spec.hold was removed, so the turn goes on to the reboot"}
		default:
			continue
		}
		held.ObservedGeneration = d.Metadata.Generation
		next := d
		next.Status.Conditions = api.SetCondition(slices.Clone(d.Status.Conditions), held, now)
		// A copy older than this operator's last write gets a 409, and
		// the next pass writes from a fresh one.
		if err := writeRequest(r.client, http.MethodPut, requestPath(d.Metadata.Name)+"/status", &next); err != nil {
			if !apiclient.Stale(err) {
				fmt.Printf("recording the hold on the Disruption %s: %v\n", d.Metadata.Name, err)
			}
			continue
		}
		fmt.Printf("Disruption %s: %s\n", d.Metadata.Name, held.Message)
		r.recorder.Transition(requestReference(&d), held, "")
	}
}
