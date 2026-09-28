package main

// These tests run whole sweeps against a fake API server that answers
// both plain reads and the watches' streaming lists. They count the
// requests a sweep sends, and check that a sweep over the watches'
// copies reaches the same verdict as a sweep that reads the API server.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// newFleetAPI holds a two-machine fleet with fresh heartbeats, the
// Cluster, the two stewarded DaemonSets, and their pods.
func newFleetAPI(now time.Time) *fakeapi.Server {
	object := fakeapi.Object
	renewed := now.UTC().Format("2006-01-02T15:04:05.000000Z07:00")
	osDaemonSet := func(name string) map[string]any {
		ds := object("apps/v1", "DaemonSet", "liken-system", name, nil)
		ds["metadata"].(map[string]any)["annotations"] = map[string]any{osVersionAnnotation: "2026.09.27-001"}
		return ds
	}
	osPod := func(app, name, node string) map[string]any {
		pod := object("v1", "Pod", "liken-system", name, map[string]any{"spec": map[string]any{"nodeName": node}})
		metadata := pod["metadata"].(map[string]any)
		metadata["labels"] = map[string]any{"app": app}
		metadata["annotations"] = map[string]any{osVersionAnnotation: "2026.09.27-001"}
		metadata["ownerReferences"] = []any{map[string]any{"kind": "DaemonSet", "name": app}}
		return pod
	}
	readyMachine := func(name string) map[string]any {
		return object("liken.sh/v1alpha1", "Machine", "", name, map[string]any{
			"status": map[string]any{"phase": "Ready", "version": map[string]any{"liken": "2026.09.27-001"}},
		})
	}
	return fakeapi.New(map[string]*fakeapi.Collection{
		kubernetes.MachinesPath: {APIVersion: "liken.sh/v1alpha1", Kind: "Machine", Items: []map[string]any{
			readyMachine("node-1"), readyMachine("node-2"),
		}},
		kubernetes.ClustersPath: {APIVersion: "liken.sh/v1alpha1", Kind: "Cluster", Items: []map[string]any{
			object("liken.sh/v1alpha1", "Cluster", "", "lab", map[string]any{
				"spec": map[string]any{"leaders": []any{"node-1"}, "version": "2026.09.27-001"},
			}),
		}},
		"/apis/coordination.k8s.io/v1/namespaces/liken-system/leases": {APIVersion: "coordination.k8s.io/v1", Kind: "Lease", Items: []map[string]any{
			object("coordination.k8s.io/v1", "Lease", "liken-system", "node-1", map[string]any{"spec": map[string]any{"renewTime": renewed}}),
			object("coordination.k8s.io/v1", "Lease", "liken-system", "node-2", map[string]any{"spec": map[string]any{"renewTime": renewed}}),
			// This program's own leader election Lease, which proves
			// the copies current (fleetReader.current).
			object("coordination.k8s.io/v1", "Lease", "liken-system", leaseName, map[string]any{"spec": map[string]any{"renewTime": renewed}}),
		}},
		daemonSetsPath: {APIVersion: "apps/v1", Kind: "DaemonSet", Items: []map[string]any{
			osDaemonSet(machineOperatorDaemonSet), osDaemonSet("machine-logs"),
		}},
		"/api/v1/namespaces/liken-system/pods": {APIVersion: "v1", Kind: "Pod", Items: []map[string]any{
			osPod(machineOperatorDaemonSet, "mo-1", "node-1"), osPod("machine-logs", "logs-1", "node-1"),
		}},
	})
}

