package informer

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// undefined returns 404 to every request until defined is set, like the
// API server does for a kind that no CRD defines, and then passes each
// request to collection. plainWatches counts the watch requests it
// rejected that did not set sendInitialEvents.
type undefined struct {
	defined      atomic.Bool
	collection   http.Handler
	plainWatches atomic.Int64
}

func (u *undefined) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !u.defined.Load() {
		query := r.URL.Query()
		if query.Get("watch") == "true" && query.Get("sendInitialEvents") == "" {
			u.plainWatches.Add(1)
		}
		http.NotFound(w, r)
		return
	}
	u.collection.ServeHTTP(w, r)
}

func undefinedWatcher(t *testing.T, server *undefined) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(apiservertest.Start(t, server).Config())
	if err != nil {
		t.Fatalf("creating the dynamic client: %v", err)
	}
	return client
}

// While the CRD does not exist, the store is ready, empty, and Absent,
// and quietWatch sends no request to the API server. After the CRD is
// installed, the store has the collection's objects once the recheck
// time and the reflector's backoff have passed. quietWatch never counts
// as a reopened watch.
func TestAbsentCollectionIsEmptyUntilItsCRDIsInstalled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &undefined{collection: newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})}
		var reopened atomic.Int64
		c := Start(t.Context(), undefinedWatcher(t, server), Source{Resource: thingResource}, Options{
			Reopened: func() { reopened.Add(1) },
			Absent:   apierrors.IsNotFound,
		})
		synctest.Wait()
		if cached := CachedList[thing](c.View()); !c.View().Ready() || len(cached) != 0 {
			t.Errorf("the store of an absent collection contains %+v, ready: %v, want an empty store that is ready", cached, c.View().Ready())
		}
		if !c.View().Absent {
			t.Error("the store of an absent collection does not report Absent")
		}
		if server.plainWatches.Load() != 0 {
			t.Errorf("the informer sent %d plain watches for an absent collection", server.plainWatches.Load())
		}

		server.defined.Store(true)
		time.Sleep(defaultAbsentRecheck + time.Minute)
		synctest.Wait()
		if _, cached := Cached[thing](c.View(), "a"); !cached {
			t.Error("the store has no object a after the CRD was installed, want a")
		}
		if c.View().Absent {
			t.Error("the store reports Absent after the CRD was installed")
		}
		if reopened.Load() != 0 {
			t.Errorf("Reopened ran %d times, want 0", reopened.Load())
		}
	})
}

func TestA404IsAFailureWhenOptionsAbsentIsNil(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &undefined{collection: newWatchServer(thingsPath, [][]thing{{}})}
		c := Start(t.Context(), undefinedWatcher(t, server), Source{Resource: thingResource}, Options{})
		time.Sleep(time.Minute)
		synctest.Wait()

		if c.Synced() {
			t.Error("the store of a collection that returns 404 synced")
		}
	})
}

// A stopped watch leaves no quietWatch goroutine running.
func TestQuietWatchClosesWhenItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		w := quietWatch(ctx, time.Hour)
		cancel()
		synctest.Wait()

		select {
		case _, open := <-w.ResultChan():
			if open {
				t.Error("quietWatch sent an event after its context ended, want none")
			}
		default:
			t.Fatal("quietWatch is still open after its context ended, want it closed")
		}
	})
}
