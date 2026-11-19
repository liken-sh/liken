package main

// Tests for the watches of the waits, using the fake API server's
// watch streams in a synctest bubble.

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// waitAPI returns a fake API server with the collections that the
// waits watch.
func waitAPI(pods, charts []map[string]any) *fakeapi.Server {
	return fakeapi.New(map[string]*fakeapi.Collection{
		"/api/v1/pods":                         {APIVersion: "v1", Kind: "Pod", Items: pods},
		"/apis/helm.cattle.io/v1/helmcharts":   {APIVersion: "helm.cattle.io/v1", Kind: "HelmChart", Items: charts},
		"/apis/policy/v1/poddisruptionbudgets": {APIVersion: "policy/v1", Kind: "PodDisruptionBudget"},
		"/api/v1/services":                     {APIVersion: "v1", Kind: "Service"},
	})
}

// podObject is one pod on node-1 with one container.
func podObject(name, namespace, image string, ready bool) map[string]any {
	return fakeapi.Object("v1", "Pod", namespace, name, map[string]any{
		"spec": map[string]any{"nodeName": "node-1"},
		"status": map[string]any{"phase": "Running", "containerStatuses": []any{
			map[string]any{"name": "main", "image": image, "ready": ready},
		}},
	})
}

// waitingReader returns a reader whose waits watch the fake server,
// and the channel that the watches signal.
func waitingReader(t *testing.T, fake *fakeapi.Server) (*reader, chan struct{}) {
	t.Helper()
	client, watcher := passClients(t, fake)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	wakes := make(chan struct{}, 1)
	return &reader{client: client, waits: newWaits(ctx, watcher, watch.Signal(wakes), "node-1")}, wakes
}

// put replaces an object on the fake server, as a write from another
// program does.
func put(t *testing.T, r *reader, path string, object map[string]any) {
	t.Helper()
	body, _ := json.Marshal(object)
	if err := r.client.RequestJSON(http.MethodPut, path, body, nil); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// woke reports whether a wake signal is pending, and consumes it.
func woke(wakes chan struct{}) bool {
	synctest.Wait()
	select {
	case <-wakes:
		return true
	default:
		return false
	}
}

// The first pass of the image proof reads the API server and starts the
// pod watch. When the last OS container becomes Ready, the watch wakes
// the next pass, which reads the copy and promotes the record without
// waiting for a timer.
func TestTheImageProofPromotesWhenTheLastContainerBecomesReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		containerStoreDir = t.TempDir()
		t.Cleanup(func() { containerStoreDir = machine.K3sAgentDir })
		root, facts := stagedImportsBoot(t)
		fake := waitAPI([]map[string]any{podObject("liken-dns-abc", "liken-system", osImagePrefix+"dns:1", false)}, nil)
		r, wakes := waitingReader(t, fake)

		first := settleImportsLifecycle(r, root, "node-1", facts, nil)
		r.waits.endPass()
		woke(wakes)
		put(t, r, "/api/v1/pods/liken-dns-abc", podObject("liken-dns-abc", "liken-system", osImagePrefix+"dns:1", true))
		wokeOnReady := woke(wakes)
		fake.Forget()
		second := settleImportsLifecycle(r, root, "node-1", facts, nil)

		if first.Reason == "Converged" || !wokeOnReady || second.Reason != "Converged" || len(fake.Requests()) != 0 {
			t.Errorf("first %s, woke on Ready %v, second %s with requests %q; want a wait, a wake, and a promotion from the copy",
				first.Reason, wokeOnReady, second.Reason, fake.Requests())
		}
	})
}

