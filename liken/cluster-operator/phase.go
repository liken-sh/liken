package main

import (
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// effectivePhase returns the phase that the fleet uses for a machine.
// Each machine writes its own phase to its status, but a machine that
// stops running cannot update it. So the written phase counts only
// while the machine's heartbeat Lease is fresh, and otherwise the
// machine is Lost.
//
// changedAt maps each Lease name to the time this program saw its
// renewTime change, on this program's clock (heartbeatSightings.changeTimes).
// Freshness uses this program's clock, not the renewTime itself, so a
// machine with a wrong clock does not read Lost too early or too late.
//
// A machine that holds a reboot grant goes silent while it reboots, so
// it reads Updating instead of Lost until the grant is older than
// rolloutStallAfter.
func effectivePhase(m *machine.Machine, changedAt map[string]time.Time, now time.Time) api.Phase {
	changed, ok := changedAt[m.Metadata.Name]
	if ok && now.Sub(changed) <= kubernetes.HeartbeatStaleAfter {
		return m.Status.Phase
	}
	if grant := api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition); grant != nil &&
		now.Sub(grant.LastTransitionTime) <= rolloutStallAfter {
		return api.PhaseUpdating
	}
	return api.PhaseLost
}
