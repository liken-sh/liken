package main

// These tests run whole passes against a fake API server that serves
// both plain reads and watches. They count the requests a pass sends,
// and check that a pass that reads the watch caches writes the same
// status as a pass that reads the API server. Each test that runs the
// watches runs in a synctest bubble, so synctest.Wait returns once
// every watch has received everything the fake sent.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
	"k8s.io/client-go/dynamic"
)

// newFleetAPI serves a fleet of node-1 and node-2 with fresh
// heartbeats, the Cluster, the DaemonSets in refreshedDaemonSets, and their pods.
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
		kubernetes.DisruptionsPath: {APIVersion: "liken.sh/v1alpha1", Kind: "Disruption"},
		kubernetes.ClustersPath: {APIVersion: "liken.sh/v1alpha1", Kind: "Cluster", Items: []map[string]any{
			object("liken.sh/v1alpha1", "Cluster", "", "lab", map[string]any{
				"spec": map[string]any{"leaders": []any{"node-1"}, "version": "2026.09.27-001"},
			}),
		}},
		"/apis/coordination.k8s.io/v1/namespaces/liken-system/leases": {APIVersion: "coordination.k8s.io/v1", Kind: "Lease", Items: []map[string]any{
			object("coordination.k8s.io/v1", "Lease", "liken-system", "node-1", map[string]any{"spec": map[string]any{"renewTime": renewed}}),
			object("coordination.k8s.io/v1", "Lease", "liken-system", "node-2", map[string]any{"spec": map[string]any{"renewTime": renewed}}),
			// The leader election Lease, whose renewal makes
			// fleetReader.current use the caches.
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

// fleetClients returns a client for plain reads and a client for the
// watches, both connected to api.
func fleetClients(t *testing.T, api http.Handler) (*apiclient.Client, dynamic.Interface) {
	t.Helper()
	server := apiservertest.Start(t, api)
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	watcher, err := dynamic.NewForConfig(server.Config())
	if err != nil {
		t.Fatalf("creating the dynamic client: %v", err)
	}
	return apiclient.New(apiservertest.Host, server.Client(), credentials), watcher
}

// awaitFleetCaches waits until the watches have processed everything
// the fake sent, and checks that the Machine, Cluster, Lease,
// DaemonSet, and pod caches have completed their first list.
func awaitFleetCaches(t *testing.T, r *fleetReader) {
	t.Helper()
	synctest.Wait()
	if !(r.machineCache.Synced() && r.clusterCache.Synced() && r.leaseCache.Synced() &&
		r.daemonSetCache.Synced() && r.podCache.Synced()) {
		t.Fatal("the watches have not completed their first list")
	}
}

// awaitClusterWrite waits until the watches have processed everything
// the fake sent, and checks that the Cluster cache has the Cluster at
// the fake's current version.
func awaitClusterWrite(t *testing.T, api *fakeapi.Server, r *fleetReader) {
	t.Helper()
	synctest.Wait()
	written := api.ResourceVersion(kubernetes.ClustersPath, "lab")
	if c, _, ok := watch.Get[cluster.Cluster](r.clusterCache.View(), "lab"); !ok || c.Metadata.ResourceVersion != written {
		t.Fatal("the Cluster cache does not have the version that the first pass wrote")
	}
}

// runPasses runs two passes and returns the requests of the second one
// and the Cluster it left. The first pass writes the Cluster's first
// status, so the second pass starts from a settled state.
func runPasses(t *testing.T, api *fakeapi.Server, r *fleetReader) ([]string, *cluster.Cluster) {
	t.Helper()
	cm, _ := fleetMetrics(t)
	for pass := range 2 {
		if pass == 1 {
			if r.clusterCache != nil {
				awaitClusterWrite(t, api, r)
			}
			api.Forget()
		}
		if err := reconcile(r, "lab", newChannelPoller(), &fluxProbe{}, &overduePodReports{}, cm); err != nil {
			t.Fatalf("running a pass: %v", err)
		}
	}
	requests := api.Requests()
	c, err := kubernetes.GetCluster(r.client, "lab")
	if err != nil {
		t.Fatalf("reading the Cluster: %v", err)
	}
	for i := range c.Status.Conditions {
		c.Status.Conditions[i].LastTransitionTime = time.Time{}
	}
	return requests, c
}

// A settled pass that reads the API server sends a read for every
// collection it needs. A settled pass that reads the watch caches
// sends only the read of the flux-system Namespace, which always goes
// to the API server on a fleet that does not declare flux. Both passes
// publish the same Cluster status.
func TestASettledSweepOverTheCopiesSendsOneRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()

		direct := newFleetAPI(now)
		client, _ := fleetClients(t, direct)
		directRequests, directCluster := runPasses(t, direct, &fleetReader{client: client})

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
		awaitFleetCaches(t, r)
		watchedRequests, watchedCluster := runPasses(t, watched, r)

		wantDirect := []string{
			"GET /apis/liken.sh/v1alpha1/clusters/lab",
			"GET /apis/liken.sh/v1alpha1/machines",
			"GET /apis/coordination.k8s.io/v1/namespaces/liken-system/leases",
			"GET /apis/liken.sh/v1alpha1/disruptions",
			"GET /apis/liken.sh/v1alpha1/machinedisruptionbudgets",
			"GET /apis/apps/v1/namespaces/liken-system/daemonsets/liken-machine-operator",
			"GET /apis/apps/v1/namespaces/liken-system/daemonsets/liken-machine-operator",
			"GET /api/v1/namespaces/liken-system/pods",
			"GET /apis/apps/v1/namespaces/liken-system/daemonsets/machine-logs",
			"GET /api/v1/namespaces/liken-system/pods",
			"GET /apis/apps/v1/namespaces/liken-system/daemonsets",
			"GET /api/v1/namespaces/flux-system",
		}
		if !slices.Equal(directRequests, wantDirect) {
			t.Errorf("a pass that reads the API server sent %q, want %q", directRequests, wantDirect)
		}
		if want := []string{"GET /api/v1/namespaces/flux-system"}; !slices.Equal(watchedRequests, want) {
			t.Errorf("a pass over the caches sent %q, want %q", watchedRequests, want)
		}
		d, _ := json.Marshal(directCluster.Status)
		w, _ := json.Marshal(watchedCluster.Status)
		if string(d) != string(w) {
			t.Errorf("the pass over the caches published\n%s\nwant what the direct pass published\n%s", w, d)
		}
	})
}

