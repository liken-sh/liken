package main

// These tests run each watch's handler through client-go's real
// reflector, against the scripted API server in watchserver_test.go.
// They check that each change the server sends reaches this program's
// handlers, and that each watch requests the objects it needs. The
// reflector's loop and the informer's copy of the collection are
// tested upstream and in the informer package.
//
// Each test that runs a watch runs in a synctest bubble.
// synctest.Wait returns when the reflector has nothing left to do at
// the current fake time, so a wake that has not arrived by then is one
// that will not arrive. time.Sleep waits out the reflector's backoff on
// the fake clock.

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// The Layout and Pod watches, each with its collection and the field
// selector it should send. The shared test changes only names and
// labels, which wake both. The Display watch ignores a label change, so
// it has its own tests below.
var wakeWatches = []struct {
	kind       string
	collection string
	apiVersion string
	fields     string
	start      openWatch
}{
	{"Layout", LayoutsPath, DisplayAPIVersion, "", openLayouts},
	{"Pod", PodsPath, "v1", "spec.nodeName=node-1", podsOnNode1},
}

// openWatch is the signature shared by openDisplays, openLayouts, and
// podsOnNode1.
type openWatch func(ctx context.Context, client dynamic.Interface, wake func(), readings *metrics) *informer.Collection

// podsOnNode1 starts the pod watch for node-1.
func podsOnNode1(ctx context.Context, client dynamic.Interface, wake func(), readings *metrics) *informer.Collection {
	return openPods(ctx, client, "node-1", wake, readings)
}

// runWakeWatch starts a watch against the store and returns a channel
// that receives a value for each wake. The watch stops when the test
// ends.
func runWakeWatch(t *testing.T, store *objectStore, start openWatch, readings *metrics) <-chan struct{} {
	t.Helper()
	wakes := make(chan struct{}, 1)
	ctx, stop := context.WithCancel(t.Context())
	watch := start(ctx, store.watcher(), func() {
		select {
		case wakes <- struct{}{}:
		default:
		}
	}, readings)
	t.Cleanup(func() {
		stop()
		<-watch.Done()
	})
	return wakes
}

// woke reports whether the watch called wake, after letting the
// reflector finish its pending work.
func woke(wakes <-chan struct{}) bool {
	synctest.Wait()
	select {
	case <-wakes:
		return true
	default:
		return false
	}
}

func awaitWake(t *testing.T, wakes <-chan struct{}, what string) {
	t.Helper()
	if !woke(wakes) {
		t.Fatalf("%s did not wake a pass, want one", what)
	}
}

func awaitNoWake(t *testing.T, wakes <-chan struct{}, what string) {
	t.Helper()
	if woke(wakes) {
		t.Fatalf("%s woke a pass, want none", what)
	}
}

func objectOf(kind, name string) map[string]any {
	return map[string]any{"metadata": map[string]any{"name": name, "namespace": "default"}, "kind": kind}
}

// The first list calls wake even when it finds no objects, and so does
// each later add, edit, and removal. Every request carries the field
// selector the pass needs.
func TestAWatchWakesOnTheFirstListAndOnEveryChange(t *testing.T) {
	for _, watch := range wakeWatches {
		t.Run(watch.kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := newObjectStore(t, watch.collection, watch.apiVersion, watch.kind)
				wakes := runWakeWatch(t, store, watch.start, nil)
				awaitWake(t, wakes, "the first read")
				if store.watching() != 1 {
					t.Fatalf("the store holds %d watches, want 1", store.watching())
				}

				store.put(objectOf(watch.kind, "one"))
				awaitWake(t, wakes, "a new object")
				edited := objectOf(watch.kind, "one")
				edited["metadata"].(map[string]any)["labels"] = map[string]any{"region": "left"}
				store.put(edited)
				awaitWake(t, wakes, "an edit")
				store.remove("one")
				awaitWake(t, wakes, "a removal")

				for _, query := range store.asked() {
					if got := query.Get("fieldSelector"); got != watch.fields {
						t.Errorf("a request selected %q, want %q", got, watch.fields)
					}
				}
			})
		})
	}
}

// podAt builds a pod with one label and a pod IP.
func podAt(uid, region, address string) map[string]any {
	return map[string]any{
		"kind":     "Pod",
		"metadata": map[string]any{"name": "player", "namespace": "default", "uid": uid, "labels": map[string]any{"region": region}},
		"status":   map[string]any{"podIP": address},
	}
}

// The pod watch wakes on the first list, a new pod, a label change, a
// pod deleted and recreated, and a removal. The placement pass reads
// only a pod's name, namespace, and labels, so a status write such as a
// new pod IP wakes nothing.
func TestThePodWatchWakesOnALabelChangeAndNotOnAStatusWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newObjectStore(t, PodsPath, "v1", "Pod")
		wakes := runWakeWatch(t, store, podsOnNode1, nil)
		awaitWake(t, wakes, "the first read")

		store.put(podAt("uid-1", "left", "10.42.0.7"))
		awaitWake(t, wakes, "a new pod")
		store.put(podAt("uid-1", "left", "10.42.0.8"))
		awaitNoWake(t, wakes, "a status write")
		store.put(podAt("uid-1", "right", "10.42.0.8"))
		awaitWake(t, wakes, "a label change")
		store.put(podAt("uid-2", "right", "10.42.0.8"))
		awaitWake(t, wakes, "a pod deleted and created again")
		store.remove("player")
		awaitWake(t, wakes, "a removal")
	})
}

