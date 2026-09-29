package main

// A watch keeps this program's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing. The
// operator watches Displays, Layouts, and the pods on its node, and
// display-api watches its capture sidecars, its two Secrets, and the
// cluster's client authority.
//
// The shared informer package runs each watch on client-go's reflector,
// and keeps the copy of the collection that a pass reads
// (objectcache.go). The shared apiclient package sends every write, and
// every read that a watch's copy does not answer.

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// The collections this program watches.
var (
	displayResource   = schema.GroupVersionResource{Group: DisplayGroup, Version: DisplayVersion, Resource: "displays"}
	layoutResource    = schema.GroupVersionResource{Group: DisplayGroup, Version: DisplayVersion, Resource: "layouts"}
	podResource       = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	secretResource    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	configMapResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
)

// wakeWatch starts a watch that turns a change to a collection into
// one wake, and runs it until the context ends. The pass that follows
// reads every object again from the watch's store, the same way every
// other wake in this operator works, so the handler converts nothing.
// With no handler of its own, the watch wakes on every change: a
// Layout has no status, so each update to one is a person's edit. The
// pod watch (openPods) and the Display watch (openDisplays) pass a
// handler that wakes only on the changes their passes read.
//
// The first read is a wake of its own, even a read that finds no
// object. At a start, a pass can read the collection before the watch
// does, and a change made between the two reads is in the watch's read
// and in no event. A watch that the API server closed resumes from the
// last version it delivered, so the API server sends each change made
// during the gap as an event. When it cannot resume, after a 410 Gone,
// the informer reads the collection again and reports each object as
// an add, an update, or a delete. Either way each change wakes the
// pass, and no change in the gap is lost.
//
// Each watch the API server accepts after the first counts as one
// restart on display_watch_restarts_total.
func wakeWatch(ctx context.Context, client dynamic.Interface, kind reconcileKind, source informer.Source,
	handler cache.ResourceEventHandler, wake func(), readings *metrics) *informer.Collection {
	if handler == nil {
		handler = cache.ResourceEventHandlerFuncs{
			AddFunc:    func(any) { wake() },
			UpdateFunc: func(any, any) { wake() },
			DeleteFunc: func(any) { wake() },
		}
	}
	return informer.Start(ctx, client, source, informer.Options{
		Handler:  handler,
		Synced:   wake,
		Reopened: func() { readings.watchRestarted(kind) },
	})
}

// watchNamed calls seen with the object each time it changes, and with
// nil when the object does not exist. It returns when the context ends
// and the watch has stopped. The field selector names the object,
// which is also how RBAC matches a list and a watch against a rule's
// resourceNames.
//
// An object absent from the first read has no event, so the watch
// reports it as nil once that read is done, unless an event for the
// object reached the handler first. The informer calls the handler on
// one goroutine and the synced report on another, and the lock runs
// one call to seen at a time. An object that arrives after an empty
// first read is seen last in either order: its event runs after the
// nil report, or the report finds the event and reports nothing.
func watchNamed[T any](ctx context.Context, client dynamic.Interface, resource schema.GroupVersionResource,
	namespace, name, what string, seen func(held *T)) {
	var mu sync.Mutex
	arrived := false
	take := func(object any) {
		mu.Lock()
		defer mu.Unlock()
		// An object that does not convert still exists, so it is not
		// reported as gone at the end of the first read.
		arrived = true
		held, err := informer.Convert[T](object)
		if err != nil {
			informer.Report(what, err)
			return
		}
		seen(&held)
	}
	gone := func() {
		mu.Lock()
		defer mu.Unlock()
		arrived = true
		seen(nil)
	}
	source := informer.Source{Resource: resource, Namespace: namespace, FieldSelector: "metadata.name=" + name}
	watch := informer.Start(ctx, client, source, informer.Options{
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    take,
			UpdateFunc: func(_, object any) { take(object) },
			DeleteFunc: func(any) { gone() },
		},
		Synced: func() {
			mu.Lock()
			defer mu.Unlock()
			if !arrived {
				seen(nil)
			}
		},
	})
	<-watch.Done()
}
