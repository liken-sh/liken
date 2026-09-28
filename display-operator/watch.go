package main

// A watch keeps this program's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing. The
// operator watches Displays, Layouts, and the pods on its node, and
// display-api watches its capture sidecars, its two Secrets, and the
// cluster's client authority.
//
// client-go's reflector runs each watch. It reads the whole collection
// first, as a list or as the initial events of a streaming list, and
// then watches from the version that read returned, so it receives
// every change made after the read. It resumes a watch that the API
// server closed from the last version it delivered, reads the
// collection again after a 410 Gone, and backs off while the API
// server fails. Upstream maintains and tests that loop, so this
// program keeps none of its own.
//
// The program imports only three parts of client-go for this: the
// reflector and informer in tools/cache, the dynamic client that lists
// and watches any resource with no generated code, and rest for the
// in-cluster configuration. The typed clientset and the informer
// factories link a client and an informer for every built-in kind, and
// the image carries this binary into every node's operator pod. The
// program's own Client (apiclient.go) still sends every read and write
// that a pass makes.

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

// The collections this program watches.
var (
	displayResource   = schema.GroupVersionResource{Group: DisplayGroup, Version: DisplayVersion, Resource: "displays"}
	layoutResource    = schema.GroupVersionResource{Group: DisplayGroup, Version: DisplayVersion, Resource: "layouts"}
	podResource       = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	secretResource    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	configMapResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
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

// collectionWatch names the part of one collection a watch covers, and
// what it does with each change.
type collectionWatch struct {
	resource schema.GroupVersionResource
	// namespace is empty for a watch across every namespace, and for a
	// cluster-scoped resource.
	namespace string
	// labels and fields narrow the list and the watch alike. The API
	// server applies them, so a change outside them never reaches this
	// process.
	labels string
	fields string

	handler cache.ResourceEventHandler
	// synced, when it is not nil, runs once, after the handler has
	// taken every object of the first read. It receives the informer's
	// store, which holds that read.
	synced func(cache.Store)
	// reopened, when it is not nil, runs each time the API server
	// accepts a watch after the first one it accepted.
	reopened func()
}

// watchCollection keeps one collection current until the context ends.
// It returns after the last call to the handler and to synced, so a
// caller that closes a channel after it returns never has a send on
// the closed channel.
func watchCollection(ctx context.Context, client dynamic.Interface, w collectionWatch) {
	var collection dynamic.ResourceInterface = client.Resource(w.resource)
	if w.namespace != "" {
		collection = client.Resource(w.resource).Namespace(w.namespace)
	}
	scope := func(options *metav1.ListOptions) {
		options.LabelSelector = w.labels
		options.FieldSelector = w.fields
	}
	// The reflector calls the watch function from one goroutine, one
	// call at a time, so the count needs no lock. Only a watch that the
	// API server accepted counts: a refusal while the API server is
	// down is a retry, not a restart.
	opened := 0
	source := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			scope(&options)
			return collection.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			scope(&options)
			stream, err := collection.Watch(ctx, options)
			if err != nil {
				return nil, err
			}
			if opened > 0 && w.reopened != nil {
				w.reopened()
			}
			opened++
			return stream, nil
		},
	}
	store, informer := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: source,
		ObjectType:    &unstructured.Unstructured{},
		Handler:       w.handler,
		Transform:     dropManagedFields,
	})
	var group sync.WaitGroup
	if w.synced != nil {
		group.Go(func() {
			select {
			case <-informer.HasSyncedChecker().Done():
				w.synced(store)
			case <-ctx.Done():
			}
		})
	}
	informer.RunWithContext(ctx)
	group.Wait()
}

// dropManagedFields removes metadata.managedFields from each object
// before the informer stores it. The field records which client set
// each field of the object. This program never reads it, and without
// the transform the informer holds a copy of it for every object.
func dropManagedFields(object any) (any, error) {
	if item, ok := object.(*unstructured.Unstructured); ok {
		item.SetManagedFields(nil)
	}
	return object, nil
}

// convert decodes one object from a watch into this program's own
// struct. The informer hands a handler an *unstructured.Unstructured,
// or, for an object that was deleted while the watch was down, a
// tombstone that holds the last copy the informer knew.
//
// An object that does not convert has a field whose type differs from
// the struct, so the schema and the struct disagree. The error names
// the object, and the caller logs it, because an object that is
// dropped with no word leaves nobody a way to find out why the program
// ignored a change.
func convert[T any](object any) (T, error) {
	var out T
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		if tombstone.Obj == nil {
			return out, fmt.Errorf("the tombstone for %s holds no copy of the object", tombstone.Key)
		}
		object = tombstone.Obj
	}
	item, ok := object.(*unstructured.Unstructured)
	if !ok {
		return out, fmt.Errorf("the watch delivered a %T, not an object", object)
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, &out); err != nil {
		return out, fmt.Errorf("%s %s does not convert: %w", item.GetKind(), watchedName(item), err)
	}
	return out, nil
}

// watchedName is namespace/name for a namespaced object and name for a
// cluster-scoped one.
func watchedName(item *unstructured.Unstructured) string {
	if item.GetNamespace() == "" {
		return item.GetName()
	}
	return item.GetNamespace() + "/" + item.GetName()
}

// reportUnconverted logs an object that convert refused.
func reportUnconverted(what string, err error) {
	fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
}

// watchWakes turns every change to a collection into one wake. The
// watches on Displays, on Layouts, and on this node's pods carry
// nothing a pass uses but their arrival, because the pass that follows
// reads every object again, the same way every other wake in this
// operator works. So the handler converts nothing.
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
func watchWakes(ctx context.Context, client dynamic.Interface, kind reconcileKind, scope collectionWatch, wake func(), readings *metrics) {
	scope.handler = cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { wake() },
		UpdateFunc: func(any, any) { wake() },
		DeleteFunc: func(any) { wake() },
	}
	scope.synced = func(cache.Store) { wake() }
	scope.reopened = func() { readings.watchRestarted(kind) }
	watchCollection(ctx, client, scope)
}

// watchNamed calls seen with the object each time it changes, and with
// nil when the object does not exist. It runs until the context ends.
// The field selector names the object, which is also how RBAC matches
// a list and a watch against a rule's resourceNames.
//
// An object absent from the first read has no event, so the watch
// reports it as nil once that read is done. The lock keeps that report
// and the handler's calls in order: the informer adds an object to its
// store before it calls the handler, so a report that finds the store
// empty runs before the handler's call for the object that arrives
// next.
func watchNamed[T any](ctx context.Context, client dynamic.Interface, resource schema.GroupVersionResource,
	namespace, name, what string, seen func(held *T)) {
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
			defer mu.Unlock()
			if len(store.ListKeys()) == 0 {
				seen(nil)
			}
		},
	})
}