func fleetClients(t *testing.T, api *fakeapi.Server) (*kubernetes.Client, dynamic.Interface) {
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

// awaitFleetCopies waits until every watch of the reader holds its
// first read.
func awaitFleetCopies(t *testing.T, r *fleetReader, wakes <-chan struct{}) {
	t.Helper()
	for !(r.machineCopy.Synced() && r.clusterCopy.Synced() && r.leaseCopy.Synced() &&
		r.daemonSetCopy.Synced() && r.podCopy.Synced()) {
		select {
		case <-wakes:
		case <-time.After(10 * time.Second):
			t.Fatal("the watches never synced")
		}
	}
}

// awaitClusterEcho waits until the reader's copy of the Cluster holds
// the first sweep's status write.
func awaitClusterEcho(t *testing.T, api *fakeapi.Server, r *fleetReader) {
	t.Helper()
	written := api.ResourceVersion(kubernetes.ClustersPath, "lab")
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, _, ok := informer.Get[cluster.Cluster](r.clusterCopy, "lab")
		if ok && c.Metadata.ResourceVersion == written {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the copy never held the first sweep's write")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runSweeps runs two sweeps and returns the requests of the second one
// and the Cluster it left. The first sweep writes the Cluster's first
// status, so the second is the settled one.
func runSweeps(t *testing.T, api *fakeapi.Server, r *fleetReader) ([]string, *cluster.Cluster) {
	t.Helper()
	cm, _ := fleetMetrics(t)
	for pass := range 2 {
		if pass == 1 {
			if r.clusterCopy != nil {
				awaitClusterEcho(t, api, r)
			}
			api.Forget()
		}
		if err := sweep(r, "lab", newChannelPoller(), &engineProbe{}, cm); err != nil {
			t.Fatal(err)
		}
	}
	requests := api.Requests()
	c, err := kubernetes.GetCluster(r.client, "lab")
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Status.Conditions {
		c.Status.Conditions[i].LastTransitionTime = time.Time{}
	}
	return requests, c
}

// A settled sweep that reads the API server sends a read for every
// collection it judges. A settled sweep over the watches' copies sends
// only the one read that stays direct: the flux-system Namespace, on a
// fleet that does not declare flux. Both sweeps publish the same
// Cluster status.
func TestASettledSweepOverTheCopiesSendsOneRead(t *testing.T) {
	now := time.Now()

	direct := newFleetAPI(now)
	client, _ := fleetClients(t, direct)
	directRequests, directCluster := runSweeps(t, direct, &fleetReader{client: client})

	watched := newFleetAPI(now)
	client, watcher := fleetClients(t, watched)
	wakes := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := watchFleet(ctx, watcher, client, func() {
		select {
		case wakes <- struct{}{}:
		default:
		}
	}, func(string) {})
	awaitFleetCopies(t, r, wakes)
	watchedRequests, watchedCluster := runSweeps(t, watched, r)

	wantDirect := []string{
		"GET /apis/liken.sh/v1alpha1/clusters/lab",
		"GET /apis/liken.sh/v1alpha1/machines",
		"GET /apis/coordination.k8s.io/v1/namespaces/liken-system/leases",
		"GET /apis/apps/v1/namespaces/liken-system/daemonsets/liken-machine-operator",
		"GET /apis/apps/v1/namespaces/liken-system/daemonsets/liken-machine-operator",
		"GET /api/v1/namespaces/liken-system/pods",
		"GET /apis/apps/v1/namespaces/liken-system/daemonsets/machine-logs",
		"GET /api/v1/namespaces/liken-system/pods",
		"GET /apis/apps/v1/namespaces/liken-system/daemonsets",
		"GET /api/v1/namespaces/flux-system",
	}
	if !slices.Equal(directRequests, wantDirect) {
		t.Errorf("a sweep that reads the API server sent %q, want %q", directRequests, wantDirect)
	}
	if want := []string{"GET /api/v1/namespaces/flux-system"}; !slices.Equal(watchedRequests, want) {
		t.Errorf("a sweep over the copies sent %q, want %q", watchedRequests, want)
	}
	d, _ := json.Marshal(directCluster.Status)
	w, _ := json.Marshal(watchedCluster.Status)
	if string(d) != string(w) {
		t.Errorf("the sweep over the copies published\n%s\nwant the direct sweep's\n%s", w, d)
	}
}

// operate finds the Cluster, sweeps, and returns after the sweep in
// flight when stop ends. Here stop ends while the first sweep writes
// the Cluster's status, so exactly one sweep runs, and it finishes its
// write.
func TestOperateFinishesTheSweepInFlightAtAShutdown(t *testing.T) {
	fake := newFleetAPI(time.Now())
	stop, cancel := context.WithCancel(t.Context())
	defer cancel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.ServeHTTP(w, r)
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/clusters/lab/status") {
			cancel()
		}
	})
	client := testClient(t, handler)
	o := metrics.NewOperator(component, machine.Version, []string{clusterKind}, watchKinds)
	cm := newClusterMetrics(o)

	operate(stop, func(ctx context.Context) bool { return ctx.Err() == nil }, &fleetReader{client: client}, make(chan struct{}), make(chan time.Time), o, cm)

	if got := fake.ResourceVersion(kubernetes.ClustersPath, "lab"); got == "5" {
		t.Error("the sweep in flight did not publish the Cluster's status")
	}
	if scrape := scrapeHandler(t, o.Handler()); !strings.Contains(scrape, `liken_reconcile_duration_seconds_count{kind="Cluster"} 1`) {
		t.Errorf("want exactly one sweep counted; the scrape holds:\n%s", scrape)
	}
}

