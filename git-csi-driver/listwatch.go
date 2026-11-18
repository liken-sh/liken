package main

// listwatch.go keeps the node's copies of API objects current. The node
// lists each kind once and then watches it, because a list on a timer
// loads the API server and finds a change only at the next tick.
//
// client-go's reflector runs each watch. It watches from the list's
// resourceVersion, so no change made after the list is lost. When the
// API server closes a watch, the reflector resumes from the last
// version it received, lists again after a 410 Gone, and backs off
// while the API server fails. The driver uses the typed clientset, and
// not the dynamic client, because it watches only built-in kinds.

import (
	"context"
	"sync/atomic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// collection is one kind of API object that the node lists and
// watches.
type collection struct {
	// kind labels git_csi_watch_restarts_total.
	kind string
	// client is the clientset behind list and watch. The reflector asks
	// it whether it supports streaming lists, and a client-go fake
	// clientset reports that it does not.
	client any
	// object is an empty object of the kind, for the informer's type.
	object runtime.Object
	list   func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error)
	watch  func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error)
	// handler receives every object of the first list and every change
	// after it.
	handler cache.ResourceEventHandler
	metrics *metrics
	// indexers let a reader look up an object in the informer's store by
	// a field, instead of scanning the store.
	indexers cache.Indexers
}

// run runs the informer until the context ends. It returns after the
// last handler call.
func (c collection) run(ctx context.Context) {
	_, informer := c.informer()
	informer.RunWithContext(ctx)
}

// informer builds the informer and its store without starting it. A
// caller that reads the store runs the informer itself.
func (c collection) informer() (cache.Indexer, cache.Controller) {
	// Every watch after the first counts as a restart. client-go's
	// reflector opens its watches one at a time from the goroutine that
	// runs the informer. The flag is atomic so that the count stays
	// correct without depending on that.
	var opened atomic.Bool
	source := &cache.ListWatch{
		ListWithContextFunc: c.list,
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			watching, err := c.watch(ctx, options)
			if err == nil && opened.Swap(true) {
				c.metrics.watchRestarted(c.kind)
			}
			return watching, err
		},
	}
	// NewInformerWithOptions builds an Indexer only when Indexers is not
	// nil. An empty Indexers makes every store an Indexer, so the type
	// assertion below cannot fail.
	indexers := c.indexers
	if indexers == nil {
		indexers = cache.Indexers{}
	}
	store, informer := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: cache.ToListWatcherWithWatchListSemantics(source, c.client),
		ObjectType:    c.object,
		Handler:       c.handler,
		Transform:     dropManagedFields,
		Indexers:      indexers,
	})
	return store.(cache.Indexer), informer
}

// dropManagedFields clears metadata.managedFields before the informer
// caches an object. The driver never reads the field, and the
// PersistentVolume informer caches every PersistentVolume in the
// cluster.
func dropManagedFields(object any) (any, error) {
	if item, ok := object.(metav1.Object); ok {
		item.SetManagedFields(nil)
	}
	return object, nil
}

// unwrapped returns the object of a delete event. When an object was
// deleted while the watch was down, the informer sends a
// DeletedFinalStateUnknown that wraps the last copy it had.
func unwrapped(object any) any {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		return tombstone.Obj
	}
	return object
}
