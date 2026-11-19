package main

// These tests run whole reconcile passes against a fake API server
// that serves both GET requests and the watches' lists. They count the
// requests a pass sends, and check that a pass that reads the watches'
// copies publishes the same status as a pass that reads the API server.
// Each test that runs the watches runs in a synctest bubble, so
// synctest.Wait returns once every watch has received what the fake
// sent.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
	"k8s.io/client-go/dynamic"
)

// newPassAPI returns a fake API server with the objects that a pass on
// node-1 reads.
func newPassAPI() *fakeapi.Server {
	return newPassAPIWithNode(readyNode())
}

// readyNode is node-1's Node, Ready and schedulable.
func readyNode() map[string]any {
	return fakeapi.Object("v1", "Node", "", "node-1", map[string]any{
		"status": map[string]any{"conditions": []any{map[string]any{
			"type": "Ready", "status": "True", "lastTransitionTime": "2026-07-06T11:00:00Z",
		}}},
	})
}

func newPassAPIWithNode(node map[string]any) *fakeapi.Server {
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
		"/api/v1/nodes": {APIVersion: "v1", Kind: "Node", Items: []map[string]any{node}},
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
	protecting(t, protection{})
	factsTree = machine.FactsTree{Dir: t.TempDir()}
	sysctlRoot, draSysfsRoot, cdiDir = t.TempDir(), t.TempDir(), t.TempDir()
	hostsPath = filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, nil, 0o644); err != nil {
		t.Fatalf("writing the hosts file: %v", err)
	}
}

// passClients builds the operator's client and the watches' dynamic
// client, both pointed at the fake.
func passClients(t *testing.T, api http.Handler) (*apiclient.Client, dynamic.Interface) {
	t.Helper()
	server := apiservertest.Start(t, api)
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	watcher, err := dynamic.NewForConfig(server.Config())
	if err != nil {
		t.Fatalf("creating the watch client: %v", err)
	}
	return apiclient.New(apiservertest.Host, server.Client(), credentials), watcher
}

// awaitCopies waits for the watches, then fails the test unless every
// watch of the reader has completed its first list.
func awaitCopies(t *testing.T, r *reader) {
	t.Helper()
	synctest.Wait()
	if !(r.machines.Synced() && r.nodes.Synced() && r.clusters.Synced() &&
		r.credentials.Synced() && r.ownPods.Synced() && r.slices.Synced()) {
		t.Fatal("not every watch finished its first list, want all of them synced")
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
	for pass := range 2 {
		if pass == 1 {
			if r.machines != nil {
				awaitEcho(t, api, r)
			}
			api.Forget()
		}
		current, err := r.machine("node-1")
		if err != nil {
			t.Fatalf("reading the Machine node-1: %v", err)
		}
		if err := reconcile(r, current, "lab", &fetcher{}, mm, nil); err != nil {
			t.Fatalf("reconciling node-1: %v", err)
		}
	}
	requests := api.Requests()
	m, err := kubernetes.GetMachine(r.client, "node-1")
	if err != nil {
		t.Fatalf("reading the Machine node-1 from the API server: %v", err)
	}
	// The two runs happen a few milliseconds apart, so only the
	// transition times may differ between them.
	for i := range m.Status.Conditions {
		m.Status.Conditions[i].LastTransitionTime = time.Time{}
	}
	return requests, m
}

// awaitEcho waits for the watches, then fails the test unless the
// reader's copy of the Machine has the resourceVersion of the first
// pass's status write.
func awaitEcho(t *testing.T, api *fakeapi.Server, r *reader) {
	t.Helper()
	synctest.Wait()
	written := api.ResourceVersion(kubernetes.MachinesPath, "node-1")
	if m, _, ok := watch.Get[machine.Machine](r.machines.View(), "node-1"); !ok || m.Metadata.ResourceVersion != written {
		t.Fatal("the copy of the Machine does not have the resourceVersion of the first pass's status write, want it to")
	}
}

// A settled pass without watches sends a GET for each object it reads,
// and for the Machine that the loop reads before the pass. A settled
// pass that reads the watches' copies sends no request. Both publish
// the same status, so the copies decode each object the same way a GET
// does.
func TestASettledPassOverTheCopiesSendsNoReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		awaitCopies(t, r)
		watchedRequests, watchedMachine := runPasses(t, watched, r)

		wantDirect := []string{
			"GET /apis/liken.sh/v1alpha1/machines/node-1",
			"GET /api/v1/namespaces/liken-system/pods",
			"GET /apis/liken.sh/v1alpha1/clusters/lab",
			"GET /api/v1/nodes/node-1",
			"GET /apis/resource.k8s.io/v1/resourceslices/node-1-liken.sh",
			"GET /apis/liken.sh/v1alpha1/disruptions",
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
	})
}

