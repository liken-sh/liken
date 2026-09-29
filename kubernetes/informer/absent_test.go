package informer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// undefined answers 404 to every request until the collection's
// definition arrives, the way the API server answers for a kind it
// does not serve, and then hands each request to the collection.
type undefined struct {
	defined    atomic.Bool
	collection http.Handler
}

func (u *undefined) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !u.defined.Load() {
		http.NotFound(w, r)
		return
	}
	u.collection.ServeHTTP(w, r)
}

// undefinedWatcher points a dynamic client at an undefined collection.
func undefinedWatcher(t *testing.T, server *undefined) dynamic.Interface {
	t.Helper()
	listening := httptest.NewServer(server)
	t.Cleanup(listening.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: listening.URL})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// A collection whose definition is absent reads as an empty copy that
// answers a pass, and the quiet stream counts as no watch opened again.
// When the definition arrives, the copy holds its objects.
func TestAnAbsentCollectionReadsAsEmptyUntilItArrives(t *testing.T) {
	server := &undefined{collection: newWatchServer(thingsPath, [][]thing{{newThing("a", "7", 1)}})}
	var reopened atomic.Int64
	c := Start(t.Context(), undefinedWatcher(t, server), Source{Resource: thingResource}, Options{
		Reopened:      func() { reopened.Add(1) },
		Absent:        apierrors.IsNotFound,
		AbsentRecheck: 20 * time.Millisecond,
	})
	eventually(t, "the absent collection syncs", c.View().Ready)
	if held := CachedList[thing](c.View()); len(held) != 0 {
		t.Errorf("the copy of an absent collection holds %+v", held)
	}

	server.defined.Store(true)
	eventually(t, "the copy holds the collection that arrived", func() bool {
		_, held := Cached[thing](c.View(), "a")
		return held
	})
	if reopened.Load() != 0 {
		t.Errorf("the watch counted %d reopened watches, want none", reopened.Load())
	}
}

// With no Absent, a 404 is a failure like any other, and the copy never
// syncs.
func TestAnAbsentCollectionIsAFailureWhenTheOptionIsUnset(t *testing.T) {
	server := &undefined{collection: newWatchServer(thingsPath, [][]thing{{}})}
	c := Start(t.Context(), undefinedWatcher(t, server), Source{Resource: thingResource}, Options{})

	time.Sleep(100 * time.Millisecond)
	if c.Synced() {
		t.Error("a collection the API server does not serve synced")
	}
}

// The quiet stream ends when the watch's context ends, so a watch that
// stops leaves no goroutine behind.
func TestTheQuietStreamEndsWithTheWatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	stream := quietWatch(ctx, time.Hour)
	cancel()

	select {
	case _, open := <-stream.ResultChan():
		if open {
			t.Error("the quiet stream sent an event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the quiet stream never ended")
	}
}
