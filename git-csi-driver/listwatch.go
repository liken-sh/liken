package main

// listwatch.go keeps the node's view of API objects current with
// events. A read of the whole state on a timer loads the API server and
// finds a change only at the next tick, so the node reads the whole
// state once, then follows every change after that read through a
// watch.
//
// client-go's reflector runs each watch. It reads the whole collection
// first, as a list or as the initial events of a streaming list, and
// then watches from the version that read returned, so it receives
// every change made after the read. It resumes a watch that the API
// server closed from the last version it delivered, reads the
// collection again after a 410 Gone, and backs off while the API
// server fails. Upstream maintains and tests that loop, so the driver
// keeps none of its own.
//
// The driver watches through the typed clientset, not the dynamic
// client. It already links the typed clientset for every read and
// write it makes, and it watches only built-in kinds. So the watch
// links tools/cache and the few packages it imports and no client of
// its own, and each handler receives a typed object with no
// conversion step.

import (
	"context"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// defaultRetry is how long the node waits after a read of the cluster
// failed before it reads again. It bounds the load a refusing API
// server takes from each volume's search for its claim, and from each
// class the node could not read, to one failed call per wait. Inside
// one call, client-go retries a request that the server resets, or
// answers with Retry-After, up to ten times about a second apart, so
// one failed call can be up to eleven requests. The typed client sets
// that count on each request and offers no way to change it.
const defaultRetry = 30 * time.Second

// collection is one list and the watch that continues from it.
type collection struct {
	// kind labels git_csi_watch_restarts_total.
	kind string
	// client is the clientset the list and the watch call. A fake
	// clientset declares that it does not answer a streaming list, and
	// the reflector reads that declaration from the client.
	client any
	// object is an empty object of the kind, which tells the informer
	// what the watch decodes to.
	object runtime.Object
	list   func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error)
	watch  func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error)
	// handler takes every object of the first read, and every change
	// after it.
	handler  cache.ResourceEventHandler
	readings *metrics
}

// follow runs the watch until the context ends. It returns only after
// the last handler call.
func (c collection) follow(ctx context.Context) {
	// Every watch after the first counts as a restart. The reflector
	// opens one watch at a time. The flag is atomic all the same, so a
	// change in client-go that opens watches from two goroutines makes
	// no data race here.
	var opened atomic.Bool
	source := &cache.ListWatch{
		ListWithContextFunc: c.list,
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			watching, err := c.watch(ctx, options)
			if err == nil && opened.Swap(true) {
				c.readings.watchRestarted(c.kind)
			}
			return watching, err
		},
	}
	_, informer := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: cache.ToListWatcherWithWatchListSemantics(source, c.client),
		ObjectType:    c.object,
		Handler:       c.handler,
		Transform:     dropManagedFields,
	})
	informer.RunWithContext(ctx)
}

// dropManagedFields removes metadata.managedFields from each object
// before the informer stores it. The field records which client set
// each field of the object. The driver never reads it, and without the
// transform the informer holds a copy of it for every
// PersistentVolume in the cluster.
func dropManagedFields(object any) (any, error) {
	if item, ok := object.(metav1.Object); ok {
		item.SetManagedFields(nil)
	}
	return object, nil
}

// unwrapped is the object a delete names. For an object deleted while
// the watch was down, the informer sends a tombstone that holds the
// last copy it knew.
func unwrapped(object any) any {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		return tombstone.Obj
	}
	return object
}

// waitOut waits out the retry after a call that failed, and ends early
// when the context does.
func waitOut(ctx context.Context, retry time.Duration) {
	timer := time.NewTimer(retry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
