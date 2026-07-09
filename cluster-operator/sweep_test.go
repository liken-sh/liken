package main

// The acting halves against a miniature API server: one sweep pass
// listing the fleet, marking the silent machine Lost, carrying out
// the rollout's grants and revocations, and publishing the Cluster's
// status.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chrisguidry/liken/kubernetes"
	"github.com/chrisguidry/liken/machine"
)

// fleetAPI is a miniature API server holding a fleet: machines,
// their heartbeat leases, and the cluster, answering the sweep's
// lists and remembering every status it publishes.
type fleetAPI struct {
	cluster  *machine.Cluster
	machines []machine.Machine
	renewals map[string]time.Time

	// statuses records each machine-status PUT by machine name;
	// clusterStatus records the cluster's, nil until written.
	statuses      map[string]*machine.Machine
	clusterStatus *machine.Cluster
}

func (api *fleetAPI) handler() http.Handler {
	api.statuses = map[string]*machine.Machine{}
	microTime := "2006-01-02T15:04:05.000000Z07:00"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/leases"):
			items := []map[string]any{}
			for name, renewed := range api.renewals {
				items = append(items, map[string]any{
					"metadata": map[string]any{"name": name},
					"spec":     map[string]any{"holderIdentity": name, "renewTime": renewed.UTC().Format(microTime)},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/machines"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": api.machines})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/clusters"):
			if api.cluster == nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []machine.Cluster{}})
				return
			}
			if strings.HasSuffix(r.URL.Path, "/clusters") {
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []machine.Cluster{*api.cluster}})
				return
			}
			_ = json.NewEncoder(w).Encode(api.cluster)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/clusters/"):
			api.clusterStatus = &machine.Cluster{}
			_ = json.NewDecoder(r.Body).Decode(api.clusterStatus)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/machines/"):
			m := &machine.Machine{}
			_ = json.NewDecoder(r.Body).Decode(m)
			api.statuses[m.Metadata.Name] = m
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func labMachine(name string, phase machine.Phase) machine.Machine {
	m := machine.Machine{Kind: "Machine", Metadata: machine.ObjectMeta{Name: name}}
	m.Status.Phase = phase
	return m
}

func TestSweepFleetMarksTheSilentMachineAndPublishesTheCluster(t *testing.T) {
	cluster := &machine.Cluster{Kind: "Cluster", Metadata: machine.ObjectMeta{Name: "lab"}}
	cluster.Spec.Leaders = []string{"node-1"}
	api := &fleetAPI{
		cluster: cluster,
		machines: []machine.Machine{
			labMachine("node-1", machine.PhaseReady),
			labMachine("node-2", machine.PhaseReady),
		},
		renewals: map[string]time.Time{
			"node-1": sweepNow.Add(-10 * time.Second),
			"node-2": sweepNow.Add(-5 * time.Minute),
		},
	}
	client := testClient(t, api.handler())

	sweepFleet(client, cluster, sweepNow)

	lost := api.statuses["node-2"]
	if lost == nil || lost.Status.Phase != machine.PhaseLost {
		t.Fatalf("the silent machine should be marked Lost: %+v", lost)
	}
	if c := machine.FindCondition(lost.Status.Conditions, "Ready"); c == nil || c.Reason != "HeartbeatStale" {
		t.Errorf("the Lost verdict explains itself: %+v", c)
	}
	if _, wrote := api.statuses["node-1"]; wrote {
		t.Error("a fresh machine's status is its own to write")
	}
	if api.clusterStatus == nil {
		t.Fatal("the sweep publishes the cluster's status")
	}
	if api.clusterStatus.Status.Machines.Summary != "1/2" || api.clusterStatus.Status.Phase != machine.PhaseDegraded {
		t.Errorf("got %+v", api.clusterStatus.Status)
	}
}

