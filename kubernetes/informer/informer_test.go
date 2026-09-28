package informer

// These tests run the copy through client-go's real reflector, against
// a scripted API server. The reflector's own loop is upstream's to
// test. What these tests prove is that the copy holds the collection,
// that each change the API server sends reaches the handler, and that
// the copy answers only while its watch runs.

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/tools/cache"
)

const thingsPath = "/apis/test.liken.sh/v1/things"

// A pod with no in-cluster environment gets no client for its watches.
func TestInClusterNeedsThePodsEnvironment(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if _, err := InCluster(); err == nil {
		t.Error("InCluster built a client with no environment")
	}
}

// A source names itself in a log line with its namespace and its
// selectors.
func TestASourceNamesItsSelection(t *testing.T) {
	cases := []struct {
		source Source
		want   string
	}{
		{Source{Resource: thingResource}, "things"},
		{Source{Resource: thingResource, Namespace: "liken-system", LabelSelector: "app=a", FieldSelector: "status.node=node-1"},
			"liken-system/things (status.node=node-1) [app=a]"},
	}
	for _, c := range cases {
		if got := c.source.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

// A copy that never started cannot answer, so the pass reads the API
// server instead.
func TestACollectionThatNeverStartedCannotAnswer(t *testing.T) {
	var missing *Collection
	if missing.Synced() || missing.View().Ready() {
		t.Error("a nil collection answered")
	}
}

// The synced copy holds each object of the first read with no
// managedFields, runs Synced once, and hands the handler each object.
// Done closes after the watch stops.
func TestTheSyncedCopyHoldsTheFirstRead(t *testing.T) {
	a, b := newThing("a", "7", 1), newThing("b", "8", 1)
	a.Metadata.ManagedFields = []any{map[string]any{"manager": "kubectl"}}
	server := newWatchServer(thingsPath, [][]thing{{a, b}})
	var synced, added atomic.Int64
	ctx, cancel := context.WithCancel(t.Context())
	c := Start(ctx, testWatcher(t, server), Source{Resource: thingResource}, Options{
		Handler: cache.ResourceEventHandlerFuncs{AddFunc: func(any) { added.Add(1) }},
		Synced:  func() { synced.Add(1) },
	})
	eventually(t, "the copy syncs", c.Synced)

	held := CachedList[thing](c.View())
	if len(held) != 2 || held[0].Metadata.Name != "a" || held[0].Metadata.ManagedFields != nil {
		t.Errorf("the copy holds %+v, want a with no managedFields, then b", held)
	}
	cancel()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the watch never stopped")
	}
	if synced.Load() != 1 || added.Load() != 2 {
		t.Errorf("Synced ran %d times and the handler took %d objects, want 1 and 2", synced.Load(), added.Load())
	}
}

// A watch with no handler keeps a copy for the pass to read.
func TestAWatchWithNoHandlerKeepsACopy(t *testing.T) {
	server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})
	c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{})
	eventually(t, "the copy syncs", c.View().Ready)

	if _, ok := Cached[thing](c.View(), "a"); !ok {
		t.Error("the copy does not hold a")
	}
}

// The list and every watch carry the source's namespace and selectors,
// so the API server sends only the objects the operator reads.
func TestEveryRequestCarriesTheSelection(t *testing.T) {
	server := newWatchServer("/apis/test.liken.sh/v1/namespaces/liken-system/things", [][]thing{{}}, []string{})
	source := Source{Resource: thingResource, Namespace: "liken-system", LabelSelector: "app=test", FieldSelector: "metadata.name=a"}
	Start(t.Context(), testWatcher(t, server), source, Options{})
	server.awaitWatches(t, 2)

	want := "labelSelector=app=test fieldSelector=metadata.name=a"
	sent := server.sent()
	if len(sent) < 2 || slices.ContainsFunc(sent, func(q string) bool { return q != want }) {
		t.Errorf("queries = %q, want each to be %q", sent, want)
	}
}

// A watch that the API server ends is opened again, and each open after
// the first counts. A change on the stream reaches the handler.
func TestAWatchOpenedAgainCounts(t *testing.T) {
	changed := newThing("a", "150", 2)
	server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}}, []string{event("MODIFIED", changed)})
	var reopened, updated atomic.Int64
	Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{
		Handler:  cache.ResourceEventHandlerFuncs{UpdateFunc: func(any, any) { updated.Add(1) }},
		Reopened: func() { reopened.Add(1) },
	})
	server.awaitWatches(t, 2)

	eventually(t, "the second watch counts", func() bool { return reopened.Load() == 1 })
	if updated.Load() != 1 {
		t.Errorf("the handler took %d updates, want 1", updated.Load())
	}
}

// A copy whose watch the API server refuses holds a list, and still
// answers nothing, because no watch keeps the list current. When the
// API server accepts the watch again, the copy answers.
func TestACopyWithARefusedWatchDoesNotAnswer(t *testing.T) {
	server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})
	server.refusing(true)
	c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{})
	eventually(t, "the copy holds the list", c.controller.HasSynced)

	if c.Synced() || c.View().Ready() {
		t.Error("the copy answers while the API server refuses its watch")
	}
	server.refusing(false)
	eventually(t, "the copy answers once the watch runs", c.Synced)
}