// A sweep that runs before the watch delivers the grant the last sweep
// wrote cannot decide from the Machines' copy, because the copy would
// count one fewer machine in flight. It reads the Machines from the
// API server, sees the grant, and grants no second turn.
func TestASweepRightAfterItsOwnGrantReadsTheFleetDirectly(t *testing.T) {
	fake := newFleetAPI(time.Now())
	client, watcher := fleetClients(t, fake)
	waiting, err := kubernetes.GetMachine(client, "node-2")
	if err != nil {
		t.Fatal(err)
	}
	status := waiting.Status
	status.Conditions = []api.Condition{{Type: "SpecConverged", Status: api.ConditionFalse, Reason: "AwaitingTurn"}}
	if _, err := kubernetes.PublishStatus(client, waiting, &status); err != nil {
		t.Fatal(err)
	}
	wakes := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := watchFleet(ctx, watcher, client, func() {
		select {
		case wakes <- struct{}{}:
		default:
		}
	}, func(string) {})
	awaitFleetCopies(t, r, wakes)
	cm, _ := fleetMetrics(t)
	fake.Hold()

	if err := sweep(r, "lab", newChannelPoller(), &engineProbe{}, cm); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fake.Requests(), "PUT "+kubernetes.MachinesPath+"/node-2/status") {
		t.Fatalf("the first sweep sent %q, want a grant to node-2", fake.Requests())
	}
	fake.Forget()
	if err := sweep(r, "lab", newChannelPoller(), &engineProbe{}, cm); err != nil {
		t.Fatal(err)
	}

	sent := fake.Requests()
	if !slices.Contains(sent, "GET "+kubernetes.MachinesPath) {
		t.Errorf("the second sweep sent %q, want a direct read of the Machines", sent)
	}
	if slices.ContainsFunc(sent, func(r string) bool { return strings.HasPrefix(r, "PUT "+kubernetes.MachinesPath) }) {
		t.Errorf("the second sweep sent %q, want no second grant", sent)
	}
}

// The copies answer only while their view of this program's own
// leader Lease is fresh. A watch connection that went quiet leaves
// that view aging, and the sweep reads the API server instead, so it
// never judges heartbeats from a frozen copy.
func TestTheCopiesAnswerOnlyWhileTheLeaderLeaseInThemIsFresh(t *testing.T) {
	cases := []struct {
		name    string
		renewed time.Duration
		current bool
	}{
		{"renewed a second ago", time.Second, true},
		{"renewed one retry period ago", operatorLeaseTiming.retryPeriod, true},
		{"renewed fifteen seconds ago", copyFreshness, false},
		{"renewed a minute ago", time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newFleetAPI(time.Now().Add(-c.renewed))
			client, watcher := fleetClients(t, fake)
			wakes := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := watchFleet(ctx, watcher, client, informer.Signal(wakes), func(string) {})
			awaitFleetCopies(t, r, wakes)

			if got := r.current(r.machineCopy) != nil; got != c.current {
				t.Errorf("current = %v, want %v", got, c.current)
			}
		})
	}
}

// A write that the guard refused, or that answered 404, wrote nothing,
// so it leaves the copy answering.
func TestAWriteThatWroteNothingLeavesTheCopyAnswering(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"a write the guard refused", fmt.Errorf("PUT not sent: %w", errNotLeading)},
		{"a write to an object that is gone", kubernetes.ErrNotFound},
		{"a write that conflicted", kubernetes.ErrConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newFleetAPI(time.Now())
			client, watcher := fleetClients(t, fake)
			wakes := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := watchFleet(ctx, watcher, client, informer.Signal(wakes), func(string) {})
			awaitFleetCopies(t, r, wakes)

			recordWrite(r.machineCopy, "node-1", "", c.err)

			if _, ok := informer.List[machine.Machine](r.machineCopy); !ok {
				t.Error("the copy stopped answering after a write that wrote nothing")
			}
		})
	}
}
