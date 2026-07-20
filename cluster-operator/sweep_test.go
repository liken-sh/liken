package main

// This file tests the acting halves against a miniature API server:
// one sweep pass listing the fleet, marking the silent machine Lost,
// carrying out the rollout's grants and revocations, and publishing
// the Cluster's status.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// fleetAPI is a miniature API server holding a fleet: machines,
// their heartbeat leases, and the cluster. It answers the sweep's
// list requests and remembers every status it publishes.
type fleetAPI struct {
	clusterDoc *cluster.Cluster
	machines   []machine.Machine
	renewals   map[string]time.Time

	// statuses records each machine-status PUT by machine name.
	// clusterStatus records the cluster's status, and stays nil
	// until something writes it.
	statuses      map[string]*machine.Machine
	clusterStatus *cluster.Cluster
}

func (fake *fleetAPI) handler() http.Handler {
	fake.statuses = map[string]*machine.Machine{}
	microTime := "2006-01-02T15:04:05.000000Z07:00"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/leases"):
			items := []map[string]any{}
			for name, renewed := range fake.renewals {
				items = append(items, map[string]any{
					"metadata": map[string]any{"name": name},
					"spec":     map[string]any{"holderIdentity": name, "renewTime": renewed.UTC().Format(microTime)},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/machines"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": fake.machines})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/clusters"):
			if fake.clusterDoc == nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []cluster.Cluster{}})
				return
			}
			if strings.HasSuffix(r.URL.Path, "/clusters") {
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []cluster.Cluster{*fake.clusterDoc}})
				return
			}
			_ = json.NewEncoder(w).Encode(fake.clusterDoc)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/clusters/"):
			fake.clusterStatus = &cluster.Cluster{}
			_ = json.NewDecoder(r.Body).Decode(fake.clusterStatus)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/machines/"):
			m := &machine.Machine{}
			_ = json.NewDecoder(r.Body).Decode(m)
			fake.statuses[m.Metadata.Name] = m
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func labMachine(name string, phase api.Phase) machine.Machine {
	m := machine.Machine{Kind: "Machine", Metadata: api.ObjectMeta{Name: name}}
	m.Status.Phase = phase
	return m
}

func TestSweepFleetMarksTheSilentMachineAndPublishesTheCluster(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	clusterDoc.Spec.Leaders = []string{"node-1"}
	fake := &fleetAPI{
		clusterDoc: clusterDoc,
		machines: []machine.Machine{
			labMachine("node-1", api.PhaseReady),
			labMachine("node-2", api.PhaseReady),
		},
		renewals: map[string]time.Time{
			"node-1": sweepNow.Add(-10 * time.Second),
			"node-2": sweepNow.Add(-5 * time.Minute),
		},
	}
	client := testClient(t, fake.handler())

	sweepFleet(client, clusterDoc, "", sweepNow)

	lost := fake.statuses["node-2"]
	if lost == nil || lost.Status.Phase != api.PhaseLost {
		t.Fatalf("the silent machine should be marked Lost: %+v", lost)
	}
	if c := api.FindCondition(lost.Status.Conditions, "Ready"); c == nil || c.Reason != "HeartbeatStale" {
		t.Errorf("the Lost verdict explains itself: %+v", c)
	}
	if _, wrote := fake.statuses["node-1"]; wrote {
		t.Error("a fresh machine's status is its own to write")
	}
	if fake.clusterStatus == nil {
		t.Fatal("the sweep publishes the cluster's status")
	}
	if fake.clusterStatus.Status.Machines.Summary != "1/2" || fake.clusterStatus.Status.Phase != api.PhaseDegraded {
		t.Errorf("got %+v", fake.clusterStatus.Status)
	}
}

func TestSweepPublishesTheChannelsAvailableVersion(t *testing.T) {
	// The poller's answer is part of the same status write as
	// everything else the sweep derives. A fresh answer alone is a
	// change worth writing, and it lands at status.releases.available.
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	clusterDoc.Spec.Leaders = []string{"node-1"}
	fake := &fleetAPI{
		clusterDoc: clusterDoc,
		machines:   []machine.Machine{labMachine("node-1", api.PhaseReady)},
		renewals:   map[string]time.Time{"node-1": sweepNow.Add(-10 * time.Second)},
	}
	client := testClient(t, fake.handler())

	sweepFleet(client, clusterDoc, "2026.07.13-002", sweepNow)

	if fake.clusterStatus == nil || fake.clusterStatus.Status.Releases.Available != "2026.07.13-002" {
		t.Fatalf("the channel's answer should reach the status: %+v", fake.clusterStatus)
	}
}