// operate finds the Cluster, passes, and returns after finishing the
// pass in progress when stop ends. Here stop ends during the first
// pass's write of the Cluster's status, so exactly one pass runs and
// its write completes.
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

	operate(stop, &fleetReader{client: client}, make(chan struct{}), make(chan time.Time), o, cm)

	if got := fake.ResourceVersion(kubernetes.ClustersPath, "lab"); got == "5" {
		t.Error("the pass in progress did not publish the status of the Cluster")
	}
	if scrape := scrapeHandler(t, o.Handler()); !strings.Contains(scrape, `liken_reconcile_duration_seconds_count{kind="Cluster"} 1`) {
		t.Errorf("the scrape is\n%s\nwant one counted pass of the Cluster", scrape)
	}
}

// A pass that runs before the watch receives the previous pass's
// grant reads that Machine from the API server, sees the grant, and
// grants no second turn. The Machine cache, without the grant, would
// count one fewer machine rebooting.
func TestAPassRightAfterItsOwnGrantReadsThatMachineDirectly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFleetAPI(time.Now())
		client, watcher := fleetClients(t, fake)
		waiting, err := kubernetes.GetMachine(client, "node-2")
		if err != nil {
			t.Fatalf("reading Machine node-2: %v", err)
		}
		status := waiting.Status
		status.Conditions = []api.Condition{{Type: "SpecConverged", Status: api.ConditionFalse, Reason: "AwaitingTurn"}}
		if _, err := kubernetes.PublishStatus(client, waiting, &status); err != nil {
			t.Fatalf("publishing the status of Machine node-2: %v", err)
		}
		fileRequest(t, client, "node-2")
		wakes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchFleet(ctx, watcher, client, func() {
			select {
			case wakes <- struct{}{}:
			default:
			}
		}, func(string) {})
		awaitFleetCaches(t, r)
		cm, _ := fleetMetrics(t)
		// A new process grants no turn until its first reading of
		// the heartbeats is HeartbeatStaleAfter old (decideRollout).
		if err := reconcile(r, "lab", newChannelPoller(), &fluxProbe{}, &overduePodReports{}, cm); err != nil {
			t.Fatalf("running the first pass: %v", err)
		}
		renewFor(t, client, kubernetes.HeartbeatStaleAfter+time.Second, []string{"node-1", "node-2"})
		fake.Hold()

		if err := reconcile(r, "lab", newChannelPoller(), &fluxProbe{}, &overduePodReports{}, cm); err != nil {
			t.Fatalf("running the second pass: %v", err)
		}
		if !slices.Contains(fake.Requests(), "PUT "+kubernetes.MachinesPath+"/node-2/status") {
			t.Fatalf("the first pass sent %q, want a grant to node-2", fake.Requests())
		}
		fake.Forget()
		if err := reconcile(r, "lab", newChannelPoller(), &fluxProbe{}, &overduePodReports{}, cm); err != nil {
			t.Fatalf("running the third pass: %v", err)
		}

		sent := fake.Requests()
		if !slices.Contains(sent, "GET "+kubernetes.MachinesPath+"/node-2") {
			t.Errorf("the second pass sent %q, want a direct read of node-2", sent)
		}
		if slices.ContainsFunc(sent, func(r string) bool { return strings.HasPrefix(r, "PUT "+kubernetes.MachinesPath) }) {
			t.Errorf("the second pass sent %q, want no second grant", sent)
		}
	})
}