// displayAt builds a Display at the given generation, with status.node
// and status.connector set.
func displayAt(generation int64, node, connector string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": "gsm-7716-lg-hdr-wqhd", "generation": generation, "uid": "uid-1"},
		"status":   map[string]any{"node": node, "connector": connector},
	}
}

// The Display watch wakes on the first list, a new Display, a spec
// edit, a Display that another node's controller adopted, and a
// removal. It does not wake on any other status write.
func TestTheDisplayWatchWakesOnAnEditAndNotOnAStatusWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newObjectStore(t, DisplaysPath, DisplayAPIVersion, "Display")
		wakes := runWakeWatch(t, store, openDisplays, nil)
		awaitWake(t, wakes, "the first read")

		store.put(displayAt(1, "node-1", "HDMI-A-1"))
		awaitWake(t, wakes, "a new Display")
		store.put(displayAt(1, "node-1", "HDMI-A-2"))
		awaitNoWake(t, wakes, "a status write")
		store.put(displayAt(2, "node-1", "HDMI-A-2"))
		awaitWake(t, wakes, "a spec edit")
		store.put(displayAt(2, "node-2", "HDMI-A-2"))
		awaitWake(t, wakes, "an adoption")
		store.remove("gsm-7716-lg-hdr-wqhd")
		awaitWake(t, wakes, "a removal")
	})
}

// The Display watch treats a change to the generation, the deletion
// timestamp, the UID, or status.node as an edit. It also treats a
// value that is not an object as an edit, because it cannot tell what
// changed.
func TestDisplayEditsWakeOnGenerationDeletionUIDAndNodeChanges(t *testing.T) {
	adopted := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"node": "node-2"}}}
	adopted.SetGeneration(1)
	adopted.SetUID("uid-1")
	object := func(generation int64, uid string, deleting bool) any {
		item := &unstructured.Unstructured{Object: map[string]any{}}
		item.SetGeneration(generation)
		item.SetUID(types.UID(uid))
		if deleting {
			now := metav1.Now()
			item.SetDeletionTimestamp(&now)
		}
		return item
	}
	cases := []struct {
		name  string
		after any
		want  bool
	}{
		{"a status write", object(1, "uid-1", false), false},
		{"a spec edit", object(2, "uid-1", false), true},
		{"a deletion request", object(1, "uid-1", true), true},
		{"a Display deleted and created again", object(1, "uid-2", false), true},
		{"a Display another node adopted", adopted, true},
		{"something that is not an object", "not an object", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			woke := false
			displayEdits(func() { woke = true }).OnUpdate(object(1, "uid-1", false), c.after)
			if woke != c.want {
				t.Errorf("the change woke a pass: %t, want %t", woke, c.want)
			}
		})
	}
}

// Each watch the reflector opens after the first increments
// display_watch_restarts_total, so the metric shows a watch that the
// API server or the network keeps closing.
func TestAReopenedWakeWatchCountsARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newObjectStore(t, DisplaysPath, DisplayAPIVersion, "Display")
		readings := newMetrics(componentName, "dev")
		wakes := runWakeWatch(t, store, openDisplays, readings)
		awaitWake(t, wakes, "the first read")
		restarts := readings.watchRestarts.WithLabelValues(string(kindDisplay))
		if got := testutil.ToFloat64(restarts); got != 0 {
			t.Fatalf("the first watch counted %v restarts, want 0", got)
		}

		store.hangUp()
		time.Sleep(time.Minute)
		synctest.Wait()

		if got := testutil.ToFloat64(restarts); got != 1 {
			t.Errorf("one watch ended and was opened again, and it counted %v restarts, want 1", got)
		}
		if store.watching() != 1 {
			t.Errorf("the store holds %d watches, want the one opened again", store.watching())
		}
	})
}

// A watch request that the API server refuses is a retry and not a
// restart, so an API server that is down does not raise the count.
func TestARefusedWatchCountsNoRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		readings := newMetrics(componentName, "dev")
		var requests atomic.Int64
		watcher := testWatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		ctx, stop := context.WithCancel(t.Context())
		watch := openDisplays(ctx, watcher, func() {}, readings)

		time.Sleep(time.Minute)
		synctest.Wait()
		stop()
		<-watch.Done()

		if requests.Load() < 3 {
			t.Fatalf("the watch sent %d requests in a minute, want at least 3 retries", requests.Load())
		}
		if got := testutil.ToFloat64(readings.watchRestarts.WithLabelValues(string(kindDisplay))); got != 0 {
			t.Errorf("refused watches counted %v restarts, want 0", got)
		}
	})
}
