package main

// These tests run the handlers of each watch through client-go's real
// reflector, against the scripted API server in watchserver_test.go.
// The reflector's own loop is upstream's to test, and the shared
// informer package tests the copy it keeps and the decode of each
// object. What these tests prove is that each change the API server
// sends reaches this program's handlers, and that each watch asks for
// the objects it must.

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// The two watches that wake a pass on every change, each with the
// collection it watches and the objects it reads there. The Display
// watch wakes on an edit alone, and its tests follow these.
var wakeWatches = []struct {
	kind       string
	collection string
	apiVersion string
	fields     string
	start      func(ctx context.Context, client dynamic.Interface, wake func(), readings *metrics)
}{
	{"Layout", LayoutsPath, DisplayAPIVersion, "", watchLayouts},
	{"Pod", PodsPath, "v1", "spec.nodeName=node-1", func(ctx context.Context, client dynamic.Interface, wake func(), readings *metrics) {
		watchPods(ctx, client, "node-1", wake, readings)
	}},
}

// runWakeWatch starts one wake watch against the store, and answers the
// channel it wakes. The watch ends with the test.
func runWakeWatch(t *testing.T, store *objectStore, start func(context.Context, dynamic.Interface, func(), *metrics), readings *metrics) <-chan struct{} {
	t.Helper()
	wakes := make(chan struct{}, 1)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		stop()
		<-done
	})
	go func() {
		defer close(done)
		start(ctx, store.watcher(), func() {
			select {
			case wakes <- struct{}{}:
			default:
			}
		}, readings)
	}()
	return wakes
}

func awaitWake(t *testing.T, wakes <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-wakes:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s woke no pass within five seconds", what)
	}
}

func objectOf(kind, name string) map[string]any {
	return map[string]any{"metadata": map[string]any{"name": name, "namespace": "default"}, "kind": kind}
}

// The first read wakes the pass, even a read that finds nothing, and so
// does each change after it: a new object, an edit, and a removal. The
// watch asks for the objects this node's pass reads, and no others.
func TestAWakeWatchWakesOnItsFirstReadAndOnEveryChange(t *testing.T) {
	for _, watch := range wakeWatches {
		t.Run(watch.kind, func(t *testing.T) {
			store := newObjectStore(t, watch.collection, watch.apiVersion, watch.kind)
			wakes := runWakeWatch(t, store, watch.start, nil)
			awaitWake(t, wakes, "the first read")
			eventually(t, "the watch opening", func() bool { return store.watching() > 0 })

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
	}
}

// podAt is a pod with one label and one address, the way the kubelet
// reports it.
func podAt(uid, region, address string) map[string]any {
	return map[string]any{
		"kind":     "Pod",
		"metadata": map[string]any{"name": "player", "namespace": "default", "uid": uid, "labels": map[string]any{"region": region}},
		"status":   map[string]any{"podIP": address},
	}
}

// Through the reflector: the pod watch wakes the placement pass on its
// first read, a new pod, a change to a pod's labels, a pod deleted and
// created again, and a removal. The pass reads a pod's name,
// namespace, and labels, so the kubelet's status writes wake nothing.
func TestThePodWatchWakesOnALabelChangeAndNotOnAStatusWrite(t *testing.T) {
	store := newObjectStore(t, PodsPath, "v1", "Pod")
	wakes := runWakeWatch(t, store, func(ctx context.Context, client dynamic.Interface, wake func(), readings *metrics) {
		watchPods(ctx, client, "node-1", wake, readings)
	}, nil)
	awaitWake(t, wakes, "the first read")
	eventually(t, "the watch opening", func() bool { return store.watching() > 0 })

	store.put(podAt("uid-1", "left", "10.42.0.7"))
	awaitWake(t, wakes, "a new pod")
	store.put(podAt("uid-1", "left", "10.42.0.8"))
	select {
	case <-wakes:
		t.Fatal("a status write woke a pass")
	case <-time.After(300 * time.Millisecond):
	}
	store.put(podAt("uid-1", "right", "10.42.0.8"))
	awaitWake(t, wakes, "a label change")
	store.put(podAt("uid-2", "right", "10.42.0.8"))
	awaitWake(t, wakes, "a pod deleted and created again")
	store.remove("player")
	awaitWake(t, wakes, "a removal")
}

// displayAt is a Display at one generation, with a status that names
// the node it is on and the connector it is on.
func displayAt(generation int64, node, connector string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": "gsm-7716-lg-hdr-wqhd", "generation": generation, "uid": "uid-1"},
		"status":   map[string]any{"node": node, "connector": connector},
	}
}

// Through the reflector: the Display watch wakes the passes on its
// first read, a new Display, a spec edit, a Display another node's
// controller adopted, and a removal. Any other status write wakes
// nothing.
func TestTheDisplayWatchWakesOnAnEditAndNotOnAStatusWrite(t *testing.T) {
	store := newObjectStore(t, DisplaysPath, DisplayAPIVersion, "Display")
	wakes := runWakeWatch(t, store, watchDisplays, nil)
	awaitWake(t, wakes, "the first read")
	eventually(t, "the watch opening", func() bool { return store.watching() > 0 })

	store.put(displayAt(1, "node-1", "HDMI-A-1"))
	awaitWake(t, wakes, "a new Display")
	store.put(displayAt(1, "node-1", "HDMI-A-2"))
	select {
	case <-wakes:
		t.Fatal("a status write woke a pass")
	case <-time.After(300 * time.Millisecond):
	}
	store.put(displayAt(2, "node-1", "HDMI-A-2"))
	awaitWake(t, wakes, "a spec edit")
	store.put(displayAt(2, "node-2", "HDMI-A-2"))
	awaitWake(t, wakes, "an adoption")
	store.remove("gsm-7716-lg-hdr-wqhd")
	awaitWake(t, wakes, "a removal")
}

// The mark the Display watch compares. A change to the generation, the
// deletion mark, the UID, or status.node is an edit, and so is
// anything that is not an object, because nothing says what it
// changed.
func TestADisplayEditIsAChangeToItsMark(t *testing.T) {
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

// Each watch the reflector opens after its first counts one restart,
// so display_watch_restarts_total still shows a watch that the API
// server or the network keeps ending.
func TestAReopenedWakeWatchCountsARestart(t *testing.T) {
	store := newObjectStore(t, DisplaysPath, DisplayAPIVersion, "Display")
	readings := newMetrics(componentName, "dev")
	wakes := runWakeWatch(t, store, watchDisplays, readings)
	awaitWake(t, wakes, "the first read")
	eventually(t, "the watch opening", func() bool { return store.watching() > 0 })
	restarts := readings.watchRestarts.WithLabelValues(string(kindDisplay))
	if got := testutil.ToFloat64(restarts); got != 0 {
		t.Fatalf("the first watch counted %v restarts, want 0", got)
	}

	store.hangUp()

	eventually(t, "the restart", func() bool { return testutil.ToFloat64(restarts) == 1 })
}

// A watch that the API server refuses is a retry, not a restart, so a
// server that is down does not raise the count.
func TestARefusedWatchCountsNoRestart(t *testing.T) {
	readings := newMetrics(componentName, "dev")
	var requests atomic.Int64
	watcher := testWatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchDisplays(ctx, watcher, func() {}, readings)
	}()

	eventually(t, "a third refused request", func() bool { return requests.Load() >= 3 })
	stop()
	<-done

	if got := testutil.ToFloat64(readings.watchRestarts.WithLabelValues(string(kindDisplay))); got != 0 {
		t.Errorf("refused watches counted %v restarts, want 0", got)
	}
}
