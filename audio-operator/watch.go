package main

// A watch keeps this program's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs one open
// connection and no reads.
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
// factories link a client for every built-in kind. The program's own
// Client (apiclient.go) still sends every read and write.
//
// Five collections are watched, each as narrowly as its reader needs:
//
//   - The operator watches its machine's Sinks and Sources with the
//     field selector status.node (endpointwatch.go).
//   - audio-api watches the Secret audio-capture-server and the
//     ConfigMap extension-apiserver-authentication, each with the
//     field selector metadata.name (api.go). RBAC authorizes a list
//     or a watch against resourceNames only when the request selects
//     that one name, so the selector is what lets a Role grant list
//     and watch on one object.
//   - audio-api watches the operator's pods with the label selector
//     app=audio-operator (apipods.go).

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

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
	sinkResource      = schema.GroupVersionResource{Group: EndpointGroup, Version: EndpointVersion, Resource: "sinks"}
	sourceResource    = schema.GroupVersionResource{Group: EndpointGroup, Version: EndpointVersion, Resource: "sources"}
	secretResource    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	configMapResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	podResource       = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
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

// collectionWatch is one watch: the collection, the selectors that
// narrow it, and what runs on each change.
type collectionWatch struct {
	collection dynamic.ResourceInterface

	// fieldSelector and labelSelector narrow the list and the watch.
	// An empty selector takes every object.
	fieldSelector, labelSelector string

	handler cache.ResourceEventHandler

	// synced, when it is not nil, runs once, after the handler has
	// taken every object of the first read.
	synced func()

	// reopened, when it is not nil, runs each time the API server
	// accepts a watch after the first it accepted. A watch the server
	// refused is not counted, so an outage adds nothing, and a plain
	// list's watch after a refused streaming list is not a restart.
	reopened func()
}

// start keeps the collection current in the background until the
// context ends. It answers the informer's store, which holds every
// object the watch selects, and whether the store holds the whole of
// the first read yet. A reader that looks an object up in the store
// reads memory, not the API server.
func (w collectionWatch) start(ctx context.Context) (cache.Store, func() bool) {
	var opens atomic.Int64
	source := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector, options.LabelSelector = w.fieldSelector, w.labelSelector
			return w.collection.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector, options.LabelSelector = w.fieldSelector, w.labelSelector
			opened, err := w.collection.Watch(ctx, options)
			if err == nil && opens.Add(1) > 1 && w.reopened != nil {
				w.reopened()
			}
			return opened, err
		},
	}
	store, informer := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: source,
		ObjectType:    &unstructured.Unstructured{},
		Handler:       w.handler,
		Transform:     dropManagedFields,
	})
	if w.synced != nil {
		go func() {
			select {
			case <-informer.HasSyncedChecker().Done():
				w.synced()
			case <-ctx.Done():
			}
		}()
	}
	go informer.RunWithContext(ctx)
	return store, informer.HasSynced
}

// dropManagedFields removes metadata.managedFields from each object
// before the informer stores it. The field records which client set
// each field of the object. The program never reads it, and without
// the transform the informer holds a copy of it for every object.
func dropManagedFields(object any) (any, error) {
	if item, ok := object.(*unstructured.Unstructured); ok {
		item.SetManagedFields(nil)
	}
	return object, nil
}

// unwrap answers the object a handler received. The informer hands a
// handler an *unstructured.Unstructured, or, for an object that was
// deleted while the watch was down, a tombstone that holds the last
// copy the informer knew. A tombstone can hold no copy at all.
func unwrap(object any) (*unstructured.Unstructured, error) {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		object = tombstone.Obj
	}
	item, ok := object.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("the watch delivered a %T, not an object", object)
	}
	return item, nil
}

// convert decodes one object from a watch into this program's own
// struct.
//
// An object that does not convert has a field whose type differs from
// the struct, so the CRD schema and the struct disagree. The error
// names the object, and the caller logs it, because an object that is
// dropped with no word leaves nobody a way to find out why the program
// ignored it.
func convert[T any](object any) (T, error) {
	var out T
	item, err := unwrap(object)
	if err != nil {
		return out, err
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
