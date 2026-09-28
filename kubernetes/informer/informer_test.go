package informer

// These tests run the copies and the wake handlers through client-go's
// real reflector, against a scripted API server. The reflector's own
// loop is upstream's to test. What these tests prove is that the copy
// answers reads, that each change the API server sends reaches the
// handlers, and that an object that does not convert is reported.

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

var testSource = Source{Resource: thingResource, LabelSelector: "app=test", FieldSelector: "metadata.name=a"}

func asObject(t *testing.T, item thing) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: fields}
}

// An object that does not convert to the operator's struct is an error
// that names the object. A tombstone, which the informer hands a
// handler for an object deleted while the watch was down, converts as
// the object it holds.
func TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt(t *testing.T) {
	good := newThing("a", "7", 2)
	mistyped := asObject(t, good)
	if err := unstructured.SetNestedField(mistyped.Object, "two", "spec", "size"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		object  any
		wantErr string
	}{
		{name: "an object", object: asObject(t, good)},
		{name: "a tombstone", object: cache.DeletedFinalStateUnknown{Key: "a", Obj: asObject(t, good)}},
		{name: "a field of the wrong type", object: mistyped, wantErr: "Thing a does not convert"},
		{name: "something that is not an object", object: "a", wantErr: "not an object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert[thing](c.object)
			if c.wantErr == "" && (err != nil || got.Metadata.Generation != 2) {
				t.Fatalf("Convert = %+v, %v; want generation 2 and no error", got.Metadata, err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("Convert error = %v, want one that says %q", err, c.wantErr)
			}
		})
	}
}

// A copy that never started cannot answer, so the pass reads the API
// server instead.
func TestACollectionThatNeverStartedCannotAnswer(t *testing.T) {
	var missing *Collection
	_, _, ok := Get[thing](missing, "a")
	_, listed := List[thing](missing)
	_, indexed := ByIndex[thing](missing, "app", "test")
	if missing.Synced() || ok || listed || indexed {
		t.Error("a nil collection answered a read")
	}
}

