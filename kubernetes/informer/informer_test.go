package informer

// These tests run client-go's real reflector against a scripted API
// server. Upstream tests the reflector's own loop, so these tests check
// what this package adds: what the store stores, which callbacks run,
// and when Synced is false.
//
// Each test that runs a watch runs in a synctest bubble. synctest.Wait
// returns when the reflector has nothing left to do at the current fake
// time, and time.Sleep waits out a backoff on the fake clock.

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

func TestInClusterFailsWithoutThePodsEnvironment(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if _, err := InCluster(); err == nil {
		t.Error("InCluster returned no error with KUBERNETES_SERVICE_HOST empty, want one")
	}
}

func TestASourceStringIncludesItsSelection(t *testing.T) {
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

func TestANilCollectionIsNeverSynced(t *testing.T) {
	var missing *Collection
	if missing.Synced() || missing.View().Ready() {
		t.Error("a nil Collection reports Synced or Ready, want neither")
	}
}

// The store stores each object of the first read without managedFields,
// calls Synced once, and calls the handler for each object. Done closes
// after the watch stops.
func TestASyncedCollectionStoresTheFirstRead(t *testing.T) {
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

		cached := CachedList[thing](c.View())
		if !c.Synced() || len(cached) != 2 || cached[0].Metadata.Name != "a" || cached[0].Metadata.ManagedFields != nil {
			t.Errorf("the store contains %+v, synced: %v; want a with no managedFields, then b", cached, c.Synced())
		}
		cancel()
		synctest.Wait()
		select {
		case <-c.Done():
		default:
			t.Fatal("the watch never stopped")
		}
		if synced.Load() != 1 || added.Load() != 2 {
			t.Errorf("Synced ran %d times and the handler received %d objects, want 1 and 2", synced.Load(), added.Load())
		}
	})
}

func TestAWatchWithNoHandlerStillFillsTheStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})
		c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{})
		synctest.Wait()

		if _, ok := Cached[thing](c.View(), "a"); !ok {
			t.Error("the store has no object a, want one")
		}
	})
}

func TestEveryRequestIncludesTheSelection(t *testing.T) {
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

// When the API server ends a watch, the reflector opens it again, and
// each open after the first calls Reopened but not Recovered.
func TestAReopenedWatchCallsReopened(t *testing.T) {
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
			t.Errorf("the watch counted %d reopened watches, %d recoveries, and the handler received %d updates, want 1, 0, and 1",
				reopened.Load(), recovered.Load(), updated.Load())
		}
	})
}

func TestAWatchAcceptedAfterAnOutageCallsRecoveredOnce(t *testing.T) {
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

// A store whose watch the API server forbids stores the list but is not
// Synced, because no watch keeps the list current.
func TestACollectionWithAForbiddenWatchIsNotSynced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})
		client := testWatcher(t, server)
		server.failing(http.StatusForbidden)
		c := Start(t.Context(), client, Source{Resource: thingResource}, Options{})
		synctest.Wait()

		if _, cached := Cached[thing](c.View(), "a"); !c.controller.HasSynced() || c.Synced() || c.View().Ready() || cached {
			t.Error("while the API server forbids the watch, Synced or Ready is true or the store has a, want none of them")
		}
		server.failing(0)
		time.Sleep(time.Minute)
		synctest.Wait()
		if !c.Synced() {
			t.Error("Synced is false a minute after the API server accepts the watch, want true")
		}
	})
}

// A watch that fails because the API server is down or busy does not
// show whether the watch is permitted, so only a 401 or a 403 makes the
// store not Synced.
func TestOnlyARefusedPermissionMakesASyncedCollectionNotSynced(t *testing.T) {
	cases := []struct {
		name       string
		failure    int
		wantSynced bool
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

				if server.failed() == 0 || c.Synced() != tc.wantSynced {
					t.Errorf("Synced() = %v after %d failures, want %v after a failed watch", c.Synced(), server.failed(), tc.wantSynced)
				}
			})
		})
	}
}