func TestSweepFleetWritesNothingOnASettledFleet(t *testing.T) {
	// The cluster's status already says what this sweep observes.
	// So the pass must not write it again. A settled fleet causes no
	// write.
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	clusterDoc.Spec.Leaders = []string{"node-1"}
	clusterDoc.Status.Phase = api.PhaseReady
	clusterDoc.Status.Machines = cluster.MachineTally{Ready: 1, Total: 1, Summary: "1/1"}
	clusterDoc.Status.Conditions = api.SetCondition(nil, api.Condition{
		Type: "MachinesReady", Status: api.ConditionTrue, Reason: "AllMachinesReady",
		Message: "all 1 machines are ready",
	}, sweepNow.Add(-time.Hour))
	clusterDoc.Status.Conditions = api.SetCondition(clusterDoc.Status.Conditions, api.Condition{
		Type: "Progressing", Status: api.ConditionTrue, Reason: "RolloutComplete",
		Message: "no machines are waiting for a reboot turn",
	}, sweepNow.Add(-time.Hour))
	fake := &fleetAPI{
		clusterDoc: clusterDoc,
		machines:   []machine.Machine{labMachine("node-1", api.PhaseReady)},
		renewals:   map[string]time.Time{"node-1": sweepNow.Add(-10 * time.Second)},
	}
	client := testClient(t, fake.handler())

	sweepFleet(client, clusterDoc, "", sweepNow)

	if fake.clusterStatus != nil {
		t.Errorf("nothing changed, so nothing should be written: %+v", fake.clusterStatus.Status)
	}
	if len(fake.statuses) != 0 {
		t.Errorf("no machine verdicts either: %v", fake.statuses)
	}
}

