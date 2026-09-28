package main

// These tests run the handlers of each watch through client-go's real
// reflector, against the scripted API server in watchserver_test.go.
// The reflector's own loop is upstream's to test. What these tests
// prove is that each change the API server sends reaches this
// program's handlers, that each watch asks for the objects it must,
// and that an object that does not convert is reported.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

func configMapNamed(name, value string) ConfigMap {
	return ConfigMap{Metadata: objectMeta{Name: name, Namespace: "test"}, Data: map[string]string{"value": value}}
}

// What a watch on one ConfigMap has seen, in order: each value, and
// "gone" for an object that does not exist.
type seenValues struct {
	mu     sync.Mutex
	values []string
}

func (s *seenValues) see(held *ConfigMap) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if held == nil {
		s.values = append(s.values, "gone")
		return
	}
	s.values = append(s.values, held.Data["value"])
}

func (s *seenValues) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.values) == 0 {
		return ""
	}
	return s.values[len(s.values)-1]
}

func watchOneConfigMap(t *testing.T, objects *objectStore) *seenValues {
	t.Helper()
	seen := &seenValues{}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		stop()
		<-done
	})
	go func() {
		defer close(done)
		watchNamed(ctx, objects.watcher(), configMapResource, "test", "tracked", "the test ConfigMap", seen.see)
	}()
	eventually(t, "the watch opening", func() bool { return objects.watching() > 0 })
	return seen
}

// The first read answers the object as it stands, and each change
// after it arrives on the watch: an update, a removal, and a new
// object. The watch asks for the one object by name, which is how RBAC
// matches it against a rule's resourceNames.
func TestTheWatchSeesEachChangeToTheObject(t *testing.T) {
	objects := newConfigMapStore(t, "test")
	objects.put(configMapNamed("tracked", "first"))
	objects.put(configMapNamed("other", "ignored"))
	seen := watchOneConfigMap(t, objects)
	eventually(t, "the first read", func() bool { return seen.last() == "first" })

	objects.put(configMapNamed("tracked", "second"))
	eventually(t, "the update", func() bool { return seen.last() == "second" })
	objects.remove("tracked")
	eventually(t, "the removal", func() bool { return seen.last() == "gone" })
	objects.put(configMapNamed("tracked", "third"))
	eventually(t, "the new object", func() bool { return seen.last() == "third" })

	for _, query := range objects.asked() {
		if got := query.Get("fieldSelector"); got != "metadata.name=tracked" {
			t.Errorf("a request selected %q, want metadata.name=tracked", got)
		}
	}
}

// An object that does not exist at the first read is seen as gone.
func TestTheWatchSeesAnAbsentObjectAsGone(t *testing.T) {
	objects := newConfigMapStore(t, "test")
	seen := watchOneConfigMap(t, objects)

	eventually(t, "the first read", func() bool { return seen.last() == "gone" })
}

// An object whose fields do not fit this program's struct is an error
// that names it, and so is a tombstone that holds one. A tombstone
// that holds no copy at all is an error too, and names its key.
func TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt(t *testing.T) {
	mistyped := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "tracked", "namespace": "test"},
		"data":       int64(5),
	}}
	cases := []struct {
		name   string
		object any
		want   string
	}{
		{name: "an object", object: mistyped, want: "ConfigMap test/tracked does not convert"},
		{name: "a tombstone", object: cache.DeletedFinalStateUnknown{Key: "test/tracked", Obj: mistyped}, want: "ConfigMap test/tracked does not convert"},
		{name: "an empty tombstone", object: cache.DeletedFinalStateUnknown{Key: "test/tracked"}, want: "the tombstone for test/tracked holds no copy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := convert[ConfigMap](c.object)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("convert answered %v, want an error that says %q", err, c.want)
			}
		})
	}
}

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
			watch := openDisplays(idleWatcher(t), func() { woke = true }, nil)
			watch.scope.handler.OnUpdate(object(1, "uid-1", false), c.after)
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