// The pod watch wakes a drain when a pod leaves. The pod watch and the
// budget watch stop after the first pass that does not drain.
func TestADrainWakesWhenAPodLeavesAndItsWatchesStopWhenItEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := waitAPI([]map[string]any{podObject("web", "default", "nginx", true)}, nil)
		r, wakes := waitingReader(t, fake)
		node := drainNode(true, true, drainNow.Add(-time.Minute).Format(time.RFC3339))

		held := gateThroughDrain(r, node, rebootingConvergence(), drainNow, machineEvents{}, nil)
		r.waits.endPass()
		woke(wakes)
		if err := r.client.RequestJSON(http.MethodDelete, "/api/v1/pods/web", nil, nil); err != nil {
			t.Fatalf("deleting the pod web: %v", err)
		}
		wokeOnLeave := woke(wakes)
		clear := gateThroughDrain(r, node, rebootingConvergence(), drainNow, machineEvents{}, nil)
		r.waits.endPass()
		running := len(r.waits.running)
		r.waits.endPass()

		if held.requestReboot || !wokeOnLeave || !clear.requestReboot {
			t.Errorf("the first pass rebooted %v, a wake came %v, the second rebooted %v; want held, woken, released",
				held.requestReboot, wokeOnLeave, clear.requestReboot)
		}
		if running != 2 || len(r.waits.running) != 0 {
			t.Errorf("%d watches ran during the drain and %d after a pass with none, want 2 and 0", running, len(r.waits.running))
		}
	})
}

// The kubelet updates a pod's status while a container restarts. A
// write that changes none of the fields that trimPod keeps must not
// wake a pass. Each drain pass tries to evict every pod again, so a
// wake on each kubelet write would retry an eviction that a budget
// refused.
func TestAPodWriteOutsideTheTrimmedFieldsDoesNotWakeTheDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := waitAPI([]map[string]any{podObject("web", "default", "nginx", false)}, nil)
		r, wakes := waitingReader(t, fake)
		node := drainNode(true, true, drainNow.Add(-time.Minute).Format(time.RFC3339))
		gateThroughDrain(r, node, rebootingConvergence(), drainNow, machineEvents{}, nil)
		woke(wakes)

		restarted := podObject("web", "default", "nginx", false)
		restarted["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)["restartCount"] = int64(4)
		put(t, r, "/api/v1/pods/web", restarted)
		wokeOnRestart := woke(wakes)
		put(t, r, "/api/v1/pods/web", podObject("web", "default", "nginx", true))
		wokeOnReady := woke(wakes)

		if wokeOnRestart || !wokeOnReady {
			t.Errorf("a restart woke %v and a Ready container woke %v, want false and true", wokeOnRestart, wokeOnReady)
		}
	})
}

// A drain that is still waiting sets a wake time at its deadline, and
// the pass at the deadline lets the reboot proceed. The deadline comes
// from the drain annotation, so an operator that restarts during a
// drain computes the same one.
func TestADrainWakesAtItsDeadlineAndProceedsThere(t *testing.T) {
	since := drainNow.Add(-time.Minute)
	node := drainNode(true, true, since.Format(time.RFC3339))
	fake := &drainAPI{pods: []kubernetes.Pod{pod("stubborn", "default")}}
	out := &passOutcome{}

	gateThroughDrain(&reader{client: testClient(t, fake.handler())}, node, rebootingConvergence(), drainNow, machineEvents{}, out)
	atDeadline := decideDrainStep(node, []kubernetes.Pod{pod("stubborn", "default")}, since.Add(drainDeadline))

	if want := since.Add(drainDeadline); !out.wake.Equal(want) {
		t.Errorf("the drain set the wake time to %s, want %s", out.wake, want)
	}
	if !atDeadline.clear {
		t.Error("the pass at the deadline did not request the reboot, want it requested")
	}
}

// evictionAPI responds to an eviction by the pod's name: 429 with a
// Retry-After header, 429 without one, 500, or 404.
func evictionAPI(t *testing.T) http.Handler {
	t.Helper()
	pods := []kubernetes.Pod{pod("waits", "default"), pod("refused", "default"), pod("broken", "default"), pod("gone", "default")}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": pods})
		case strings.HasSuffix(r.URL.Path, "/waits/eviction"):
			w.Header().Set("Retry-After", "7")
			http.Error(w, `{"kind":"Status","code":429}`, http.StatusTooManyRequests)
		case strings.HasSuffix(r.URL.Path, "/refused/eviction"):
			http.Error(w, `{"kind":"Status","code":429,"message":"Cannot evict pod as it would violate the pod's disruption budget."}`, http.StatusTooManyRequests)
		case strings.HasSuffix(r.URL.Path, "/broken/eviction"):
			http.Error(w, "etcd is gone", http.StatusInternalServerError)
		default:
			http.Error(w, `{"kind":"Status","code":404}`, http.StatusNotFound)
		}
	})
}

