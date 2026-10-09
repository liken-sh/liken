package informer

// These tests run the copy through client-go's real reflector, against
// a scripted API server. The reflector's own loop is upstream's to
// test. What these tests prove is that the copy holds the collection,
// that each change the API server sends reaches the handler, and that
// the copy stops answering only while the API server forbids its watch.
//
// Each test that runs a watch runs in a synctest bubble. synctest.Wait
// returns once the reflector has done all it can do at the present
// moment, and a time.Sleep waits out its backoff on the fake clock.

import (
	"context"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/memo"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	synctest.Test(t, func(t *testing.T) {
		a, b := newThing("a", "7", 1), newThing("b", "8", 1)
		a.Metadata.ManagedFields = []any{map[string]any{"manager": "kubectl"}}
		server := newWatchServer(thingsPath, [][]thing{{a, b}})
		var synced, added atomic.Int64
		ctx, cancel := context.WithCancel(t.Context())
		c := Start(ctx, testWatcher(t, server), Source{Resource: thingResource}, Options{
			Handler: cache.ResourceEventHandlerFuncs{AddFunc: func(any) { added.Add(1) }},
			Synced:  func() { synced.Add(1) },
		})
		synctest.Wait()

		held := CachedList[thing](c.View())
		if !c.Synced() || len(held) != 2 || held[0].Metadata.Name != "a" || held[0].Metadata.ManagedFields != nil {
			t.Errorf("the copy holds %+v, synced: %v; want a with no managedFields, then b", held, c.Synced())
		}
		cancel()
		synctest.Wait()
		select {
		case <-c.Done():
		default:
			t.Fatal("the watch never stopped")
		}
		if synced.Load() != 1 || added.Load() != 2 {
			t.Errorf("Synced ran %d times and the handler took %d objects, want 1 and 2", synced.Load(), added.Load())
		}
	})
}

// A watch with no handler keeps a copy for the pass to read.
func TestAWatchWithNoHandlerKeepsACopy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})
		c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{})
		synctest.Wait()

		if _, ok := Cached[thing](c.View(), "a"); !ok {
			t.Error("the copy does not hold a")
		}
	})
}

// The list and every watch carry the source's namespace and selectors,
// so the API server sends only the objects the operator reads.
func TestEveryRequestCarriesTheSelection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer("/apis/test.liken.sh/v1/namespaces/liken-system/things", [][]thing{{}}, []string{})
		source := Source{Resource: thingResource, Namespace: "liken-system", LabelSelector: "app=test", FieldSelector: "metadata.name=a"}
		Start(t.Context(), testWatcher(t, server), source, Options{})
		time.Sleep(time.Minute)
		synctest.Wait()

		want := "labelSelector=app=test fieldSelector=metadata.name=a"
		sent := server.sent()
		if len(sent) < 2 || slices.ContainsFunc(sent, func(q string) bool { return q != want }) {
			t.Errorf("queries = %q, want each to be %q", sent, want)
		}
	})
}

// A watch that the API server ends is opened again, and each open after
// the first counts. A change on the stream reaches the handler.
func TestAWatchOpenedAgainCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		changed := newThing("a", "150", 2)
		server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}}, []string{event("MODIFIED", changed)})
		var reopened, updated, recovered atomic.Int64
		Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{
			Handler:   cache.ResourceEventHandlerFuncs{UpdateFunc: func(any, any) { updated.Add(1) }},
			Reopened:  func() { reopened.Add(1) },
			Recovered: func() { recovered.Add(1) },
		})
		time.Sleep(time.Minute)
		synctest.Wait()

		if reopened.Load() != 1 || updated.Load() != 1 || recovered.Load() != 0 {
			t.Errorf("the watch counted %d reopened watches, %d recoveries, and the handler took %d updates, want 1, 0, and 1",
				reopened.Load(), recovered.Load(), updated.Load())
		}
	})
}

// A watch that the API server accepts after an outage is a recovery,
// and the outage's failed attempts count once, not once each.
func TestAWatchAcceptedAfterAnOutageRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}}, []string{})
		var recovered atomic.Int64
		Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{
			Recovered: func() { recovered.Add(1) },
		})
		synctest.Wait()

		server.failing(http.StatusServiceUnavailable)
		time.Sleep(time.Minute)
		synctest.Wait()
		during := recovered.Load()
		server.failing(0)
		time.Sleep(time.Minute)
		synctest.Wait()

		if server.failed() < 2 || during != 0 || recovered.Load() != 1 {
			t.Errorf("%d failed watches, %d recoveries during the outage and %d after, want at least 2, 0, and 1",
				server.failed(), during, recovered.Load())
		}
	})
}

// A copy whose watch the API server forbids holds a list, and still
// answers nothing, because no watch keeps the list current. When the
// API server accepts the watch again, the copy answers.
func TestACopyWithAForbiddenWatchDoesNotAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})
		client := testWatcher(t, server)
		server.failing(http.StatusForbidden)
		c := Start(t.Context(), client, Source{Resource: thingResource}, Options{})
		synctest.Wait()

		if _, held := Cached[thing](c.View(), "a"); !c.controller.HasSynced() || c.Synced() || c.View().Ready() || held {
			t.Error("the copy answers, or holds no list, while the API server forbids its watch")
		}
		server.failing(0)
		time.Sleep(time.Minute)
		synctest.Wait()
		if !c.Synced() {
			t.Error("the copy does not answer a minute after the API server accepts its watch")
		}
	})
}

