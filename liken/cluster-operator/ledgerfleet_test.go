package main

// This file builds the fleet that the tests of the grant ledger share:
// a fake API server that checks versions as the API server does, and
// fleet readers that sweep it as copies of this program that hold the
// Lease (pausedleader_test.go, ledgersweep_test.go).

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/machine"
)

// leaseRenewed is the renewTime of every machine's heartbeat Lease in
// the race's fleet. Each reader saw it renewed ten seconds before the
// sweep, so every machine reads available.
var leaseRenewed = sweepNow.Add(-10 * time.Second)

// fleetMachine is one Machine as the fake stores it: Ready, and asking
// for a reboot turn when awaiting is true.
func fleetMachine(t *testing.T, name string, awaiting bool) map[string]any {
	t.Helper()
	m := machine.Machine{APIVersion: api.APIVersion, Kind: "Machine",
		Metadata: api.ObjectMeta{Name: name, ResourceVersion: "5", UID: "uid-" + name}}
	m.Status.Phase = api.PhaseReady
	if awaiting {
		m.Status.Conditions = awaitingTurn(nil)
	}
	return asObject(t, m)
}

// awaitingTurn adds the condition a machine operator writes when its
// machine has staged a change and waits for a reboot turn.
func awaitingTurn(conditions []api.Condition) []api.Condition {
	return api.SetCondition(conditions, api.Condition{
		Type: "SpecConverged", Status: api.ConditionFalse, Reason: "AwaitingTurn",
	}, sweepNow.Add(-time.Minute))
}

func asObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

// raceFleet is a fake API server that checks versions, with three
// leaders, a disruption budget of one, and node-2 waiting for a turn.
func raceFleet(t *testing.T) *fakeapi.Server {
	t.Helper()
	return raceFleetWith(t, nil)
}

// raceFleetWith is raceFleet with more collections, such as the
// Disruptions and the budgets.
func raceFleetWith(t *testing.T, more map[string]*fakeapi.Collection) *fakeapi.Server {
	t.Helper()
	clusterDoc := cluster.Cluster{APIVersion: api.APIVersion, Kind: "Cluster",
		Metadata: api.ObjectMeta{Name: "lab", ResourceVersion: "5", UID: "uid-lab"}}
	clusterDoc.Spec.Leaders = []string{"node-1", "node-2", "node-3"}
	clusterDoc.Spec.Disruption.MaxUnavailable = 1
	renewed := leaseRenewed.Format("2006-01-02T15:04:05.000000Z07:00")
	lease := func(name string) map[string]any {
		return fakeapi.Object("coordination.k8s.io/v1", "Lease", "liken-system", name,
			map[string]any{"spec": map[string]any{"renewTime": renewed}})
	}
	collections := map[string]*fakeapi.Collection{
		kubernetes.MachinesPath: {APIVersion: api.APIVersion, Kind: "Machine", Items: []map[string]any{
			fleetMachine(t, "node-1", false), fleetMachine(t, "node-2", true), fleetMachine(t, "node-3", false),
		}},
		kubernetes.ClustersPath: {APIVersion: api.APIVersion, Kind: "Cluster", Items: []map[string]any{asObject(t, clusterDoc)}},
		"/apis/coordination.k8s.io/v1/namespaces/liken-system/leases": {APIVersion: "coordination.k8s.io/v1", Kind: "Lease",
			Items: []map[string]any{lease("node-1"), lease("node-2"), lease("node-3")}},
		// The copies of a program that runs watch these too, and find
		// nothing to steward.
		daemonSetsPath:                         {APIVersion: "apps/v1", Kind: "DaemonSet"},
		"/api/v1/namespaces/liken-system/pods": {APIVersion: "v1", Kind: "Pod"},
	}
	maps.Copy(collections, more)
	fake := fakeapi.New(collections)
	fake.CheckVersions()
	return fake
}

// leadingReader is a fleet reader with no watches, so it reads the API
// server, and with an empty memo, as a copy that just took the Lease
// has. It saw each heartbeat Lease renewed at leaseRenewed, and began
// reading them long enough ago to judge them.
func leadingReader(t *testing.T, handler http.Handler) *fleetReader {
	t.Helper()
	r := &fleetReader{client: testClient(t, handler), machineVersions: memo.New(), clusterVersions: memo.New()}
	r.sightings.began = sweepNow.Add(-time.Hour)
	for _, name := range []string{"node-1", "node-2", "node-3"} {
		r.sightings.observe(name, leaseRenewed, leaseRenewed)
	}
	return r
}

// sweepAt runs one sweep of a reader at sweepNow, from a fresh read of
// the Cluster.
func sweepAt(t *testing.T, r *fleetReader) {
	t.Helper()
	clusterDoc, err := r.cluster("lab")
	if err != nil {
		t.Errorf("reading the Cluster: %v", err)
		return
	}
	cm, _ := fleetMetrics(t)
	if err := sweepFleet(r, clusterDoc, "", &engineProbe{}, &podSteward{}, cm, sweepNow); err != nil {
		t.Errorf("sweeping: %v", err)
	}
}

// askForATurn writes node-1's status as its machine operator does when
// the machine stages a change that needs a reboot.
func askForATurn(t *testing.T, client *apiclient.Client, name string) {
	t.Helper()
	m, err := kubernetes.GetMachine(client, name)
	if err != nil {
		t.Fatal(err)
	}
	status := m.Status
	status.Conditions = awaitingTurn(slices.Clone(m.Status.Conditions))
	if _, err := kubernetes.PublishStatus(client, m, &status); err != nil {
		t.Fatal(err)
	}
}

// grantHolders answers the machines that hold a reboot grant in the fake.
func grantHolders(t *testing.T, client *apiclient.Client) []string {
	t.Helper()
	machines, err := kubernetes.List[machine.Machine](client, kubernetes.MachinesPath)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range machines {
		if api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition) != nil {
			names = append(names, m.Metadata.Name)
		}
	}
	return names
}
