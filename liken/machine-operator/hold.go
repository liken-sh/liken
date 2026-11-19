package main

// A Disruption that sets spec.hold lets its requester work on the
// drained machine between the drain and the reboot.
//
// Some changes must run while no workload runs, and take effect at the
// next boot. firmware-operator's Secure Boot database update is one.
// The kernel reads db and dbx only at boot, so the write and the reboot
// belong in the same turn. Otherwise the machine runs with variables
// that its kernel does not enforce until some later reboot.
//
// The turn drains the machine and stops before the reboot.
// machine-operator sets the Held condition on the request, and the
// requester's pod, which tolerates the cordon, does the work. When the
// requester removes spec.hold, the next pass reboots. Once a request is
// Held, the pass does not drain again, because a drain would evict the
// requester's pod.
//
// The hold has no deadline. A requester that never removes spec.hold
// keeps the machine drained until a person removes it. The time of the
// Held condition shows when the hold began, so an alert can read it.

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

// holdingRequests returns, in name order, the requests that set
// spec.hold, are approved, and are not done. An unapproved request is
// not part of the turn, so its hold does not stop the reboot.
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

// handoverRequest returns the name of a request whose requester set
// BootNext for this turn's reboot, as firmware-operator does for a
// capsule, or "" when there is none. The request sets
// spec.keepBootNext, no longer sets spec.hold, is approved, and is not
// done. The reboot intent names it, and init then leaves BootNext as
// the requester set it (init/handover.go). Several such requests all
// describe the same reboot, so the first by name is enough.
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

// holdForRequesters cancels conv's reboot and sets its condition to
// False with reason Held, naming the requests it waits for.
func holdForRequesters(conv convergence, holding []disruption.Disruption) convergence {
	var names []string
	for _, d := range holding {
		names = append(names, fmt.Sprintf("%s (%s)", d.Metadata.Name, d.Spec.Requester))
	}
	conv.requestReboot = false
	conv.condition = api.Condition{
		Type: conv.condition.Type, Status: api.ConditionFalse, Reason: "Held",
		Message: "The Node is drained, and the reboot waits for each requester to remove spec.hold: " + strings.Join(names, ", "),
	}
	return conv
}

// settleHolds sets Held to True on each holding request once the drain
// is done, and to False on a Held request whose requester removed
// spec.hold. Each transition posts an Event on the Disruption.
func settleHolds(r *reader, requests []disruption.Disruption, holding []disruption.Disruption, drained bool, now time.Time) {
	for _, d := range requests {
		var held api.Condition
		switch {
		case drained && !d.Held() && slices.ContainsFunc(holding, func(h disruption.Disruption) bool { return h.Metadata.Name == d.Metadata.Name }):
			held = api.Condition{Type: disruption.HeldCondition, Status: api.ConditionTrue, Reason: "Drained",
				Message: fmt.Sprintf("Machine %s is drained and waits for %s to remove spec.hold", d.Spec.Machine, d.Spec.Requester)}
		case d.Held() && !d.Spec.Hold:
			held = api.Condition{Type: disruption.HeldCondition, Status: api.ConditionFalse, Reason: "Released",
				Message: "spec.hold was removed, so the machine reboots in this turn"}
		default:
			continue
		}
		held.ObservedGeneration = d.Metadata.Generation
		next := d
		next.Status.Conditions = api.SetCondition(slices.Clone(d.Status.Conditions), held, now)
		// A 409 means d is older than the stored object. The next
		// pass writes from a fresh read.
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
