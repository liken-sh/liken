package main

import (
	"time"
)

// disruptions is one pass's running record of what has already
// started: whether some document requested the reboot, and whether
// a drain is holding one back. The documents pass through the gate
// in a fixed order: the Machine's spec, the cluster document, the
// system release, the registry credentials, and finally the
// demotion. The restart suppression in gate depends on this order.
// A reboot requested by an earlier document silences a later
// document's restart, never the reverse.
type disruptions struct {
	draining  bool
	rebooting bool

	// events posts the drain's cordon about this Machine.
	events machineEvents

	// out records what the drain did not finish, and when the drain's
	// deadline wants a pass.
	out *passOutcome
}

// gate intercepts one document's convergence decision on its way to
// its side effects. A reboot already requested this pass covers any
// restart: the boot path re-renders everything a restart would have
// applied, so a second intent would only add noise. (Init also
// prefers the reboot file when both exist, so this guard is not
// strictly needed, but it does no harm.) A granted reboot goes
// through the drain first (drain.go): the node is cordoned and
// emptied before the intent is written, so workloads move to other
// nodes instead of being killed by the reboot. A pass whose Node
// read failed skips the drain, because during a demotion there is
// no Node to cordon, and the reboot must still happen. The Node's
// copy stops answering after a failed watch (watches.go), so while the
// API server is down the Node read fails and the drain is skipped the
// same way. A Node that reads but whose pods do not list holds the
// reboot (gateThroughDrain): a slow API server must not let a reboot
// kill pods past their disruption budgets.
func (d *disruptions) gate(r *reader, node *nodeObject, nodeErr error, t turn, now time.Time, conv convergence) convergence {
	conv.requestRestart = conv.requestRestart && !d.rebooting
	if conv.requestReboot && t == turnGranted && nodeErr == nil {
		conv = gateThroughDrain(r, node, conv, now, d.events, d.out)
		d.draining = d.draining || !conv.requestReboot
	}
	d.rebooting = d.rebooting || conv.requestReboot
	return conv
}
