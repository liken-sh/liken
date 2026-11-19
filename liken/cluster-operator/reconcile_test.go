package main

// This file tests the writing half of the pass against a fake API
// server: listing the fleet, marking a silent machine Lost, applying
// the rollout's grants and revocations, and publishing the Cluster's
// status.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// fleetAPI is a fake API server that serves a fleet (machines, their
// heartbeat leases, and the Cluster) and records every status the
// pass writes.
type fleetAPI struct {
	clusterDoc *cluster.Cluster
	machines   []machine.Machine
	renewals   map[string]time.Time

	// statuses records each Machine status PUT by machine name.
	// clusterStatus stays nil until the pass writes the Cluster's
	// status.
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

// sawEachRenewal records each of the fake's Lease renewals as seen
// when it was made, as the Lease watch of an operator that has been
// leader since before the renewals would. An operator that first sees
// a Lease treats it as renewed at that moment (heartbeats.go), so
// without these records no machine would be marked Lost.
func (fake *fleetAPI) sawEachRenewal(r *fleetReader) *fleetReader {
	for name, renewed := range fake.renewals {
		r.sightings.observe(name, renewed, renewed)
	}
	return r
}

func labMachine(name string, phase api.Phase) machine.Machine {
	m := machine.Machine{Kind: "Machine", Metadata: api.ObjectMeta{Name: name}}
	m.Status.Phase = phase
	return m
}

func TestReconcileFleetMarksTheSilentMachineAndPublishesTheCluster(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	clusterDoc.Spec.Leaders = []string{"node-1"}
	fake := &fleetAPI{
		clusterDoc: clusterDoc,
		machines: []machine.Machine{
			labMachine("node-1", api.PhaseReady),
			labMachine("node-2", api.PhaseReady),
		},
		renewals: map[string]time.Time{
			"node-1": passTime.Add(-10 * time.Second),
			"node-2": passTime.Add(-5 * time.Minute),
		},
	}
	client := testClient(t, fake.handler())

	cm, _ := fleetMetrics(t)
	reconcileFleet(fake.sawEachRenewal(&fleetReader{client: client}), clusterDoc, "", &fluxProbe{}, &overduePodReports{}, cm, passTime)

	lost := fake.statuses["node-2"]
	if lost == nil || lost.Status.Phase != api.PhaseLost {
		t.Fatalf("node-2 status is %+v, want phase Lost because its heartbeat is stale", lost)
	}
	if c := api.FindCondition(lost.Status.Conditions, "Ready"); c == nil || c.Reason != "HeartbeatStale" {
		t.Errorf("node-2 Ready condition is %+v, want reason HeartbeatStale", c)
	}
	if _, wrote := fake.statuses["node-1"]; wrote {
		t.Error("the pass wrote node-1's status, want no write because its heartbeat is fresh")
	}
	if fake.clusterStatus == nil {
		t.Fatal("the pass wrote no Cluster status, want one")
	}
	if fake.clusterStatus.Status.Machines.Summary != "1/2" || fake.clusterStatus.Status.Phase != api.PhaseDegraded {
		t.Errorf("Cluster status is %+v, want machine summary 1/2 and phase Degraded", fake.clusterStatus.Status)
	}
}

func TestReconcileFleetPublishesTheChannelsAvailableVersion(t *testing.T) {
	// A new version from the channel poller is enough to make the
	// pass write the Cluster's status.
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	clusterDoc.Spec.Leaders = []string{"node-1"}
	fake := &fleetAPI{
		clusterDoc: clusterDoc,
		machines:   []machine.Machine{labMachine("node-1", api.PhaseReady)},
		renewals:   map[string]time.Time{"node-1": passTime.Add(-10 * time.Second)},
	}
	client := testClient(t, fake.handler())

	cm, _ := fleetMetrics(t)
	reconcileFleet(&fleetReader{client: client}, clusterDoc, "2026.07.13-002", &fluxProbe{}, &overduePodReports{}, cm, passTime)

	if fake.clusterStatus == nil || fake.clusterStatus.Status.Releases.Available != "2026.07.13-002" {
		t.Fatalf("Cluster status is %+v, want releases.available 2026.07.13-002", fake.clusterStatus)
	}
}

func TestReconcileFleetWritesNothingWhenNothingChanged(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	clusterDoc.Spec.Leaders = []string{"node-1"}
	clusterDoc.Status.Phase = api.PhaseReady
	clusterDoc.Status.Machines = cluster.MachineTally{Ready: 1, Total: 1, Summary: "1/1"}
	clusterDoc.Status.Conditions = api.SetCondition(nil, api.Condition{
		Type: "MachinesReady", Status: api.ConditionTrue, Reason: "AllMachinesReady",
		Message: "All 1 machines are ready",
	}, passTime.Add(-time.Hour))
	clusterDoc.Status.Conditions = api.SetCondition(clusterDoc.Status.Conditions, api.Condition{
		Type: "Progressing", Status: api.ConditionTrue, Reason: "RolloutComplete",
		Message: "No machine waits for a reboot turn",
	}, passTime.Add(-time.Hour))
	fake := &fleetAPI{
		clusterDoc: clusterDoc,
		machines:   []machine.Machine{labMachine("node-1", api.PhaseReady)},
		renewals:   map[string]time.Time{"node-1": passTime.Add(-10 * time.Second)},
	}
	client := testClient(t, fake.handler())

	cm, _ := fleetMetrics(t)
	reconcileFleet(&fleetReader{client: client}, clusterDoc, "", &fluxProbe{}, &overduePodReports{}, cm, passTime)

	if fake.clusterStatus != nil {
		t.Errorf("the pass wrote Cluster status %+v, want no write because nothing changed", fake.clusterStatus.Status)
	}
	if len(fake.statuses) != 0 {
		t.Errorf("the pass wrote Machine statuses %v, want none because nothing changed", fake.statuses)
	}
}

// A tearDownFlux refusal appears as a Cluster condition, next to the
// MachinesReady and Progressing conditions.
func TestPublishClusterStatusWritesTheFluxRefusal(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := &fleetAPI{clusterDoc: clusterDoc}
	client := testClient(t, fake.handler())

	publishClusterStatus(&fleetReader{client: client}, clusterDoc, fleetDecision{}, rollout{}, declinedFluxTeardown(), "", "", passTime)

	if fake.clusterStatus == nil {
		t.Fatal("the pass wrote no Cluster status, want one because the flux refusal is new")
	}
	c := api.FindCondition(fake.clusterStatus.Status.Conditions, fluxTeardownCondition)
	if c == nil || c.Reason != "NotPlantedByLiken" {
		t.Fatalf("conditions are %+v, want %s with reason NotPlantedByLiken", fake.clusterStatus.Status.Conditions, fluxTeardownCondition)
	}
	if !strings.Contains(c.Message, "kubectl annotate namespace flux-system") {
		t.Errorf("the message is %q, want it to give the kubectl annotate command", c.Message)
	}
}

// The FluxTeardown condition is removed when tearDownFlux no longer
// has a refusal to report.
func TestPublishClusterStatusDropsTheFluxRefusalWhenItEnds(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	clusterDoc.Status.Conditions = api.SetCondition(nil, *declinedFluxTeardown(), passTime.Add(-time.Hour))
	fake := &fleetAPI{clusterDoc: clusterDoc}
	client := testClient(t, fake.handler())

	publishClusterStatus(&fleetReader{client: client}, clusterDoc, fleetDecision{}, rollout{}, nil, "", "", passTime)

	if fake.clusterStatus == nil {
		t.Fatal("the pass wrote no Cluster status, want one that drops the ended flux refusal")
	}
	if c := api.FindCondition(fake.clusterStatus.Status.Conditions, fluxTeardownCondition); c != nil {
		t.Errorf("the status still has %+v, want no flux refusal condition", c)
	}
}

// refusing wraps a handler so that a request with the given method,
// whose path contains fragment, gets the status, and every other
// request goes to inner.
func refusing(inner http.Handler, method, fragment string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == method && strings.Contains(r.URL.Path, fragment) {
			w.WriteHeader(status)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// silentFleet is a fleet of node-1 and node-2 where node-2's
// heartbeat lease is stale, so the pass has a Lost phase to write
// and a Cluster status to publish.
func silentFleet(clusterDoc *cluster.Cluster) *fleetAPI {
	return &fleetAPI{
		clusterDoc: clusterDoc,
		machines: []machine.Machine{
			labMachine("node-1", api.PhaseReady),
			labMachine("node-2", api.PhaseReady),
		},
		renewals: map[string]time.Time{
			"node-1": passTime.Add(-10 * time.Second),
			"node-2": passTime.Add(-5 * time.Minute),
		},
	}
}

func TestReconcileFleetStopsWhenTheMachineListFails(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := silentFleet(clusterDoc)
	client := testClient(t, refusing(fake.handler(), http.MethodGet, "/machines", http.StatusInternalServerError))

	cm, _ := fleetMetrics(t)
	reconcileFleet(&fleetReader{client: client}, clusterDoc, "", &fluxProbe{}, &overduePodReports{}, cm, passTime)

	if fake.clusterStatus != nil {
		t.Errorf("the pass wrote Cluster status %+v, want no write because the Machine list failed", fake.clusterStatus.Status)
	}
	if len(fake.statuses) != 0 {
		t.Errorf("the pass wrote Machine statuses %v, want none because the Machine list failed", fake.statuses)
	}
}

func TestReconcileFleetStopsWhenTheHeartbeatListFails(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := silentFleet(clusterDoc)
	client := testClient(t, refusing(fake.handler(), http.MethodGet, "/leases", http.StatusInternalServerError))

	cm, _ := fleetMetrics(t)
	reconcileFleet(&fleetReader{client: client}, clusterDoc, "", &fluxProbe{}, &overduePodReports{}, cm, passTime)

	if fake.clusterStatus != nil {
		t.Errorf("the pass wrote Cluster status %+v, want no write because the Lease list failed", fake.clusterStatus.Status)
	}
	if len(fake.statuses) != 0 {
		t.Errorf("the pass wrote Machine statuses %v, want none because the Lease list failed", fake.statuses)
	}
}

func TestReconcileFleetContinuesAfterAClusterStatusWriteFailure(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := silentFleet(clusterDoc)
	client := testClient(t, refusing(fake.handler(), http.MethodPut, "/clusters", http.StatusInternalServerError))

	cm, _ := fleetMetrics(t)
	reconcileFleet(fake.sawEachRenewal(&fleetReader{client: client}), clusterDoc, "", &fluxProbe{}, &overduePodReports{}, cm, passTime)

	if lost := fake.statuses["node-2"]; lost == nil || lost.Status.Phase != api.PhaseLost {
		t.Errorf("node-2 status is %+v, want phase Lost even though the Cluster write failed", lost)
	}
}

func TestMarkLostContinuesWhenTheMachineWritesFirst(t *testing.T) {
	// A conflict on the Lost write means the machine's operator wrote
	// its own status first, so the pass skips that machine and marks
	// the next one.
	fake := &fleetAPI{}
	client := testClient(t, refusing(fake.handler(), http.MethodPut, "/machines/node-1", http.StatusConflict))
	machines := []machine.Machine{
		labMachine("node-1", api.PhaseReady),
		labMachine("node-2", api.PhaseReady),
	}

	markLost(&fleetReader{client: client}, machines, []string{"node-1", "node-2"}, nil, passTime)

	if _, wrote := fake.statuses["node-1"]; wrote {
		t.Error("node-1 status was written, want no write because its Lost write conflicted")
	}
	if fake.statuses["node-2"] == nil {
		t.Error("node-2 status was not written, want Lost after node-1's write conflicted")
	}
}

func TestMarkLostContinuesAfterAFailedWrite(t *testing.T) {
	fake := &fleetAPI{}
	client := testClient(t, refusing(fake.handler(), http.MethodPut, "/machines/node-1", http.StatusInternalServerError))
	machines := []machine.Machine{
		labMachine("node-1", api.PhaseReady),
		labMachine("node-2", api.PhaseReady),
	}

	markLost(&fleetReader{client: client}, machines, []string{"node-1", "node-2"}, nil, passTime)

	if _, wrote := fake.statuses["node-1"]; wrote {
		t.Error("node-1 status was written, want no write because the API server refused it")
	}
	if fake.statuses["node-2"] == nil {
		t.Error("node-2 status was not written, want Lost after node-1's write was refused")
	}
}

func TestCarryOutRolloutGrantsAndRevokes(t *testing.T) {
	fake := &fleetAPI{}
	client := testClient(t, fake.handler())

	granted := labMachine("node-4", api.PhaseReady)
	granted.Status.Conditions = api.SetCondition(nil, api.Condition{
		Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows",
	}, passTime.Add(-time.Minute))
	machines := []machine.Machine{labMachine("node-3", api.PhaseUpdatePending), granted}

	carryOutRollout(&fleetReader{client: client}, machines, rollout{grant: []string{"node-3"}, revoke: []string{"node-4"}}, []cluster.RebootTurn{{Machine: "node-3"}}, passTime)

	grant := fake.statuses["node-3"]
	if grant == nil || api.FindCondition(grant.Status.Conditions, machine.RebootApprovedCondition) == nil {
		t.Errorf("node-3 status is %+v, want a RebootApproved condition", grant)
	}
	revoked := fake.statuses["node-4"]
	if revoked == nil || api.FindCondition(revoked.Status.Conditions, machine.RebootApprovedCondition) != nil {
		t.Errorf("node-4 status is %+v, want no RebootApproved condition", revoked)
	}
}

func TestReconcileReadsTheClusterOnEachPass(t *testing.T) {
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := &fleetAPI{
		clusterDoc: clusterDoc,
		machines:   []machine.Machine{labMachine("node-1", api.PhaseReady)},
		renewals:   map[string]time.Time{"node-1": passTime.Add(-10 * time.Second)},
	}
	client := testClient(t, fake.handler())
	cm, _ := fleetMetrics(t)
	reconcile(&fleetReader{client: client}, "lab", newChannelPoller(), &fluxProbe{}, &overduePodReports{}, cm)
	if fake.clusterStatus == nil {
		t.Error("the pass wrote no Cluster status, want one because the fleet changed")
	}
}

func TestReconcileSkipsThePassWhenTheClusterReadFails(t *testing.T) {
	// The rollout needs the Cluster's spec, so a pass that cannot read
	// the Cluster writes nothing.
	clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	fake := silentFleet(clusterDoc)
	client := testClient(t, refusing(fake.handler(), http.MethodGet, "/clusters", http.StatusInternalServerError))

	cm, _ := fleetMetrics(t)
	reconcile(&fleetReader{client: client}, "lab", newChannelPoller(), &fluxProbe{}, &overduePodReports{}, cm)

	if fake.clusterStatus != nil {
		t.Errorf("the pass wrote Cluster status %+v, want no write because the Cluster read failed", fake.clusterStatus.Status)
	}
	if len(fake.statuses) != 0 {
		t.Errorf("the pass wrote Machine statuses %v, want none because the Cluster read failed", fake.statuses)
	}
}

func TestAwaitClusterRetriesAfterAFailingList(t *testing.T) {
	// awaitCluster prints a 500, unlike the 404 of a CRD that is not
	// served yet, and retries both. The wait ends only when a Cluster
	// exists.
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
	clusterDoc := awaitCluster(&fleetReader{client: client}, oneWake(), t.Context())
	if clusterDoc == nil || clusterDoc.Metadata.Name != "lab" || calls != 2 {
		t.Errorf("awaitCluster = %+v after %d list calls, want Cluster lab after 2", clusterDoc, calls)
	}
}

func TestAwaitClusterWaitsForTheFirstCluster(t *testing.T) {
	// While the CRD is not served the list returns 404. Once the
	// machine operators create the Cluster, the next list finds it.
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
	clusterDoc := awaitCluster(&fleetReader{client: client}, oneWake(), t.Context())
	if clusterDoc == nil || clusterDoc.Metadata.Name != "lab" || calls != 2 {
		t.Errorf("awaitCluster = %+v after %d list calls, want Cluster lab after 2", clusterDoc, calls)
	}
}

// oneWake returns a channel with one wake already queued, like the
// wake sent when the Cluster watch first sees a Cluster.
func oneWake() <-chan struct{} {
	wakes := make(chan struct{}, 1)
	wakes <- struct{}{}
	return wakes
}

func TestAwaitClusterEndsAtAShutdown(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	stop, cancel := context.WithCancel(t.Context())
	cancel()
	if got := awaitCluster(&fleetReader{client: client}, make(chan struct{}), stop); got != nil {
		t.Errorf("awaitCluster = %+v after a shutdown, want nil", got)
	}
}
