package main

// A machine's phase as the fleet should read it.
//
// Each machine derives its own phase from its own conditions
// (machine-operator/phase.go), but a written status is only as
// current as the machine that wrote it, and a silent machine may no
// longer exist. This is the fleet-side correction: trust the
// machine's claim while its heartbeat is fresh, and read silence as
// Lost. The one phase a machine can never derive for itself is
// exactly the one this program exists to write.

import (
	"time"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// effectivePhase is a machine's phase corrected for liveness: its
// own claim when its heartbeat is fresh, and Lost when it has gone
// silent. A machine with no lease at all has never been heard from.
// Every machine is judged the same way, including whichever one
// hosts this program's pod: the observer is not a machine, so there
// is no self to exempt. Both the fleet sweep and the rollout
// conductor judge machines through this lens.
//
// Silence is not always trouble: a machine holding a reboot grant
// (rollout.go) was told to go down, so until the grant is old enough
// to count as a stall, the sweep treats its silence as the reboot in
// progress.
func effectivePhase(m *machine.Machine, renewals map[string]time.Time, now time.Time) machine.Phase {
	renewed, heard := renewals[m.Metadata.Name]
	if heard && now.Sub(renewed) <= kubernetes.HeartbeatStaleAfter {
		return m.Status.Phase
	}
	if grant := machine.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition); grant != nil &&
		now.Sub(grant.LastTransitionTime) <= rolloutStallAfter {
		return machine.PhaseUpdating
	}
	return machine.PhaseLost
}