// The pass reads the caches only while the leader Lease in the Lease
// cache was renewed less than cacheFreshness ago. When the watch
// connection stops sending, that renewal ages, and the pass reads the
// API server.
func TestTheCopiesAnswerOnlyWhileTheLeaderLeaseInThemIsFresh(t *testing.T) {
	cases := []struct {
		name    string
		renewed time.Duration
		current bool
	}{
		{"renewed a second ago", time.Second, true},
		{"renewed one retry period ago", election.RetryPeriod, true},
		{"renewed fifteen seconds ago", cacheFreshness, false},
		{"renewed a minute ago", time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fake := newFleetAPI(time.Now().Add(-c.renewed))
				client, watcher := fleetClients(t, fake)
				wakes := make(chan struct{}, 1)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				r := watchFleet(ctx, watcher, client, watch.Signal(wakes), func(string) {})
				awaitFleetCaches(t, r)

				if got := r.current(r.machineCache).Ready(); got != c.current {
					t.Errorf("current = %v, want %v", got, c.current)
				}
			})
		})
	}
}

// After a failed write, even one that changed nothing, such as one the
// write guard refused or one that returned 404 or 409, this program has
// no known-current version of that Machine. The next list reads that
// Machine from the API server, and after that the cache is used again.
func TestAFailedWriteCostsOneReadOfThatMachine(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"a write the guard refused", fmt.Errorf("PUT not sent: %w", election.ErrNotLeading)},
		{"a write to an object that is gone", apiclient.ErrNotFound},
		{"a write that conflicted", apiclient.ErrConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fake := newFleetAPI(time.Now())
				client, watcher := fleetClients(t, fake)
				wakes := make(chan struct{}, 1)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				r := watchFleet(ctx, watcher, client, watch.Signal(wakes), func(string) {})
				awaitFleetCaches(t, r)
				fake.Forget()

				_ = r.machineVersions.Send("node-1", func() (string, error) { return "", c.err })

				for range 2 {
					if machines, err := r.machines(); err != nil || len(machines) != 2 {
						t.Fatalf("the list is %d Machines with error %v, want 2 Machines and no error", len(machines), err)
					}
				}
				if sent := fake.Requests(); !slices.Equal(sent, []string{"GET " + kubernetes.MachinesPath + "/node-1"}) {
					t.Errorf("two lists after the failed write sent %q, want one read of node-1", sent)
				}
			})
		})
	}
}

