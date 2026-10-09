package main

// These tests run a new process's first sweeps against a fleet with a
// rollout already waiting, in a synctest bubble. A new process records
// every heartbeat Lease as seen when it starts, so for its first
// HeartbeatStaleAfter a machine that is already down reads its last
// status, and the rollout must grant no turn until that time passes.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// awaitTurn makes node-2, the worker of newFleetAPI's fleet, ask for a
// reboot turn.
func awaitTurn(t *testing.T, client *apiclient.Client) {
	t.Helper()
	waiting, err := kubernetes.GetMachine(client, "node-2")
	if err != nil {
		t.Fatal(err)
	}
	status := waiting.Status
	status.Conditions = []api.Condition{{Type: "SpecConverged", Status: api.ConditionFalse, Reason: "AwaitingTurn"}}
	if _, err := kubernetes.PublishStatus(client, waiting, &status); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
}

// sweepFresh runs one sweep, and answers node-1 and node-2 and the
// Cluster as the sweep left them in the API server.
func sweepFresh(t *testing.T, r *fleetReader, client *apiclient.Client) (*machine.Machine, *machine.Machine, *cluster.Cluster) {
	t.Helper()
	cm, _ := fleetMetrics(t)
	if err := sweep(r, "lab", newChannelPoller(), &engineProbe{}, &podSteward{}, cm); err != nil {
		t.Fatal(err)
	}
	leader, err := kubernetes.GetMachine(client, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := kubernetes.GetMachine(client, "node-2")
	if err != nil {
		t.Fatal(err)
	}
	clusterDoc, err := apiclient.Get[cluster.Cluster](client, clusterPath("lab"))
	if err != nil {
		t.Fatal(err)
	}
	return leader, worker, clusterDoc
}

func granted(m *machine.Machine) bool {
	return api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition) != nil
}

// A new process grants no turn while its record of the heartbeats is
// younger than HeartbeatStaleAfter, even when every machine reads
// Ready, and says so on the Cluster's Progressing condition.
func TestANewProcessGrantsNoTurnUntilItCanJudgeTheHeartbeats(t *testing.T) {
	for _, after := range []time.Duration{0, kubernetes.HeartbeatStaleAfter - time.Second} {
		t.Run(after.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, client := startWatching(t, time.Now().Add(-time.Hour))
				awaitTurn(t, client)
				renewFor(t, client, after, []string{"node-1", "node-2"})

				leader, worker, clusterDoc := sweepFresh(t, r, client)

				if leader.Status.Phase != api.PhaseReady {
					t.Errorf("node-1 reads %s, want %s", leader.Status.Phase, api.PhaseReady)
				}
				if granted(worker) {
					t.Errorf("node-2 holds a reboot turn %s after the process started", after)
				}
				progressing := api.FindCondition(clusterDoc.Status.Conditions, "Progressing")
				if progressing == nil || !strings.Contains(progressing.Message, "heartbeat") {
					t.Errorf("the Progressing condition gives no reason for the hold: %+v", progressing)
				}
			})
		})
	}
}

// Once the first sweep's read of the Leases is older than
// HeartbeatStaleAfter, a machine whose
// Lease never changed reads Lost and fills the disruption budget, and
// a fleet whose machines all renew gets its turn as before.
func TestANewProcessGrantsTurnsOnceItCanJudgeTheHeartbeats(t *testing.T) {
	cases := []struct {
		name        string
		renewing    []string
		leaderPhase api.Phase
		grant       bool
	}{
		{"every machine renews", []string{"node-1", "node-2"}, api.PhaseReady, true},
		{"the leader never renews", []string{"node-2"}, api.PhaseLost, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, client := startWatching(t, time.Now().Add(-time.Hour))
				awaitTurn(t, client)
				sweepFresh(t, r, client)
				renewFor(t, client, kubernetes.HeartbeatStaleAfter+time.Second, c.renewing)

				leader, worker, _ := sweepFresh(t, r, client)

				if leader.Status.Phase != c.leaderPhase {
					t.Errorf("node-1 reads %s, want %s", leader.Status.Phase, c.leaderPhase)
				}
				if granted(worker) != c.grant {
					t.Errorf("node-2 granted = %v, want %v", granted(worker), c.grant)
				}
			})
		})
	}
}