func TestSweepFleetWritesNothingOnASettledFleet(t *testing.T) {
	// The cluster's status already says what this sweep observes, so
	// the pass must not write it again: a settled fleet writes
	// nothing.
	cluster := &machine.Cluster{Kind: "Cluster", Metadata: machine.ObjectMeta{Name: "lab"}}
	cluster.Spec.Leaders = []string{"node-1"}
	cluster.Status.Phase = machine.PhaseReady
	cluster.Status.Machines = machine.MachineTally{Ready: 1, Total: 1, Summary: "1/1"}
	cluster.Status.Conditions = machine.SetCondition(nil, machine.Condition{
		Type: "MachinesReady", Status: machine.ConditionTrue, Reason: "AllMachinesReady",
		Message: "all 1 machines are ready",
	}, sweepNow.Add(-time.Hour))
	cluster.Status.Conditions = machine.SetCondition(cluster.Status.Conditions, machine.Condition{
		Type: "Progressing", Status: machine.ConditionTrue, Reason: "RolloutComplete",
		Message: "no machines are waiting for a reboot turn",
	}, sweepNow.Add(-time.Hour))
	api := &fleetAPI{
		cluster:  cluster,
		machines: []machine.Machine{labMachine("node-1", machine.PhaseReady)},
		renewals: map[string]time.Time{"node-1": sweepNow.Add(-10 * time.Second)},
	}
	client := testClient(t, api.handler())

	sweepFleet(client, cluster, sweepNow)

	if api.clusterStatus != nil {
		t.Errorf("nothing changed, so nothing should be written: %+v", api.clusterStatus.Status)
	}
	if len(api.statuses) != 0 {
		t.Errorf("no machine verdicts either: %v", api.statuses)
	}
}

func TestCarryOutRolloutGrantsAndRevokes(t *testing.T) {
	api := &fleetAPI{}
	client := testClient(t, api.handler())

	granted := labMachine("node-4", machine.PhaseReady)
	granted.Status.Conditions = machine.SetCondition(nil, machine.Condition{
		Type: machine.RebootApprovedCondition, Status: machine.ConditionTrue, Reason: "DisruptionBudgetAllows",
	}, sweepNow.Add(-time.Minute))
	machines := []machine.Machine{labMachine("node-3", machine.PhaseUpdatePending), granted}

	carryOutRollout(client, machines, rollout{grant: []string{"node-3"}, revoke: []string{"node-4"}}, sweepNow)

	grant := api.statuses["node-3"]
	if grant == nil || machine.FindCondition(grant.Status.Conditions, machine.RebootApprovedCondition) == nil {
		t.Errorf("the grant lands on the machine: %+v", grant)
	}
	revoked := api.statuses["node-4"]
	if revoked == nil || machine.FindCondition(revoked.Status.Conditions, machine.RebootApprovedCondition) != nil {
		t.Errorf("the spent grant disappears: %+v", revoked)
	}
}

func TestSweepReadsTheClusterFreshEachPass(t *testing.T) {
	cluster := &machine.Cluster{Kind: "Cluster", Metadata: machine.ObjectMeta{Name: "lab"}}
	api := &fleetAPI{
		cluster:  cluster,
		machines: []machine.Machine{labMachine("node-1", machine.PhaseReady)},
		renewals: map[string]time.Time{"node-1": sweepNow.Add(-10 * time.Second)},
	}
	client := testClient(t, api.handler())
	sweep(client, "lab")
	if api.clusterStatus == nil {
		t.Error("a pass over a fleet with news publishes the cluster's status")
	}
}

func TestAwaitClusterWaitsForTheFirstCluster(t *testing.T) {
	// The CRD isn't served at first (404), then the machine operators
	// seed the object; the wait resolves itself.
	calls := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []machine.Cluster{
			{Kind: "Cluster", Metadata: machine.ObjectMeta{Name: "lab"}},
		}})
	}))
	cluster := awaitCluster(client)
	if cluster.Metadata.Name != "lab" || calls != 2 {
		t.Errorf("got %q after %d calls", cluster.Metadata.Name, calls)
	}
}

func TestDrainEventsEmptiesTheQueue(t *testing.T) {
	events := make(chan *machine.Machine, 4)
	events <- &machine.Machine{}
	events <- &machine.Machine{}
	drainEvents(events)
	if len(events) != 0 {
		t.Errorf("the burst is fully drained: %d left", len(events))
	}
}

// TestMain silences the retry pause: awaitCluster loops on it while
// the CRD isn't served, and no test wants the real five-second wait.
func TestMain(m *testing.M) {
	kubernetes.RetryPause = func() {}
	os.Exit(m.Run())
}
