package main

// A newly started process treats every heartbeat Lease as just renewed,
// so for its first HeartbeatStaleAfter a machine that is already down
// still reads as the phase it last wrote. These tests check that the
// rollout grants no reboot turn in that time unless the process has
// seen a renewal of every Lease that could fill the budget.

import (
	"encoding/json"
	"net/http"
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

// awaitTurn makes node-2, the worker of newFleetAPI's fleet, request a
// reboot turn.
func awaitTurn(t *testing.T, client *apiclient.Client) {
	t.Helper()
	awaitTurnOf(t, client, "node-2")
}

func awaitTurnOf(t *testing.T, client *apiclient.Client, name string) {
	t.Helper()
	waiting, err := kubernetes.GetMachine(client, name)
	if err != nil {
		t.Fatalf("reading Machine %s: %v", name, err)
	}
	status := waiting.Status
	status.Conditions = []api.Condition{{Type: "SpecConverged", Status: api.ConditionFalse, Reason: "AwaitingTurn"}}
	if _, err := kubernetes.PublishStatus(client, waiting, &status); err != nil {
		t.Fatalf("publishing the status of Machine %s: %v", name, err)
	}
	fileRequest(t, client, name)
	synctest.Wait()
}

// fileRequest creates an approved Disruption for a machine, as its
// machine-operator does for the update it staged.
func fileRequest(t *testing.T, client *apiclient.Client, name string) {
	t.Helper()
	body, err := json.Marshal(requestObject(t, approvedRequest(name)))
	if err != nil {
		t.Fatalf("encoding the Disruption of Machine %s: %v", name, err)
	}
	if err := client.RequestJSON(http.MethodPost, kubernetes.DisruptionsPath, body, nil); err != nil {
		t.Fatalf("creating the Disruption of Machine %s: %v", name, err)
	}
}

// reconcileAndRead runs one pass, then reads node-1, node-2, and the Cluster
// from the API server.
func reconcileAndRead(t *testing.T, r *fleetReader, client *apiclient.Client) (*machine.Machine, *machine.Machine, *cluster.Cluster) {
	t.Helper()
	cm, _ := fleetMetrics(t)
	if err := reconcile(r, "lab", newChannelPoller(), &fluxProbe{}, &overduePodReports{}, cm); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	leader, err := kubernetes.GetMachine(client, "node-1")
	if err != nil {
		t.Fatalf("reading Machine node-1: %v", err)
	}
	worker, err := kubernetes.GetMachine(client, "node-2")
	if err != nil {
		t.Fatalf("reading Machine node-2: %v", err)
	}
	clusterDoc, err := apiclient.Get[cluster.Cluster](client, clusterPath("lab"))
	if err != nil {
		t.Fatalf("reading Cluster lab: %v", err)
	}
	return leader, worker, clusterDoc
}

func granted(m *machine.Machine) bool {
	return api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition) != nil
}

// The Progressing condition of the Cluster gives the reason for the wait.
func TestANewProcessGrantsNoTurnUntilItCanTrustTheHeartbeats(t *testing.T) {
	cases := []struct {
		name     string
		after    time.Duration
		renewing []string
	}{
		{"no Lease has been renewed since the process started", 0, []string{"node-1", "node-2"}},
		{"the leader has not renewed its Lease since the process started", kubernetes.HeartbeatStaleAfter - time.Second, []string{"node-2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, client := startWatching(t, time.Now().Add(-time.Hour))
				awaitTurn(t, client)
				renewFor(t, client, c.after, c.renewing)

				leader, worker, clusterDoc := reconcileAndRead(t, r, client)

				if leader.Status.Phase != api.PhaseReady {
					t.Errorf("node-1 phase is %s, want %s", leader.Status.Phase, api.PhaseReady)
				}
				if granted(worker) {
					t.Errorf("node-2 has a reboot turn %s after the process started, want none", c.after)
				}
				progressing := api.FindCondition(clusterDoc.Status.Conditions, "Progressing")
				if progressing == nil || !strings.Contains(progressing.Message, "heartbeat") {
					t.Errorf("the Progressing condition is %+v, want a message that mentions heartbeat", progressing)
				}
			})
		})
	}
}

// A process that has seen every machine renew its Lease grants a turn
// at once. After HeartbeatStaleAfter, a machine whose Lease never
// changed reads Lost and uses up the disruption budget, so node-2 gets
// no turn.
func TestANewProcessGrantsTurnsOnceItCanTrustTheHeartbeats(t *testing.T) {
	cases := []struct {
		name        string
		renewFor    time.Duration
		renewing    []string
		leaderPhase api.Phase
		grant       bool
	}{
		{"every machine renews once", kubernetes.HeartbeatRenewAfter, []string{"node-1", "node-2"}, api.PhaseReady, true},
		{"every machine renews", kubernetes.HeartbeatStaleAfter + time.Second, []string{"node-1", "node-2"}, api.PhaseReady, true},
		{"the leader never renews", kubernetes.HeartbeatStaleAfter + time.Second, []string{"node-2"}, api.PhaseLost, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, client := startWatching(t, time.Now().Add(-time.Hour))
				awaitTurn(t, client)
				reconcileAndRead(t, r, client)
				renewFor(t, client, c.renewFor, c.renewing)

				leader, worker, _ := reconcileAndRead(t, r, client)

				if leader.Status.Phase != c.leaderPhase {
					t.Errorf("node-1 phase is %s, want %s", leader.Status.Phase, c.leaderPhase)
				}
				if granted(worker) != c.grant {
					t.Errorf("node-2 granted = %v, want %v", granted(worker), c.grant)
				}
			})
		})
	}
}