// The drain handles each eviction response differently. A 429 from a
// budget is not a failure. With a Retry-After header, the drain sets
// the wake time to that moment, and without one it waits for the
// budget watch. A 404 means the pod is already gone. A 500 is a
// failure that the next pass retries.
func TestADrainHandlesEachEvictionResponse(t *testing.T) {
	out := &passOutcome{}
	client := testClient(t, evictionAPI(t)).WithObserver(out.observe)
	node := drainNode(true, true, drainNow.Add(-time.Minute).Format(time.RFC3339))

	gateThroughDrain(&reader{client: client}, node, rebootingConvergence(), drainNow, machineEvents{}, out)

	if len(out.failures) != 1 || !strings.Contains(out.failures[0].step, "/broken/eviction") {
		t.Errorf("failures %+v, want the 500 alone", out.failures)
	}
	if want := drainNow.Add(7 * time.Second); !out.wake.Equal(want) {
		t.Errorf("the drain set the wake time to %s, want the 429's %s", out.wake, want)
	}
}

// A budget wakes the drain when it allows more disruptions than
// before and when it is deleted. A budget that allows fewer, or a new
// budget, only restricts more and wakes nothing.
func TestABudgetWakesTheDrainWhenItAllowsAnEviction(t *testing.T) {
	budget := func(allowed int64) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"disruptionsAllowed": allowed}}}
		return u
	}
	cases := []struct {
		name  string
		event func(h interface {
			OnAdd(any, bool)
			OnUpdate(any, any)
			OnDelete(any)
		})
		wakes bool
	}{
		{"more allowed", func(h interface {
			OnAdd(any, bool)
			OnUpdate(any, any)
			OnDelete(any)
		}) {
			h.OnUpdate(budget(0), budget(1))
		}, true},
		{"fewer allowed", func(h interface {
			OnAdd(any, bool)
			OnUpdate(any, any)
			OnDelete(any)
		}) {
			h.OnUpdate(budget(1), budget(0))
		}, false},
		{"deleted", func(h interface {
			OnAdd(any, bool)
			OnUpdate(any, any)
			OnDelete(any)
		}) {
			h.OnDelete(budget(0))
		}, true},
		{"new", func(h interface {
			OnAdd(any, bool)
			OnUpdate(any, any)
			OnDelete(any)
		}) {
			h.OnAdd(budget(0), false)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			woke := false
			tc.event(budgetHandler(func() { woke = true }))
			if woke != tc.wakes {
				t.Errorf("woke %v, want %v", woke, tc.wakes)
			}
		})
	}
}

// The chart watch wakes the pass when the last HelmChart is deleted,
// and stops after the first pass that no longer waits.
func TestAHeldRetractionFinishesWhenTheLastChartIsDeleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		chart := fakeapi.Object("helm.cattle.io/v1", "HelmChart", "kube-system", "traefik", nil)
		fake := waitAPI(nil, []map[string]any{chart})
		r, wakes := waitingReader(t, fake)

		satisfiedFirst, _, err := evaluatePrecondition(r, cluster.NoHelmCharts)
		if err != nil {
			t.Fatalf("evaluating the helm precondition: %v", err)
		}
		r.waits.endPass()
		woke(wakes)
		if err := r.client.RequestJSON(http.MethodDelete, "/apis/helm.cattle.io/v1/helmcharts/traefik", nil, nil); err != nil {
			t.Fatalf("deleting the HelmChart traefik: %v", err)
		}
		wokeOnDelete := woke(wakes)
		satisfiedSecond, _, _ := evaluatePrecondition(r, cluster.NoHelmCharts)
		r.waits.endPass()
		r.waits.endPass()

		if satisfiedFirst || !wokeOnDelete || !satisfiedSecond || len(r.waits.running) != 0 {
			t.Errorf("first satisfied %v, woke %v, second satisfied %v, %d watches left; want false, true, true, 0",
				satisfiedFirst, wokeOnDelete, satisfiedSecond, len(r.waits.running))
		}
	})
}

