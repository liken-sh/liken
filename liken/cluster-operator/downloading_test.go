package main

// The conductor's view of a machine that downloads a release.

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/api"
)

// A machine that downloads a release serves its workloads, so the
// download takes no slot of the disruption budget. A download that
// never finishes must not hold every other machine's turn.
func TestRolloutADownloadTakesNoSlotOfTheBudget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseDownloading, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)

	r := decideRollout(machines, renewals, labCluster(0), "", sweepNow)

	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants = %v, want node-4: node-3's download takes no slot of the budget of one", r.grant)
	}
}

// A fleet whose machines download a release is updating, not
// degraded.
func TestSweepCountsADownloadAsUpdating(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, fresh},
		fleetEntry{"node-3", api.PhaseDownloading, fresh},
	)

	s := decideFleetSweep(machines, renewals, sweepNow)

	if s.phase != api.PhaseUpdating || s.condition.Reason != "MachinesUpdating" {
		t.Errorf("the fleet is %s (%s), want Updating", s.phase, s.condition.Reason)
	}
}
