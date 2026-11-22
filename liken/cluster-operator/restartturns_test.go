package main

// This test restarts this program more often than HeartbeatStaleAfter
// during a rollout, as a crash loop or a pod evicted again and again
// does. Each copy starts with no heartbeat record. A copy may trust a
// machine's status only after it has seen that machine renew its
// heartbeat Lease, so a machine that is down while its status still
// reads Ready never counts as up, however often the program restarts.

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/election/electiontest"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/machine"
)

// finishedTurnFleet is raceFleet after node-1's turn: node-1 is back,
// Ready, and requests nothing, but still holds its grant and its entry
// in status.rebootTurns. node-2 waits for a turn.
func finishedTurnFleet(t *testing.T) *fakeapi.Server {
	t.Helper()
	node1 := machine.Machine{APIVersion: api.APIVersion, Kind: "Machine",
		Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "5", UID: "uid-node-1"}}
	node1.Status.Phase = api.PhaseReady
	node1.Status.Conditions = api.SetCondition(nil, api.Condition{
		Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows",
	}, passTime.Add(-time.Hour))
	clusterDoc := cluster.Cluster{APIVersion: api.APIVersion, Kind: "Cluster",
		Metadata: api.ObjectMeta{Name: "lab", ResourceVersion: "5", UID: "uid-lab"}}
	clusterDoc.Spec.Leaders = []string{"node-1", "node-2", "node-3"}
	clusterDoc.Spec.Disruption.MaxUnavailable = 1
	clusterDoc.Status.RebootTurns = []cluster.RebootTurn{{Machine: "node-1", MachineResourceVersion: "5", Since: passTime.Add(-time.Hour)}}
	return raceFleetWith(t, map[string]*fakeapi.Collection{
		kubernetes.MachinesPath: {APIVersion: api.APIVersion, Kind: "Machine", Items: []map[string]any{
			asObject(t, node1), fleetMachine(t, "node-2", true), fleetMachine(t, "node-3", false),
		}},
		kubernetes.ClustersPath: {APIVersion: api.APIVersion, Kind: "Cluster", Items: []map[string]any{asObject(t, clusterDoc)}},
	})
}

// copyLifetime is how long each copy runs before the next replaces it,
// as in a crash loop of a pod that the kubelet restarts at once.
const copyLifetime = 15 * time.Second

func TestAnOperatorThatRestartsMoreOftenThanTheHeartbeatWindowFinishesTheTurnAndGrantsOnlyOnRenewals(t *testing.T) {
	cases := []struct {
		name     string
		renewing []string
		granted  []string
	}{
		{"every machine renews its heartbeat, so node-1's turn ends and node-2 gets one",
			[]string{"node-1", "node-2", "node-3"}, []string{"node-2"}},
		{"node-3 is down while its status reads Ready, so node-1's turn ends and node-2 gets none",
			[]string{"node-1", "node-2"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fake := finishedTurnFleet(t)
				machineOperators := testClient(t, fake)
				lease := electiontest.New(leaseName, leaseNamespace)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				go renewHeartbeats(ctx, machineOperators, c.renewing...)

				for copyNumber := range 8 {
					stop, end := context.WithCancel(ctx)
					done := make(chan struct{})
					go func() {
						defer close(done)
						runCopy(t, fmt.Sprintf("liken-cluster-operator-%d", copyNumber), lease.Config(t), fake, stop, end)
					}()
					time.Sleep(copyLifetime)
					end()
					<-done
				}

				if got := grantHolders(t, machineOperators); !slices.Equal(got, c.granted) {
					t.Errorf("machines with a grant = %q, want %q", got, c.granted)
				}
				cancel()
				synctest.Wait()
			})
		})
	}
}