// Until the watch receives this operator's own status write, a read of
// the Machine goes to the API server and not to the older copy. After
// the watch receives the write, the read uses the copy again.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		fake := newPassAPI()
		client, watcher := passClients(t, fake)
		wakes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchThisMachine(ctx, watcher, client, "node-1", "lab", watch.Signal(wakes), func(string) {})
		awaitCopies(t, r)
		current, err := r.machine("node-1")
		if err != nil {
			t.Fatalf("reading the Machine node-1: %v", err)
		}
		status := current.Status
		status.Phase = "Ready"
		fake.Hold()
		if err := r.publishStatus(current, &status); err != nil {
			t.Fatalf("publishing the status of node-1: %v", err)
		}
		fake.Forget()

		held, err := r.machine("node-1")
		if err != nil || held.Status.Phase != "Ready" {
			t.Errorf("the read after the write = %+v, %v; want the written status", held, err)
		}
		if sent := fake.Requests(); !slices.Equal(sent, []string{"GET " + kubernetes.MachinesPath + "/node-1"}) {
			t.Errorf("the read after the write sent %q, want one read of the Machine", sent)
		}
		fake.Release()
		awaitEcho(t, fake, r)
		fake.Forget()
		if _, err := r.machine("node-1"); err != nil {
			t.Fatalf("reading the Machine node-1 after the watch delivered the write: %v", err)
		}
		if sent := fake.Requests(); len(sent) != 0 {
			t.Errorf("the read after the watch delivered the write sent %q, want nothing", sent)
		}
	})
}

// failingWatches serves api, and once fail is set, it fails every
// watch with a 503, as an API server does while it restarts.
type failingWatches struct {
	api  *fakeapi.Server
	fail atomic.Bool
}

func (f *failingWatches) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.fail.Load() && r.URL.Query().Get("watch") == "true" {
		http.Error(w, "the API server is restarting", http.StatusServiceUnavailable)
		return
	}
	f.api.ServeHTTP(w, r)
}

// breakWatches makes every later watch fail and closes the open
// streams, as a restart of the server does, so each reflector opens its
// watch again and receives the 503.
func breakWatches(server *apiservertest.Server, watches *failingWatches) {
	watches.fail.Store(true)
	server.SetDown(true)
	server.SetDown(false)
}

// After a watch fails, every copy reports that it is not ready, and a
// pass reads the API server. A copy can miss the writes made while its
// reflector backs off, and a pass that read it could stage, and reboot
// into, a rollout that was withdrawn.
func TestAPassReadsTheAPIServerAfterAWatchFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		fake := newPassAPI()
		watches := &failingWatches{api: fake}
		server := apiservertest.Start(t, watches)
		client, _ := passClients(t, fake)
		watcher, err := dynamic.NewForConfig(server.Config())
		if err != nil {
			t.Fatalf("creating the watch client: %v", err)
		}
		wakes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchThisMachine(ctx, watcher, client, "node-1", "lab", watch.Signal(wakes), func(string) {})
		awaitCopies(t, r)

		breakWatches(server, watches)

		// Each reflector opens its watch again after its backoff, which
		// starts under two seconds, and the fake refuses it.
		time.Sleep(10 * time.Second)
		synctest.Wait()
		copies := []*informer.Collection{r.machines, r.nodes, r.clusters, r.credentials, r.ownPods, r.slices}
		if slices.ContainsFunc(copies, func(c *informer.Collection) bool { return c.View().Ready() }) {
			t.Fatal("a copy still served reads ten seconds after its watch failed, want none")
		}
		fake.Forget()
		if _, err := r.cluster("lab"); err != nil {
			t.Fatalf("reading the Cluster lab: %v", err)
		}
		if _, err := r.node("node-1"); err != nil {
			t.Fatalf("reading the Node node-1: %v", err)
		}
		want := []string{"GET " + kubernetes.ClustersPath + "/lab", "GET /api/v1/nodes/node-1"}
		if sent := fake.Requests(); !slices.Equal(sent, want) {
			t.Errorf("the reads sent %q, want %q", sent, want)
		}
	})
}
