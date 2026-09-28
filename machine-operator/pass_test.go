package main

// These tests run whole reconcile passes against a fake API server
// that answers both plain reads and the watches' streaming lists. They
// count the requests a pass sends, and check that a pass over the
// watches' copies reaches the same verdict as a pass that reads the API
// server.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/machine"
	"github.com/liken-sh/liken/metrics"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// newPassAPI holds the objects one machine's pass reads.
func newPassAPI() *fakeapi.Server {
	object := fakeapi.Object
	return fakeapi.New(map[string]*fakeapi.Collection{
		kubernetes.MachinesPath: {APIVersion: "liken.sh/v1alpha1", Kind: "Machine", Items: []map[string]any{
			object("liken.sh/v1alpha1", "Machine", "", "node-1", nil),
		}},
		kubernetes.ClustersPath: {APIVersion: "liken.sh/v1alpha1", Kind: "Cluster", Items: []map[string]any{
			object("liken.sh/v1alpha1", "Cluster", "", "lab", map[string]any{
				"spec": map[string]any{"leaders": []any{"node-1"}},
			}),
		}},
		"/api/v1/nodes": {APIVersion: "v1", Kind: "Node", Items: []map[string]any{
			object("v1", "Node", "", "node-1", map[string]any{
				"status": map[string]any{"conditions": []any{map[string]any{
					"type": "Ready", "status": "True", "lastTransitionTime": "2026-07-06T11:00:00Z",
				}}},
			}),
		}},
		"/api/v1/namespaces/liken-system/secrets": {APIVersion: "v1", Kind: "Secret", Items: []map[string]any{
			object("v1", "Secret", "liken-system", "registry-credentials", map[string]any{
				"type": "kubernetes.io/dockerconfigjson",
				"data": map[string]any{".dockerconfigjson": "eyJhdXRocyI6e319"},
			}),
		}},
		"/api/v1/namespaces/liken-system/pods": {APIVersion: "v1", Kind: "Pod", Items: []map[string]any{
			object("v1", "Pod", "liken-system", "liken-machine-operator-x", map[string]any{
				"spec": map[string]any{"nodeName": "node-1"},
			}),
		}},
		kubernetes.ResourceSlicesPath:                                 {APIVersion: "resource.k8s.io/v1", Kind: "ResourceSlice"},
		"/apis/coordination.k8s.io/v1/namespaces/liken-system/leases": {APIVersion: "coordination.k8s.io/v1", Kind: "Lease"},
	})
}

