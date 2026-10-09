package main

// These tests run whole reconcile passes against a fake API server
// that answers both plain reads and the watches' streaming lists. They
// count the requests a pass sends, and check that a pass over the
// watches' copies reaches the same verdict as a pass that reads the API
// server. Each test that runs the watches runs in a synctest bubble, so
// synctest.Wait returns once every watch has delivered what the fake
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

// newPassAPI holds the objects one machine's pass reads.
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

// newPassAPIWithNode holds the objects one machine's pass reads, with
// node as its Node.
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
		t.Fatal(err)
	}
}

// passClients builds the operator's client and the watches' dynamic
// client, both pointed at the fake.
func passClients(t *testing.T, api http.Handler) (*apiclient.Client, dynamic.Interface) {
	t.Helper()
	server := apiservertest.Start(t, api)
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher, err := dynamic.NewForConfig(server.Config())
	if err != nil {
		t.Fatal(err)
	}
	return apiclient.New(apiservertest.Host, server.Client(), credentials), watcher
}

// awaitCopies waits until the watches have done all they can, and
// checks that every watch of the reader holds its first read.
func awaitCopies(t *testing.T, r *reader) {
	t.Helper()
	synctest.Wait()
	if !(r.machines.Synced() && r.nodes.Synced() && r.clusters.Synced() &&
		r.credentials.Synced() && r.ownPods.Synced() && r.slices.Synced()) {
		t.Fatal("the watches never synced")
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
			t.Fatal(err)
		}
		if err := reconcile(r, current, "lab", &fetcher{}, mm, nil); err != nil {
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

// awaitEcho waits until the watches have done all they can, and checks
// that the reader's copy of the Machine holds the first pass's status
// write, the way the loop's next pass would find it after the watch
// delivered the write.
func awaitEcho(t *testing.T, api *fakeapi.Server, r *reader) {
	t.Helper()
	synctest.Wait()
	written := api.ResourceVersion(kubernetes.MachinesPath, "node-1")
	if m, _, ok := watch.Get[machine.Machine](r.machines.View(), "node-1"); !ok || m.Metadata.ResourceVersion != written {
		t.Fatal("the copy never held the first pass's write")
	}
}

// A settled pass that reads the API server sends one read for each
// object it judges, and the loop read the Machine too. A settled pass
// over the watches' copies sends none of them. Both passes reach the
// same status, so the copies decode each object the way a direct read
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
	})
}

// A pass that runs before the watch delivers this operator's own status
// write does not start from the older copy in the Machine's store. It
// reads the Machine from the API server once, and after the watch
// delivers the write, the store answers again with no request.
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
			t.Fatal(err)
		}
		status := current.Status
		status.Phase = "Ready"
		fake.Hold()
		if err := r.publishStatus(current, &status); err != nil {
			t.Fatal(err)
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
			t.Fatal(err)
		}
		if sent := fake.Requests(); len(sent) != 0 {
			t.Errorf("the read after the watch delivered the write sent %q, want nothing", sent)
		}
	})
}

// failingWatches serves a fake API server whose watches fail with a 503
// once fail is set, the way an API server answers while it restarts.
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

// breakWatches makes every later watch fail and cuts the streams that
// are open, the way a restart of the server does, so each reflector
// opens a watch again and meets the 503.
func breakWatches(server *apiservertest.Server, watches *failingWatches) {
	watches.fail.Store(true)
	server.SetDown(true)
	server.SetDown(false)
}

// After a watch fails for any reason, such as an API server restart,
// every copy stops answering, and a pass reads the API server. A copy
// can miss the writes made while its reflector waits out a backoff, and
// a pass that staged a withdrawn rollout from it would reboot into it.
func TestAPassReadsTheAPIServerAfterAWatchFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		fake := newPassAPI()
		watches := &failingWatches{api: fake}
		server := apiservertest.Start(t, watches)
		client, _ := passClients(t, fake)
		watcher, err := dynamic.NewForConfig(server.Config())
		if err != nil {
			t.Fatal(err)
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
			t.Fatal("a copy kept answering ten seconds after its watch failed")
		}
		fake.Forget()
		if _, err := r.cluster("lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.node("node-1"); err != nil {
			t.Fatal(err)
		}
		want := []string{"GET " + kubernetes.ClustersPath + "/lab", "GET /api/v1/nodes/node-1"}
		if sent := fake.Requests(); !slices.Equal(sent, want) {
			t.Errorf("the reads sent %q, want %q", sent, want)
		}
	})
}
