package main

// A watch keeps this program's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing. The
// operator watches the collections its pass reads (clusterwatch.go),
// and the api role watches the named objects that hold its
// certificates (namedwatch.go).
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
// the pod build, which every playback pod runs, must stay small. The
// program's own Client (apiclient.go) still sends every write, and
// every read that must include this program's own last write.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
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

	// optional marks a collection whose resource another operator
	// defines, which a cluster may not have installed. optionalWatch
	// says how the watch treats its absence.
	optional bool

	// handler, when it is not nil, takes each change the informer
	// reports. A watch with no handler only keeps its store current.
	handler cache.ResourceEventHandler
	// synced, when it is not nil, runs once, after the handler has
	// taken every object of the first read. It receives the informer's
	// store, which holds that read and every change after it.
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
			list, err := collection.List(ctx, options)
			if w.optional && apierrors.IsNotFound(err) {
				return &unstructured.UnstructuredList{}, nil
			}
			return list, err
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			scope(&options)
			stream, err := collection.Watch(ctx, options)
			if w.optional && apierrors.IsNotFound(err) && options.SendInitialEvents == nil {
				return optionalWatch(ctx), nil
			}
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
	// The informer calls its handler for every change, so a watch that
	// only keeps a store current gets one that does nothing.
	handler := w.handler
	if handler == nil {
		handler = cache.ResourceEventHandlerFuncs{}
	}
	store, informer := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: source,
		ObjectType:    &unstructured.Unstructured{},
		Handler:       handler,
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

// optionalRecheck is how long the watch of an absent optional
// collection waits before it asks the API server again. It is a
// variable so a test drives it in milliseconds.
//
// An operator that defines the resource can be installed at any time,
// and nothing this program watches reports that. So the watch of an
// absent resource is a quiet stream that closes after this wait, and
// the reflector opens the next watch, which finds the resource once it
// exists. The list before it answers an empty collection, so the pass
// reads an absent resource as a resource with no objects, which is
// what the API server's 404 means. Without
// the quiet stream the reflector would back off and log a failure
// every 30 seconds for as long as the resource is absent.
var optionalRecheck = 5 * time.Minute

// optionalWatch is the quiet stream. It sends no event, and closes when
// the context ends or the recheck is due.
func optionalWatch(ctx context.Context) watch.Interface {
	stream := watch.NewFake()
	go func() {
		timer := time.NewTimer(optionalRecheck)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		stream.Stop()
	}()
	return stream
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

// poke never blocks, and the wake channel buffers exactly one. A
// wake already queued says everything a second one would say,
// because the pass that answers it reads the whole collection.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