// Through the reflector: the synced copy answers a read by name, an
// absent name, a list, and a lookup by label, and it holds no
// managedFields.
func TestTheSyncedCopyAnswersReads(t *testing.T) {
	a, b := newThing("a", "7", 1), newThing("b", "8", 1)
	a.Metadata.Labels = map[string]string{"app": "one"}
	b.Metadata.Labels = map[string]string{"app": "two"}
	a.Metadata.ManagedFields = []any{map[string]any{"manager": "kubectl"}}
	server := newWatchServer([][]thing{{a, b}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := Start(ctx, testClient(t, server), testSource, Options{Indexers: cache.Indexers{"app": LabelIndex("app")}})
	awaitSynced(t, c)

	got, found, ok := Get[thing](c, "a")
	if !ok || !found || got.Metadata.Name != "a" || got.Metadata.ManagedFields != nil {
		t.Errorf("Get(a) = %+v, found %v, ok %v; want a with no managedFields", got, found, ok)
	}
	if _, found, ok := Get[thing](c, "c"); !ok || found {
		t.Errorf("Get(c) found %v, ok %v; want an answer that c is absent", found, ok)
	}
	if all, ok := List[thing](c); !ok || len(all) != 2 {
		t.Errorf("List = %d objects, ok %v; want 2", len(all), ok)
	}
	if two, ok := ByIndex[thing](c, "app", "two"); !ok || len(two) != 1 || two[0].Metadata.Name != "b" {
		t.Errorf("ByIndex(app=two) = %+v, ok %v; want b", two, ok)
	}
}

// Through the reflector: the list and every watch carry the source's
// selectors, so the API server sends only the objects the operator
// reads.
func TestEveryRequestCarriesTheSelectors(t *testing.T) {
	server := newWatchServer([][]thing{{}}, []string{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	Start(ctx, testClient(t, server), testSource, Options{})
	server.awaitWatches(t, 2)

	want := "labelSelector=app=test fieldSelector=metadata.name=a"
	sent := server.sent()
	if len(sent) < 2 || slices.ContainsFunc(sent, func(q string) bool { return q != want }) {
		t.Errorf("queries = %q, want each to be %q", sent, want)
	}
}

// Through the reflector: a watch that the API server ends is opened
// again, and each open after the first counts as a restart. The first
// watch is the streaming list.
func TestAWatchOpenedAgainCountsAsARestart(t *testing.T) {
	a := newThing("a", "7", 1)
	server := newWatchServer([][]thing{{a}}, []string{event("MODIFIED", newThing("a", "150", 1))})
	var reopened atomic.Int64
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	Start(ctx, testClient(t, server), testSource, Options{Reopened: func() { reopened.Add(1) }})
	server.awaitWatches(t, 2)

	// The count moves when the client has the server's answer, a
	// moment after the server sent it.
	deadline := time.Now().Add(5 * time.Second)
	for reopened.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := reopened.Load(); got != 1 {
		t.Errorf("restarts = %d after two watches, want 1", got)
	}
}

// Through the reflector: Synced runs once when the first read is done,
// even when the collection is empty.
func TestSyncedRunsOnceAfterTheFirstRead(t *testing.T) {
	server := newWatchServer([][]thing{{}})
	var synced atomic.Int64
	ctx, cancel := context.WithCancel(t.Context())
	c := Start(ctx, testClient(t, server), testSource, Options{Synced: func() { synced.Add(1) }})
	awaitSynced(t, c)
	cancel()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the watch never stopped")
	}
	if got := synced.Load(); got != 1 {
		t.Errorf("Synced ran %d times, want 1", got)
	}
}

// The two wake rules, through the reflector. WakeOnEdit ignores a
// status write and wakes for a spec edit, a deletion mark, a new
// object, and a removed object. WakeOnChange wakes for the status
// write too.
func TestEachHandlerWakesTheLoopForItsChanges(t *testing.T) {
	statusWrite := newThing("a", "110", 1)
	statusWrite.Status.Phase = "Ready"
	specEdit := newThing("a", "120", 2)
	deleting := newThing("a", "130", 2)
	deleting.Metadata.DeletionTimestamp = "2026-09-27T12:00:00Z"
	cases := []struct {
		name    string
		handler func(wake func()) cache.ResourceEventHandler
		line    string
		wakes   bool
	}{
		{"an edit handler, a status write", editHandler, event("MODIFIED", statusWrite), false},
		{"an edit handler, a spec edit", editHandler, event("MODIFIED", specEdit), true},
		{"an edit handler, a deletion mark", editHandler, event("MODIFIED", deleting), true},
		{"an edit handler, a new object", editHandler, event("ADDED", newThing("b", "140", 1)), true},
		{"an edit handler, a removed object", editHandler, event("DELETED", newThing("a", "150", 1)), true},
		{"a change handler, a status write", changeHandler, event("MODIFIED", statusWrite), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newWatchServer([][]thing{{newThing("a", "7", 1)}}, []string{pause, c.line, holdOpen})
			wakes := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			Start(ctx, testClient(t, server), testSource, Options{Handler: c.handler(Signal(wakes))})
			server.awaitWatches(t, 1)
			settleWakes(wakes)

			server.release()

			if woke := wokeWithin(wakes, time.Second); woke != c.wakes {
				t.Errorf("woke = %v, want %v", woke, c.wakes)
			}
		})
	}
}

func editHandler(wake func()) cache.ResourceEventHandler { return WakeOnEdit[thing](testSource, wake) }
func changeHandler(wake func()) cache.ResourceEventHandler {
	return WakeOnChange[thing](testSource, wake)
}

// After a gap in the watch, the informer reads the collection again
// and reports the difference. An update whose resourceVersion did not
// move is no change, and a change handler does not wake for it.
func TestARereadWithNoChangeDoesNotWake(t *testing.T) {
	a := newThing("a", "7", 1)
	h := WakeOnChange[thing](testSource, func() { t.Error("an unchanged object woke the loop") })
	h.OnUpdate(asObject(t, a), asObject(t, a))
}

// A removal wakes the loop even when its tombstone holds no object,
// because nothing says what was removed.
func TestARemovalWithNoCopyWakes(t *testing.T) {
	woke := false
	h := WakeOnEdit[thing](testSource, func() { woke = true })
	h.OnDelete(cache.DeletedFinalStateUnknown{Key: "a"})
	if !woke {
		t.Error("a removal with no copy did not wake the loop")
	}
}

// An object that does not convert does not wake the loop, because the
// pass cannot use it either. The handler logs it.
func TestAnObjectThatDoesNotConvertDoesNotWake(t *testing.T) {
	mistyped := asObject(t, newThing("a", "7", 1))
	if err := unstructured.SetNestedField(mistyped.Object, "two", "spec", "size"); err != nil {
		t.Fatal(err)
	}
	h := WakeOnChange[thing](testSource, func() { t.Error("an object that does not convert woke the loop") })
	h.OnAdd(mistyped, false)
}

// A copy whose watch the API server refuses cannot answer, even after
// the reflector lists the collection, because nothing keeps the list
// current between the reflector's retries.
func TestACopyWhoseWatchIsRefusedCannotAnswer(t *testing.T) {
	listed := make(chan struct{}, 8)
	lists := newWatchServer([][]thing{{newThing("a", "7", 1)}})
	refusing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`))
			return
		}
		lists.ServeHTTP(w, r)
		listed <- struct{}{}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := Start(ctx, testClient(t, refusing), testSource, Options{})
	select {
	case <-listed:
	case <-time.After(5 * time.Second):
		t.Fatal("the reflector never listed the collection")
	}
	awaitSynced(t, c)

	if _, _, ok := Get[thing](c, "a"); ok || c.Synced() {
		t.Error("a copy with no accepted watch answered a read")
	}
}