// refusing wraps a handler so that requests matching one method and
// path fragment are refused with the given status, and everything
// else passes through. This models a sweep that meets one failing
// endpoint on an otherwise healthy API server.
func refusing(inner http.Handler, method, fragment string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == method && strings.Contains(r.URL.Path, fragment) {
			w.WriteHeader(status)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// silentFleet is a two-machine fleet whose node-2 has stopped
// renewing its heartbeat. It is the smallest fleet that has both a
// Lost verdict to write and a cluster status worth publishing.
func silentFleet(clusterDoc *cluster.Cluster) *fleetAPI {
	return &fleetAPI{
		clusterDoc: clusterDoc,
		machines: []machine.Machine{
			labMachine("node-1", api.PhaseReady),
			labMachine("node-2", api.PhaseReady),
		},
		renewals: map[string]time.Time{
			"node-1": sweepNow.Add(-10 * time.Second),
			"node-2": sweepNow.Add(-5 * time.Minute),
		},
	}
}

func TestSweepFleetStopsWhenTheMachineListFails(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := silentFleet(clusterDoc)
	client := testClient(t, refusing(fake.handler(), http.MethodGet, "/machines", http.StatusInternalServerError))

	sweepFleet(client, clusterDoc, "", sweepNow)

	if fake.clusterStatus != nil {
		t.Errorf("a sweep that cannot see the fleet must not judge it: %+v", fake.clusterStatus.Status)
	}
	if len(fake.statuses) != 0 {
		t.Errorf("and must not write any machine verdicts: %v", fake.statuses)
	}
}

func TestSweepFleetStopsWhenTheHeartbeatListFails(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := silentFleet(clusterDoc)
	client := testClient(t, refusing(fake.handler(), http.MethodGet, "/leases", http.StatusInternalServerError))

	sweepFleet(client, clusterDoc, "", sweepNow)

	if fake.clusterStatus != nil {
		t.Errorf("without heartbeats there is no liveness verdict to publish: %+v", fake.clusterStatus.Status)
	}
	if len(fake.statuses) != 0 {
		t.Errorf("and no machine may be judged silent: %v", fake.statuses)
	}
}

func TestSweepFleetToleratesAClusterStatusWriteFailure(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := silentFleet(clusterDoc)
	client := testClient(t, refusing(fake.handler(), http.MethodPut, "/clusters", http.StatusInternalServerError))

	sweepFleet(client, clusterDoc, "", sweepNow)

	if lost := fake.statuses["node-2"]; lost == nil || lost.Status.Phase != api.PhaseLost {
		t.Errorf("the machine verdicts land even when the cluster write fails: %+v", lost)
	}
}

func TestMarkLostConcedesWhenTheMachineWritesFirst(t *testing.T) {
	// A conflict on the Lost write means the machine came back and
	// wrote its own status first. That is exactly the outcome this
	// write exists to allow, so the sweep moves on to the next
	// machine.
	fake := &fleetAPI{}
	client := testClient(t, refusing(fake.handler(), http.MethodPut, "/machines/node-1", http.StatusConflict))
	machines := []machine.Machine{
		labMachine("node-1", api.PhaseReady),
		labMachine("node-2", api.PhaseReady),
	}

	markLost(client, machines, []string{"node-1", "node-2"}, sweepNow)

	if _, wrote := fake.statuses["node-1"]; wrote {
		t.Error("the conflicting write must not land")
	}
	if fake.statuses["node-2"] == nil {
		t.Error("one machine's conflict must not stop the next verdict")
	}
}

func TestMarkLostCarriesOnPastAFailedWrite(t *testing.T) {
	fake := &fleetAPI{}
	client := testClient(t, refusing(fake.handler(), http.MethodPut, "/machines/node-1", http.StatusInternalServerError))
	machines := []machine.Machine{
		labMachine("node-1", api.PhaseReady),
		labMachine("node-2", api.PhaseReady),
	}

	markLost(client, machines, []string{"node-1", "node-2"}, sweepNow)

	if _, wrote := fake.statuses["node-1"]; wrote {
		t.Error("the failed write must not land")
	}
	if fake.statuses["node-2"] == nil {
		t.Error("one machine's write failure must not stop the next verdict")
	}
}

func TestCarryOutRolloutGrantsAndRevokes(t *testing.T) {
	fake := &fleetAPI{}
	client := testClient(t, fake.handler())

	granted := labMachine("node-4", api.PhaseReady)
	granted.Status.Conditions = api.SetCondition(nil, api.Condition{
		Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows",
	}, sweepNow.Add(-time.Minute))
	machines := []machine.Machine{labMachine("node-3", api.PhaseUpdatePending), granted}

	carryOutRollout(client, machines, rollout{grant: []string{"node-3"}, revoke: []string{"node-4"}}, sweepNow)

	grant := fake.statuses["node-3"]
	if grant == nil || api.FindCondition(grant.Status.Conditions, machine.RebootApprovedCondition) == nil {
		t.Errorf("the grant lands on the machine: %+v", grant)
	}
	revoked := fake.statuses["node-4"]
	if revoked == nil || api.FindCondition(revoked.Status.Conditions, machine.RebootApprovedCondition) != nil {
		t.Errorf("the spent grant disappears: %+v", revoked)
	}
}

func TestSweepReadsTheClusterFreshEachPass(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := &fleetAPI{
		clusterDoc: clusterDoc,
		machines:   []machine.Machine{labMachine("node-1", api.PhaseReady)},
		renewals:   map[string]time.Time{"node-1": sweepNow.Add(-10 * time.Second)},
	}
	client := testClient(t, fake.handler())
	sweep(client, "lab", newChannelPoller())
	if fake.clusterStatus == nil {
		t.Error("a pass over a fleet with news publishes the cluster's status")
	}
}

func TestSweepSkipsThePassWhenTheClusterReadFails(t *testing.T) {
	// Every pass reads the Cluster fresh, because its spec drives
	// the rollout. A pass that cannot read the Cluster has no spec
	// to act on, and must do nothing at all.
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := silentFleet(clusterDoc)
	client := testClient(t, refusing(fake.handler(), http.MethodGet, "/clusters", http.StatusInternalServerError))

	sweep(client, "lab", newChannelPoller())

	if fake.clusterStatus != nil {
		t.Errorf("no cluster status without a cluster: %+v", fake.clusterStatus.Status)
	}
	if len(fake.statuses) != 0 {
		t.Errorf("and no machine verdicts either: %v", fake.statuses)
	}
}

func TestAwaitClusterRetriesAfterAFailingList(t *testing.T) {
	// A real failure, not the expected 404 of an unserved CRD, is
	// printed and retried the same way. The wait only ends when a
	// Cluster actually appears.
	calls := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []cluster.Cluster{
			{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}},
		}})
	}))
	clusterDoc := awaitCluster(client)
	if clusterDoc.Metadata.Name != "lab" || calls != 2 {
		t.Errorf("got %q after %d calls", clusterDoc.Metadata.Name, calls)
	}
}

func TestAwaitClusterWaitsForTheFirstCluster(t *testing.T) {
	// The CRD is not served at first, so the first request returns a
	// 404. Then the machine operators seed the object, and the wait
	// resolves itself.
	calls := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []cluster.Cluster{
			{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}},
		}})
	}))
	clusterDoc := awaitCluster(client)
	if clusterDoc.Metadata.Name != "lab" || calls != 2 {
		t.Errorf("got %q after %d calls", clusterDoc.Metadata.Name, calls)
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

// TestMain silences the retry pause. awaitCluster loops on this
// pause while the CRD is not served, and no test wants the real
// five-second wait.
func TestMain(m *testing.M) {
	kubernetes.RetryPause = func() {}
	os.Exit(m.Run())
}
