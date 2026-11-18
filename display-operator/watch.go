package main

// Every watch in this program runs on client-go's reflector through
// the informer package, which also keeps the copy of the collection
// that a pass reads (objectcache.go). The operator watches Displays,
// Layouts, and the pods on its node. display-api watches its capture
// sidecars, its TLS Secrets, and the ConfigMap of the client CA.

import (
	"context"

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

// wakeWatch watches one collection and calls wake on each change until
// ctx ends. wake receives no object, because the pass it starts reads
// the returned collection.
//
// A nil handler wakes on every add, update, and delete. openLayouts
// uses it, because a Layout has no status and every update is an edit.
// openPods and openDisplays pass a handler that wakes only on the
// changes their passes read.
//
// wake also runs once when the first list completes, even for an empty
// list. A pass at startup can read the collection before that list,
// and a change made in between shows up in the list and not as an
// event.
//
// When the API server closes the watch, the informer resumes it from
// the last resourceVersion, or lists again after a 410 Gone, so every
// change still calls wake. Each watch opened after the first
// increments display_watch_restarts_total.
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