// loadBalancerServices reads the Service watch's copy, which keeps each
// Service's type, and returns only the LoadBalancer Services.
func TestLoadBalancerServicesReadsTheServiceCopy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := fakeapi.New(map[string]*fakeapi.Collection{"/api/v1/services": {APIVersion: "v1", Kind: "Service", Items: []map[string]any{
			fakeapi.Object("v1", "Service", "media", "jellyfin", map[string]any{"spec": map[string]any{"type": "LoadBalancer", "ports": []any{map[string]any{"port": 8096}}}}),
			fakeapi.Object("v1", "Service", "media", "internal", map[string]any{"spec": map[string]any{"type": "ClusterIP"}}),
		}}})
		r, wakes := waitingReader(t, fake)
		_, _ = r.loadBalancerServices()
		r.waits.endPass()
		woke(wakes)
		fake.Forget()

		services, err := r.loadBalancerServices()

		if err != nil || len(services) != 1 || services[0].Metadata.Name != "jellyfin" || len(fake.Requests()) != 0 {
			t.Errorf("services %+v (%v) with requests %q, want jellyfin from the copy", services, err, fake.Requests())
		}
	})
}

// A Service wakes the retraction when it is added, deleted, or changes
// its type, and not when any other field changes.
func TestAServiceWakesTheRetractionOnlyForItsPresenceOrType(t *testing.T) {
	service := func(kind string, port int64) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"type": kind, "port": port}}}
	}
	wakes := 0
	h := serviceHandler(func() { wakes++ })
	h.OnAdd(service("ClusterIP", 80), false)
	h.OnUpdate(service("ClusterIP", 80), service("ClusterIP", 81))
	h.OnUpdate(service("ClusterIP", 81), service("LoadBalancer", 81))
	h.OnDelete(service("LoadBalancer", 81))
	presence := 0
	p := presenceHandler(func() { presence++ })
	p.OnAdd(nil, false)
	p.OnUpdate(nil, nil)
	p.OnDelete(nil)

	if wakes != 3 || presence != 2 {
		t.Errorf("the Service handler woke %d times and the chart handler %d, want 3 and 2", wakes, presence)
	}
}

// A full pod and the same pod after trimPod convert to the same
// kubernetes.Pod, except for annotations that no code reads.
func TestTheTrimKeepsEveryFieldThePodTypeReads(t *testing.T) {
	raw := `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"player","namespace":"media","uid":"u-1","resourceVersion":"42",
		"deletionTimestamp":"2026-10-09T15:00:00Z","labels":{"app":"player"},
		"annotations":{"kubernetes.io/config.mirror":"abc","liken.sh/os-version":"2026.10.09-001","other":"x"},
		"ownerReferences":[{"apiVersion":"apps/v1","kind":"DaemonSet","name":"player","uid":"d-1"}],
		"managedFields":[{"manager":"kubelet"}]},
		"spec":{"nodeName":"node-1","containers":[{"name":"main","image":"mpv"}],
		"volumes":[{"name":"plugins","hostPath":{"path":"/var/lib/kubelet/plugins","type":"Directory"}},{"name":"cache","emptyDir":{}}],
		"resourceClaims":[{"name":"gpu","resourceClaimTemplateName":"render"}],
		"tolerations":[{"key":"node.kubernetes.io/unschedulable","operator":"Exists","effect":"NoSchedule"},{"operator":"Exists"}]},
		"status":{"phase":"Running","podIP":"10.42.0.9","containerStatuses":[{"name":"main","image":"mpv:1","ready":true,"restartCount":3}]}}`
	full := &unstructured.Unstructured{}
	if err := full.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatalf("parsing the pod: %v", err)
	}
	trimmed := full.DeepCopy()
	trimPod(trimmed)

	want, errWant := informer.Convert[kubernetes.Pod](full)
	got, errGot := informer.Convert[kubernetes.Pod](trimmed)
	if errWant != nil || errGot != nil {
		t.Fatalf("converting: %v, %v", errWant, errGot)
	}
	delete(want.Metadata.Annotations, "other")

	if !reflect.DeepEqual(got, want) {
		t.Errorf("trimmed pod converts to\n%+v\nwant\n%+v", got, want)
	}
}

// A reader with no waits reads from the API server, as the readers in
// the pass tests do.
func TestAReaderWithNoWaitsListsTheAPIServer(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("web", "default")}}
	r := &reader{client: testClient(t, fake.handler())}

	pods, err := r.nodePods("node-1")
	r.watchBudgets()
	r.waits.endPass()

	if err != nil || len(pods) != 1 {
		t.Errorf("pods %+v (%v), want web from the API server", pods, err)
	}
}
