package main

// Fixtures for the tests of status.rebootTurns: a fake API server that
// checks resourceVersions as the real one does, and fleet readers that
// pass it as the leader does.

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
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/machine"
)

// leaseRenewed is the renewTime of every machine's heartbeat Lease in
// raceFleet. Each reader saw it ten seconds before the pass, so every
// machine reads as available.
var leaseRenewed = passTime.Add(-10 * time.Second)

// fleetMachine returns a Ready Machine as the fake stores it. When
// awaiting is true, the Machine is waiting for a reboot turn.
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

// awaitingTurn adds the condition that a machine operator writes when
// its machine has staged a change and waits for a reboot turn.
func awaitingTurn(conditions []api.Condition) []api.Condition {
	return api.SetCondition(conditions, api.Condition{
		Type: "SpecConverged", Status: api.ConditionFalse, Reason: "AwaitingTurn",
	}, passTime.Add(-time.Minute))
}

func asObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding the value as JSON: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decoding the JSON into an object: %v", err)
	}
	return object
}

// raceFleet returns a fake API server that checks resourceVersions. It
// has three leaders, a disruption budget of one, and node-2 waiting for
// a reboot turn.
func raceFleet(t *testing.T) *fakeapi.Server {
	t.Helper()
	return raceFleetWith(t, nil)
}

// raceFleetWith is raceFleet with additional collections, such as
// budgets.
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
		// The pass lists DaemonSets and pods too. Both are empty, so
		// it evicts no pod.
		daemonSetsPath:                         {APIVersion: "apps/v1", Kind: "DaemonSet"},
		"/api/v1/namespaces/liken-system/pods": {APIVersion: "v1", Kind: "Pod"},
		// node-2's Disruption, which its machine operator creates for
		// the change it staged.
		kubernetes.DisruptionsPath: {APIVersion: api.APIVersion, Kind: "Disruption",
			Items: []map[string]any{requestObject(t, approvedRequest("node-2"))}},
	}
	maps.Copy(collections, more)
	fake := fakeapi.New(collections)
	fake.CheckVersions()
	return fake
}

// leadingReader returns a fleet reader that has no watches, so it reads
// from the API server, and that has empty memos, like a process that
// has just become leader. It saw each heartbeat Lease at leaseRenewed
// and first read the Leases an hour ago, so the rollout may grant turns.
func leadingReader(t *testing.T, handler http.Handler) *fleetReader {
	t.Helper()
	r := &fleetReader{client: testClient(t, handler), machineVersions: memo.New(), clusterVersions: memo.New()}
	r.sightings.firstRead = passTime.Add(-time.Hour)
	for _, name := range []string{"node-1", "node-2", "node-3"} {
		r.sightings.observe(name, leaseRenewed, leaseRenewed)
	}
	return r
}

// reconcileAt runs one pass of the reader at passTime, using a fresh read
// of the Cluster.
func reconcileAt(t *testing.T, r *fleetReader) {
	t.Helper()
	clusterDoc, err := r.cluster("lab")
	if err != nil {
		t.Errorf("reading the Cluster: %v", err)
		return
	}
	cm, _ := fleetMetrics(t)
	if err := reconcileFleet(r, clusterDoc, "", &fluxProbe{}, &overduePodReports{}, cm, passTime); err != nil {
		t.Errorf("reconciling the fleet: %v", err)
	}
}

// askForATurn writes the named machine's status, and creates its
// Disruption, as its machine operator does when the machine stages a
// change that needs a reboot.
func askForATurn(t *testing.T, client *apiclient.Client, name string) {
	t.Helper()
	m, err := kubernetes.GetMachine(client, name)
	if err != nil {
		t.Fatalf("reading the Machine %s: %v", name, err)
	}
	status := m.Status
	status.Conditions = awaitingTurn(slices.Clone(m.Status.Conditions))
	if _, err := kubernetes.PublishStatus(client, m, &status); err != nil {
		t.Fatalf("writing the status of the Machine %s: %v", name, err)
	}
	body, err := json.Marshal(requestObject(t, approvedRequest(name)))
	if err != nil {
		t.Fatalf("encoding the Disruption of %s: %v", name, err)
	}
	if err := client.RequestJSON(http.MethodPost, kubernetes.DisruptionsPath, body, nil); err != nil {
		t.Fatalf("creating the Disruption of %s: %v", name, err)
	}
}

// requestObject returns a Disruption as the fake stores it.
func requestObject(t *testing.T, d disruption.Disruption) map[string]any {
	t.Helper()
	d.Metadata.ResourceVersion = "5"
	return asObject(t, d)
}

// grantHolders returns the names of the machines that have a
// RebootApproved condition in the fake.
func grantHolders(t *testing.T, client *apiclient.Client) []string {
	t.Helper()
	machines, err := kubernetes.List[machine.Machine](client, kubernetes.MachinesPath)
	if err != nil {
		t.Fatalf("listing the Machines: %v", err)
	}
	var names []string
	for _, m := range machines {
		if api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition) != nil {
			names = append(names, m.Metadata.Name)
		}
	}
	return names
}
