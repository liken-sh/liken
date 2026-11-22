package main

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/api"
)

// A machine that is downloading a release still runs its workloads,
// and a download that never finishes must not block the other
// machines.
func TestRolloutDoesNotCountADownloadAgainstTheBudget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseDownloading, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)

	r := decideFor(machines, renewals, heartbeatTrust{}, labCluster(0), "", passTime)

	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants are %v, want node-4, because node-3's download does not count against the budget of one", r.grant)
	}
}

func TestDecideFleetCountsADownloadAsUpdating(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, fresh},
		fleetEntry{"node-3", api.PhaseDownloading, fresh},
	)

	s := decideFleet(machines, renewals, passTime)

	if s.phase != api.PhaseUpdating || s.condition.Reason != "MachinesUpdating" {
		t.Errorf("the fleet is %s (%s), want Updating", s.phase, s.condition.Reason)
	}
}