// A collection with Options.UnreadyOnWatchError set is not Synced after
// any failed watch, so ReadOne reads the API server. The same failures
// leave a collection without the option Synced, and ReadOne reads the
// store.
func TestACollectionWithUnreadyOnWatchErrorIsNotSyncedAfterAnyFailedWatch(t *testing.T) {
	cases := []struct {
		name                string
		failure             int
		unreadyOnWatchError bool
		wantSynced          bool
	}{
		{"UnreadyOnWatchError set, the API server refuses the connection", serverDown, true, false},
		{"UnreadyOnWatchError set, the API server is unavailable", http.StatusServiceUnavailable, true, false},
		{"UnreadyOnWatchError set, the watch is forbidden", http.StatusForbidden, true, false},
		{"UnreadyOnWatchError unset, the API server refuses the connection", serverDown, false, true},
		{"UnreadyOnWatchError unset, the API server is unavailable", http.StatusServiceUnavailable, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}}, []string{})
				c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{UnreadyOnWatchError: tc.unreadyOnWatchError})
				synctest.Wait()
				server.failing(tc.failure)
				time.Sleep(time.Minute)
				synctest.Wait()
				api := newFakeAPI(newThing("a", "9", 1))

				watched := Held{View: c.View(), Versions: memo.New()}
				got, err := ReadOne[thing](testClient(t, api), watched, "a", thingPath("a"))

				if err != nil || server.failed() == 0 || c.Synced() != tc.wantSynced {
					t.Fatalf("Synced() = %v, err = %v after %d failures; want %v and no error after a failed watch",
						c.Synced(), err, server.failed(), tc.wantSynced)
				}
				fromServer := got.Metadata.ResourceVersion == "9" && len(api.sent()) == 1
				if fromServer == tc.wantSynced {
					t.Errorf("the read returned version %s, want the store's copy %v", got.Metadata.ResourceVersion, tc.wantSynced)
				}
			})
		})
	}
}

func TestTheStoreKeepsWhatTheTransformLeaves(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := newThing("a", "7", 1)
		a.Spec.Size = 3
		a.Metadata.ManagedFields = []any{map[string]any{"manager": "kubectl"}}
		server := newWatchServer(thingsPath, [][]thing{{a}})
		c := Start(t.Context(), testWatcher(t, server), Source{Resource: thingResource}, Options{
			Transform: func(item *unstructured.Unstructured) { unstructured.RemoveNestedField(item.Object, "spec", "size") },
		})
		synctest.Wait()

		cached, ok := Cached[thing](c.View(), "a")
		if !ok || cached.Spec.Size != 0 || cached.Metadata.ManagedFields != nil {
			t.Errorf("the store contains %+v, want a with no size and no managedFields", cached)
		}
	})
}

func TestAFailedListCallsListFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failures := make(chan error, 16)
		Start(t.Context(), undefinedWatcher(t, &undefined{}), Source{Resource: thingResource}, Options{
			ListFailed: func(err error) { failures <- err },
		})
		synctest.Wait()

		select {
		case err := <-failures:
			if !apierrors.IsNotFound(err) {
				t.Errorf("ListFailed received %v, want the 404", err)
			}
		default:
			t.Fatal("ListFailed was not called, want a call with the 404")
		}
	})
}

func TestAWatchMaintainsItsIndexes(t *testing.T) {
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
			t.Fatal("the store of a watch with indexers is not a cache.Indexer, want one")
		}
		cached, err := indexer.ByIndex("size", "large")
		if err != nil || len(cached) != 1 {
			t.Fatalf("ByIndex = %d objects, %v; want b alone", len(cached), err)
		}
		if item, _ := Convert[thing](cached[0]); item.Metadata.Name != "b" {
			t.Errorf("the large index contains %q, want b", item.Metadata.Name)
		}
	})
}
