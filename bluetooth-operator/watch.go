package main

// A watch keeps this operator's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing.
//
// client-go's reflector runs each watch. It reads the whole collection
// first, as a list or as the initial events of a streaming list, and
// then watches from the version that read returned, so it receives
// every change made after the read. It resumes a watch that the API
// server closed from the last version it delivered, reads the
// collection again after a 410 Gone, and backs off while the API
// server fails. Upstream maintains and tests that loop, so this
// operator keeps none of its own.
//
// The operator imports only three parts of client-go for this: the
// reflector and informer in tools/cache, the dynamic client that lists
// and watches a custom resource with no generated code, and rest for
// the in-cluster configuration. The
// typed clientset and the informer factories link a client for every
// built-in kind, and this operator watches none of them. The
// operator's own Client (apiclient.go) still sends every read and
// write that a pass makes.

import (
	"context"
	"fmt"
	"os"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// The three collections this operator watches.
var (
	adapterResource        = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "adapters"}
	peripheralResource     = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "peripherals"}
	pairingRequestResource = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "pairingrequests"}
)

// inClusterWatcher builds the dynamic client for the watches from the
// pod's ServiceAccount, the same credentials InClusterClient reads.
func inClusterWatcher() (dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(config)
}

// watchCollection keeps one collection current until the context ends,
// and sends each change to the handler. A label selector, when it is
// not empty, narrows the list and the watch to the objects that carry
// that label. The watch of a namespaced resource covers every
// namespace.
//
// synced, when it is not nil, runs once, after the handler has taken
// every object of the first read.
func watchCollection(ctx context.Context, client dynamic.Interface, resource schema.GroupVersionResource, selector string, handler cache.ResourceEventHandler, synced func()) {
	collection := client.Resource(resource)
	source := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = selector
			return collection.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = selector
			return collection.Watch(ctx, options)
		},
	}
	_, informer := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: source,
		ObjectType:    &unstructured.Unstructured{},
		Handler:       handler,
		Transform:     dropManagedFields,
	})
	// The informer's handlers and synced both run before this returns,
	// so a caller that closes a channel after it returns never has a
	// send on the closed channel.
	var group sync.WaitGroup
	if synced != nil {
		group.Go(func() {
			select {
			case <-informer.HasSyncedChecker().Done():
				synced()
			case <-ctx.Done():
			}
		})
	}
	informer.RunWithContext(ctx)
	group.Wait()
}

// dropManagedFields removes metadata.managedFields from each object
// before the informer stores it. The field records which client set
// each field of the object. The operator never reads it, and without
// the transform the informer holds a copy of it for every object.
func dropManagedFields(object any) (any, error) {
	if item, ok := object.(*unstructured.Unstructured); ok {
		item.SetManagedFields(nil)
	}
	return object, nil
}

// convert decodes one object from a watch into the operator's own
// struct. The informer hands a handler an *unstructured.Unstructured,
// or, for an object that was deleted while the watch was down, a
// tombstone that holds the last copy the informer knew.
//
// An object that does not convert has a field whose type differs
// from the operator's struct, so the CRD schema and the struct
// disagree. The error names the object, and the caller logs it, because an object
// that is dropped with no word leaves nobody a way to find out why the
// operator ignored an edit.
func convert[T any](object any) (T, error) {
	var out T
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		object = tombstone.Obj
	}
	item, ok := object.(*unstructured.Unstructured)
	if !ok {
		return out, fmt.Errorf("the watch delivered a %T, not an object", object)
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, &out); err != nil {
		return out, fmt.Errorf("%s %s does not convert: %w", item.GetKind(), objectName(item), err)
	}
	return out, nil
}

// objectName is namespace/name for a namespaced object and name for a
// cluster-scoped one.
func objectName(item *unstructured.Unstructured) string {
	if item.GetNamespace() == "" {
		return item.GetName()
	}
	return item.GetNamespace() + "/" + item.GetName()
}

// reportUnconverted logs an object that convert refused.
func reportUnconverted(what string, err error) {
	fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
}
