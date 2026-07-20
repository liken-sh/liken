package main

// This file computes a machine's phase as the fleet should read it.
//
// Each machine derives its own phase from its own conditions (see
// machine-operator/phase.go). But a written status is only as
// current as the machine that wrote it, and a silent machine may no
// longer exist. This file makes the fleet-side correction: trust the
// machine's claim while its heartbeat is fresh, and read silence as
// Lost. The one phase a machine can never derive for itself is
// exactly the phase this program exists to write.

import (
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// effectivePhase returns a machine's phase, corrected for liveness.
// It returns the machine's own claim when its heartbeat is fresh,
// and Lost when the machine has gone silent. A machine with no lease
// at all has never been heard from. This function judges every
// machine the same way, including whichever machine hosts this
// program's pod: this program is not itself a machine, so no machine
// is exempt. Both the fleet sweep and the rollout use this function
// to judge machines.
//
// Silence does not always mean trouble. A machine holding a reboot
// grant (see rollout.go) was told to go down. So until the grant is
// old enough to count as a stall, the sweep treats the machine's
// silence as a reboot in progress.
func effectivePhase(m *machine.Machine, renewals map[string]time.Time, now time.Time) api.Phase {
	renewed, heard := renewals[m.Metadata.Name]
	if heard && now.Sub(renewed) <= kubernetes.HeartbeatStaleAfter {
		return m.Status.Phase
	}
	if grant := api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition); grant != nil &&
		now.Sub(grant.LastTransitionTime) <= rolloutStallAfter {
		return api.PhaseUpdating
	}
	return api.PhaseLost
}