// awaitMachineCached waits until the watches have processed everything
// the fake sent, and checks that the Machine cache has the named
// Machine at the fake's current version.
func awaitMachineCached(t *testing.T, api *fakeapi.Server, r *fleetReader, name string) {
	t.Helper()
	synctest.Wait()
	want := api.ResourceVersion(kubernetes.MachinesPath, name)
	if m, _, ok := watch.Get[machine.Machine](r.machineCache.View(), name); !ok || m.Metadata.ResourceVersion != want {
		t.Fatalf("the Machine cache does not have %s at version %s", name, want)
	}
}

// Once the cache has this program's own write, the recorded version is
// removed, so a later write by another writer, such as machine-operator's
// status write, does not make the next list read that Machine.
func TestAWriteFromAnotherWriterAfterACachedGrantCostsNoRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFleetAPI(time.Now())
		client, watcher := fleetClients(t, fake)
		wakes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchFleet(ctx, watcher, client, watch.Signal(wakes), func(string) {})
		awaitFleetCaches(t, r)
		granted, err := kubernetes.GetMachine(client, "node-2")
		if err != nil {
			t.Fatalf("reading Machine node-2: %v", err)
		}
		status := granted.Status
		status.Conditions = []api.Condition{{Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows"}}
		if err := r.publishStatus(granted, &status); err != nil {
			t.Fatalf("publishing the status of Machine node-2 through the fleet reader: %v", err)
		}
		awaitMachineCached(t, fake, r, "node-2")
		if _, err := r.machines(); err != nil {
			t.Fatalf("listing the Machines: %v", err)
		}

		own, err := kubernetes.GetMachine(client, "node-2")
		if err != nil {
			t.Fatalf("reading Machine node-2: %v", err)
		}
		status = own.Status
		status.Phase = api.PhaseUpdating
		if _, err := kubernetes.PublishStatus(client, own, &status); err != nil {
			t.Fatalf("publishing the status of Machine node-2: %v", err)
		}
		awaitMachineCached(t, fake, r, "node-2")
		fake.Forget()

		if _, err := r.machines(); err != nil {
			t.Fatalf("listing the Machines: %v", err)
		}
		if sent := fake.Requests(); len(sent) != 0 {
			t.Errorf("a list after another writer's change sent %q, want no requests", sent)
		}
	})
}

// failingWatches serves the watches of a fake API server, and fails
// each of their requests with a 503 once fail is set, as an API server
// does while it restarts. It fails the reflector's lists as well as its
// watches, because the fake would count a reflector's list as one of
// the pass's reads.
type failingWatches struct {
	api  *fakeapi.Server
	fail atomic.Bool
}

func (f *failingWatches) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.fail.Load() {
		http.Error(w, "the API server is restarting", http.StatusServiceUnavailable)
		return
	}
	f.api.ServeHTTP(w, r)
}

// After a watch fails, such as at an API server restart, the pass
// reads the Machines from the API server, even while the leader Lease
// in the Lease cache is fresh. A Machine cache that missed a machine's
// Degraded status would count it as available, and the pass could
// grant turns beyond the disruption budget.
func TestAPassReadsTheFleetDirectlyAfterAWatchFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFleetAPI(time.Now())
		watches := &failingWatches{api: fake}
		client, _ := fleetClients(t, fake)
		failing := apiservertest.Start(t, watches)
		watcher, err := dynamic.NewForConfig(failing.Config())
		if err != nil {
			t.Fatalf("creating the dynamic client: %v", err)
		}
		wakes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchFleet(ctx, watcher, client, watch.Signal(wakes), func(string) {})
		awaitFleetCaches(t, r)

		watches.fail.Store(true)
		failing.SetDown(true)
		failing.SetDown(false)
		// The reflector reopens the watch after its backoff, which
		// starts under two seconds, and the fake refuses it.
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if r.machineCache.View().Ready() {
			t.Fatal("the Machine cache is still ready ten seconds after its watch failed")
		}
		fake.Forget()
		if _, err := r.machines(); err != nil {
			t.Fatalf("listing the Machines: %v", err)
		}
		if sent := fake.Requests(); !slices.Equal(sent, []string{"GET " + kubernetes.MachinesPath}) {
			t.Errorf("the list after the failed watch sent %q, want a direct read of the Machines", sent)
		}
	})
}
