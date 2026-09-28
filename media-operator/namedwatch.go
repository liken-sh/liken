package main

// This file holds the watch on one named object, such as a CA
// ConfigMap or the serving Secret. The api role follows each of them
// and hands every version of the object to its owner, so a rotated
// certificate reaches the next TLS handshake when the API server writes
// it.
//
// The watch opens the collection with the field selector
// metadata.name=<name>. The API server authorizes that request against
// a Role's resourceNames, because it reads the name from the selector,
// so a Role that names one object grants its list and its watch. A GET
// on the object's own path with watch=true is not a watch: the API
// server answers it as a plain get and closes the stream.

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// The two kinds the api role follows by name.
var (
	configMapResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	secretResource    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
)

// watchNamed calls seen with the object each time it changes, and with
// nil when the object does not exist. It runs until the context ends.
// synced, when it is not nil, closes once seen has taken the first
// read, whether that read found the object or not.
//
// An object absent from the first read has no event, so the watch
// reports it as nil once that read is done. The lock keeps that report
// and the handler's calls in order: the informer adds an object to its
// store before it calls the handler, so a report that finds the store
// empty runs before the handler's call for the object that arrives
// next.
//
// An object that does not convert is reported and the owner keeps what
// it holds, because the next version of the object can be valid.
func watchNamed[T any](ctx context.Context, client dynamic.Interface, resource schema.GroupVersionResource,
	namespace, name, what string, seen func(held *T), synced chan<- struct{}) {
	var mu sync.Mutex
	take := func(object any) {
		held, err := convert[T](object)
		if err != nil {
			reportUnconverted(what, err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		seen(&held)
	}
	gone := func() {
		mu.Lock()
		defer mu.Unlock()
		seen(nil)
	}
	watchCollection(ctx, client, collectionWatch{
		resource:  resource,
		namespace: namespace,
		fields:    "metadata.name=" + name,
		handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    take,
			UpdateFunc: func(_, object any) { take(object) },
			DeleteFunc: func(any) { gone() },
		},
		synced: func(store cache.Store) {
			mu.Lock()
			if len(store.ListKeys()) == 0 {
				seen(nil)
			}
			mu.Unlock()
			if synced != nil {
				close(synced)
			}
		},
	})
}

// awaitSynced waits until every channel has closed or the wait runs
// out, and answers whether every one closed. The api role reads its
// certificates before it listens, and a read the API server does not
// answer in time must not hold the listener back for good.
func awaitSynced(ctx context.Context, channels ...<-chan struct{}) bool {
	for _, channel := range channels {
		select {
		case <-channel:
		case <-ctx.Done():
			return false
		}
	}
	return true
}
