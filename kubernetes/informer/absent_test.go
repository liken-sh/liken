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

// undefined answers 404 to every request until the collection's
// definition arrives, the way the API server answers for a kind it
// does not serve, and then hands each request to the collection.
// plainWatches counts the watches it refused that ask for no initial
// events.
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

// undefinedWatcher points a dynamic client at an undefined collection.
func undefinedWatcher(t *testing.T, server *undefined) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(apiservertest.Start(t, server).Config())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// A collection whose definition is absent reads as an empty copy that
// answers a pass. The quiet stream asks the API server nothing, and
// counts as no watch opened again. When the definition arrives, the
// copy holds its objects after the next recheck and the reflector's
// backoff.
func TestAnAbsentCollectionReadsAsEmptyUntilItArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &undefined{collection: newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})}
		var reopened atomic.Int64
		c := Start(t.Context(), undefinedWatcher(t, server), Source{Resource: thingResource}, Options{
			Reopened: func() { reopened.Add(1) },
			Absent:   apierrors.IsNotFound,
		})
		synctest.Wait()
		if held := CachedList[thing](c.View()); !c.View().Ready() || len(held) != 0 {
			t.Errorf("the copy of an absent collection holds %+v, ready: %v; want an empty copy that answers", held, c.View().Ready())
		}
		if server.plainWatches.Load() != 0 {
			t.Errorf("the watch of an absent collection sent %d plain watches", server.plainWatches.Load())
		}

		server.defined.Store(true)
		time.Sleep(defaultAbsentRecheck + time.Minute)
		synctest.Wait()
		if _, held := Cached[thing](c.View(), "a"); !held {
			t.Error("the copy does not hold the collection that arrived")
		}
		if reopened.Load() != 0 {
			t.Errorf("the watch counted %d reopened watches, want none", reopened.Load())
		}
	})
}

// With no Absent, a 404 is a failure like any other, and the copy never
// syncs.
func TestAnAbsentCollectionIsAFailureWhenTheOptionIsUnset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &undefined{collection: newWatchServer(thingsPath, [][]thing{{}})}
		c := Start(t.Context(), undefinedWatcher(t, server), Source{Resource: thingResource}, Options{})
		time.Sleep(time.Minute)
		synctest.Wait()

		if c.Synced() {
			t.Error("a collection the API server does not serve synced")
		}
	})
}

// The quiet stream ends when the watch's context ends, so a watch that
// stops leaves no goroutine behind.
func TestTheQuietStreamEndsWithTheWatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		stream := quietWatch(ctx, time.Hour)
		cancel()
		synctest.Wait()

		select {
		case _, open := <-stream.ResultChan():
			if open {
				t.Error("the quiet stream sent an event")
			}
		default:
			t.Fatal("the quiet stream never ended")
		}
	})
}