// Only a refusal of the permission to watch stops a synced copy from
// answering. A watch that fails because the API server is down or busy
// says nothing about the copy's permission, and the operator keeps its
// local work going from the copy until the API server returns.
func TestOnlyARefusedPermissionStopsASyncedCopy(t *testing.T) {
	cases := []struct {
		name    string
		failure int
		answers bool
	}{
		{"the API server refuses the connection", serverDown, true},
		{"the API server is unavailable", http.StatusServiceUnavailable, true},
		{"the watch is forbidden", http.StatusForbidden, false},
		{"the credentials are refused", http.StatusUnauthorized, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}}, []string{})
				c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{})
				synctest.Wait()
				server.failing(tc.failure)
				time.Sleep(time.Minute)
				synctest.Wait()

				if server.failed() == 0 || c.Synced() != tc.answers {
					t.Errorf("Synced() = %v after %d failures, want %v after a failed watch", c.Synced(), server.failed(), tc.answers)
				}
			})
		})
	}
}

// A copy whose Options.UnreadyOnWatchError is set stops answering after
// any failed watch, and a read of it goes to the API server until the
// API server accepts a watch again. The same failures leave a copy
// without the option answering. An operator sets it when a copy that
// missed changes, such as the changes made while its reflector waited
// out a backoff after an API server restart, must never decide a pass.
func TestAStrictCopyStopsAnsweringAfterAnyFailedWatch(t *testing.T) {
	cases := []struct {
		name    string
		failure int
		strict  bool
		answers bool
	}{
		{"a strict copy, the API server refuses the connection", serverDown, true, false},
		{"a strict copy, the API server is unavailable", http.StatusServiceUnavailable, true, false},
		{"a strict copy, the watch is forbidden", http.StatusForbidden, true, false},
		{"a copy, the API server refuses the connection", serverDown, false, true},
		{"a copy, the API server is unavailable", http.StatusServiceUnavailable, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}}, []string{})
				c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{UnreadyOnWatchError: tc.strict})
				synctest.Wait()
				server.failing(tc.failure)
				time.Sleep(time.Minute)
				synctest.Wait()
				api := newFakeAPI(newThing("a", "9", 1))

				held := Held{View: c.View(), Versions: memo.New()}
				got, err := ReadOne[thing](testClient(t, api), held, "a", thingPath("a"))

				if err != nil || server.failed() == 0 || c.Synced() != tc.answers {
					t.Fatalf("Synced() = %v, err = %v after %d failures; want %v and no error after a failed watch",
						c.Synced(), err, server.failed(), tc.answers)
				}
				fromServer := got.Metadata.ResourceVersion == "9" && len(api.sent()) == 1
				if fromServer == tc.answers {
					t.Errorf("the read answered version %s; want the store's copy %v", got.Metadata.ResourceVersion, tc.answers)
				}
			})
		})
	}
}

// An operator's transform trims each object before the copy holds it,
// after the managedFields are gone.
func TestTheCopyHoldsWhatTheTransformLeaves(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := newThing("a", "7", 1)
		a.Spec.Size = 3
		a.Metadata.ManagedFields = []any{map[string]any{"manager": "kubectl"}}
		server := newWatchServer(thingsPath, [][]thing{{a}})
		c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{
			Transform: func(item *unstructured.Unstructured) { unstructured.RemoveNestedField(item.Object, "spec", "size") },
		})
		synctest.Wait()

		held, ok := Cached[thing](c.View(), "a")
		if !ok || held.Spec.Size != 0 || held.Metadata.ManagedFields != nil {
			t.Errorf("the copy holds %+v, want a with no size and no managedFields", held)
		}
	})
}

// A list that fails is reported to the operator, which can stop waiting
// for a first read that does not come.
func TestAFailedListIsReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failures := make(chan error, 16)
		Start(t.Context(), undefinedWatcher(t, &undefined{}), Source{Resource: thingResource}, Options{
			ListFailed: func(err error) { failures <- err },
		})
		synctest.Wait()

		select {
		case err := <-failures:
			if !apierrors.IsNotFound(err) {
				t.Errorf("the failed list reported %v, want the 404", err)
			}
		default:
			t.Fatal("no failed list was reported")
		}
	})
}

// A watch with indexers keeps each index in its store, so a pass reads
// the objects of one value without a scan of the whole copy.
func TestAWatchKeepsTheIndexesItNames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		small, large := newThing("a", "7", 1), newThing("b", "8", 1)
		small.Spec.Size, large.Spec.Size = 1, 9
		server := newWatchServer(thingsPath, [][]thing{{small, large}})
		bySize := func(object any) ([]string, error) {
			item, err := Convert[thing](object)
			if err != nil {
				return nil, err
			}
			if item.Spec.Size > 5 {
				return []string{"large"}, nil
			}
			return []string{"small"}, nil
		}
		c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{
			Indexers: cache.Indexers{"size": bySize},
		})
		synctest.Wait()

		indexer, ok := c.View().Store.(cache.Indexer)
		if !ok {
			t.Fatal("the store of a watch with indexers is not a cache.Indexer")
		}
		held, err := indexer.ByIndex("size", "large")
		if err != nil || len(held) != 1 {
			t.Fatalf("ByIndex = %d objects, %v; want b alone", len(held), err)
		}
		if item, _ := Convert[thing](held[0]); item.Metadata.Name != "b" {
			t.Errorf("the large index holds %q, want b", item.Metadata.Name)
		}
	})
}
