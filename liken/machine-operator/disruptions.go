package main

import (
	"slices"
	"time"

	"github.com/liken-sh/liken/liken/disruption"
)

// disruptions records what one reconcile pass has done so far about
// reboots and drains. Each document's convergence decision goes
// through gate in this order: the Machine's spec, the cluster
// document, the system release, the registry credentials, and the
// demotion. The order matters because gate drops the restart of a
// document that comes after a reboot it let through, but cannot take
// back a restart it already let through.
type disruptions struct {
	draining  bool
	rebooting bool

	// holding lists the requests that keep this machine's turn after
	// its drain (hold.go). drained is true when the drain finished
	// while a request held the turn, and the pass then sets the Held
	// condition of those requests.
	holding []disruption.Disruption
	drained bool

	// handover is the name of the request whose requester set BootNext
	// for this turn's reboot (handoverRequest), or empty when none did.
	handover string

	// events posts the Events about this Machine that the drain
	// writes, such as the cordon.
	events machineEvents

	// out records the work that the drain left unfinished and the time
	// when the next pass must run to check the drain's deadline.
	out *passOutcome
}

// gate takes one document's convergence decision and returns the
// decision to carry out. It drops a restart when this pass already
// requested a reboot, because the boot re-renders everything that the
// restart would apply. Init also prefers the reboot file when both
// exist, so the drop only avoids writing a second request.
//
// A granted reboot goes through the drain first (drain.go), so the
// node's workloads move to other nodes before the reboot request is
// written. The drain is skipped when the pass failed to read the Node.
// During a demotion there is no Node to cordon and the reboot must
// still happen. While the API server is down, the operator does not use
// its cached Node (watches.go), so the read fails and the drain is
// skipped the same way. When the Node reads but its pods do not list,
// gateThroughDrain holds the reboot, because a slow API server must not
// let a reboot kill pods past their disruption budgets.
//
// A request that holds the turn stops the reboot after the drain
// finishes (hold.go). That turn ends in a reboot, which applies
// everything a restart would, so no restart runs while a request holds
// the turn. Once a request is Held, the gate does not drain again,
// because the requester's pod runs on the cordoned node.
//
// Every reboot that the gate lets through has the handover as its
// keepBootNext, because init acts on whichever document's reboot
// request it reads first, and the requester's BootNext must survive
// that reboot (hold.go).
func (d *disruptions) gate(r *reader, node *nodeObject, nodeErr error, t turn, now time.Time, conv convergence) convergence {
	holding := t == turnGranted && len(d.holding) > 0
	conv.requestRestart = conv.requestRestart && !d.rebooting && !holding
	if conv.requestReboot && t == turnGranted && nodeErr == nil {
		if !slices.ContainsFunc(d.holding, func(h disruption.Disruption) bool { return h.Held() }) {
			conv = gateThroughDrain(r, node, conv, now, d.events, d.out)
		}
		if conv.requestReboot && holding {
			d.drained = true
			conv = holdForRequesters(conv, d.holding)
		}
		d.draining = d.draining || !conv.requestReboot
	}
	if conv.requestReboot && t == turnGranted {
		conv.keepBootNext = d.handover
	}
	d.rebooting = d.rebooting || conv.requestReboot
	return conv
}