// isolatePass points every host path a pass touches at a tempdir, so a
// test never writes the host's kernel parameters, hosts file, or CDI
// specs.
func isolatePass(t *testing.T) {
	t.Helper()
	saved := []any{factsTree, sysctlRoot, hostsPath, draSysfsRoot, cdiDir}
	t.Cleanup(func() {
		factsTree = saved[0].(machine.FactsTree)
		sysctlRoot, hostsPath, draSysfsRoot, cdiDir = saved[1].(string), saved[2].(string), saved[3].(string), saved[4].(string)
	})
	factsTree = machine.FactsTree{Dir: t.TempDir()}
	sysctlRoot, draSysfsRoot, cdiDir = t.TempDir(), t.TempDir(), t.TempDir()
	hostsPath = filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// passClients builds the operator's client and the watches' dynamic
// client, both pointed at the fake.
func passClients(t *testing.T, api *fakeapi.Server) (*kubernetes.Client, dynamic.Interface) {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	return kubernetes.NewClient(server.URL, server.Client(), credentials), watcher
}

// awaitCopies waits until every watch of the reader holds its first
// read.
func awaitCopies(t *testing.T, r *reader, wakes <-chan struct{}) {
	t.Helper()
	for !(r.machines.Synced() && r.nodes.Synced() && r.clusters.Synced() &&
		r.credentials.Synced() && r.ownPods.Synced() && r.slices.Synced()) {
		select {
		case <-wakes:
		case <-time.After(10 * time.Second):
			t.Fatal("the watches never synced")
		}
	}
}

// runPasses runs two passes, the way the loop does after a start, and
// returns the requests of the second pass and the status it left. The
// first pass creates the heartbeat lease and writes the first status,
// so the second pass is the settled one.
func runPasses(t *testing.T, api *fakeapi.Server, r *reader) ([]string, *machine.Machine) {
	t.Helper()
	o := metrics.NewOperator(component, machine.Version, []string{machineKind}, watchKinds)
	mm := newMachineMetrics(o, &fetcher{})
	heartbeat := kubernetes.NewHeartbeat("node-1")
	for pass := range 2 {
		if pass == 1 {
			if r.machines != nil {
				awaitEcho(t, api, r)
			}
			api.Forget()
		}
		current, err := r.machine("node-1")
		if err != nil {
			t.Fatal(err)
		}
		if err := reconcile(r, current, "lab", &fetcher{}, heartbeat, mm); err != nil {
			t.Fatal(err)
		}
	}
	requests := api.Requests()
	m, err := kubernetes.GetMachine(r.client, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	// The two runs happen a few milliseconds apart, so only the
	// transition times may differ between them.
	for i := range m.Status.Conditions {
		m.Status.Conditions[i].LastTransitionTime = time.Time{}
	}
	return requests, m
}

// awaitEcho waits until the reader's copy of the Machine holds the
// first pass's status write, the way the loop's next pass would find
// it after the watch delivered the write.
func awaitEcho(t *testing.T, api *fakeapi.Server, r *reader) {
	t.Helper()
	written := api.ResourceVersion(kubernetes.MachinesPath, "node-1")
	deadline := time.Now().Add(5 * time.Second)
	for {
		m, _, ok := informer.Get[machine.Machine](r.machines, "node-1")
		if ok && m.Metadata.ResourceVersion == written {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the copy never held the first pass's write")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A settled pass that reads the API server sends one read for each
// object it judges, and the loop read the Machine too. A settled pass
// over the watches' copies sends none of them. Both passes reach the
// same status, so the copies decode each object the way a direct read
// does.
func TestASettledPassOverTheCopiesSendsNoReads(t *testing.T) {
	isolatePass(t)

	direct := newPassAPI()
	client, _ := passClients(t, direct)
	directRequests, directMachine := runPasses(t, direct, &reader{client: client})

	watched := newPassAPI()
	client, watcher := passClients(t, watched)
	wakes := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := watchThisMachine(ctx, watcher, client, "node-1", "lab", func() {
		select {
		case wakes <- struct{}{}:
		default:
		}
	}, func(string) {})
	awaitCopies(t, r, wakes)
	watchedRequests, watchedMachine := runPasses(t, watched, r)

	wantDirect := []string{
		"GET /apis/liken.sh/v1alpha1/machines/node-1",
		"GET /api/v1/namespaces/liken-system/pods",
		"GET /api/v1/nodes/node-1",
		"GET /apis/resource.k8s.io/v1/resourceslices/node-1-liken.sh",
		"GET /apis/liken.sh/v1alpha1/clusters/lab",
		"GET /api/v1/namespaces/liken-system/secrets/registry-credentials",
	}
	if !slices.Equal(directRequests, wantDirect) {
		t.Errorf("a pass that reads the API server sent %q, want %q", directRequests, wantDirect)
	}
	if len(watchedRequests) != 0 {
		t.Errorf("a pass over the copies sent %q, want nothing", watchedRequests)
	}
	d, _ := json.Marshal(directMachine.Status)
	w, _ := json.Marshal(watchedMachine.Status)
	if string(d) != string(w) {
		t.Errorf("the pass over the copies published\n%s\nwant the direct pass's\n%s", w, d)
	}
}

// The pass writes the heartbeat lease with this machine's Machine as
// its owner, so deleting the Machine deletes the lease.
func TestAPassNamesTheMachineAsItsLeaseOwner(t *testing.T) {
	isolatePass(t)
	api := newPassAPI()
	client, _ := passClients(t, api)
	runPasses(t, api, &reader{client: client})

	lease := &kubernetes.Lease{}
	if err := client.RequestJSON(http.MethodGet, "/apis/coordination.k8s.io/v1/namespaces/liken-system/leases/node-1", nil, lease); err != nil {
		t.Fatal(err)
	}

	want := []kubernetes.OwnerReference{{APIVersion: "liken.sh/v1alpha1", Kind: "Machine", Name: "node-1", UID: "uid-node-1"}}
	if !slices.Equal(lease.Metadata.OwnerReferences, want) {
		t.Errorf("the lease's owners are %+v, want %+v", lease.Metadata.OwnerReferences, want)
	}
}
