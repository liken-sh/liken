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
// the connection's configuration. The typed clientset and the informer
// factories link a client for every built-in kind, and this operator
// watches none of them. The operator's own Client (apiclient.go) still
// sends every write, and every read the stores cannot answer.
//
// A handler only wakes the loop, and reads an object only to decide
// whether the change is one the pass must see. A pass reads each kind
// from the informers' stores (objectcache.go).

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

// The collections this operator watches. Each one is cluster-scoped.
var (
	receiverResource   = schema.GroupVersionResource{Group: "equipment.liken.sh", Version: "v1alpha1", Resource: "receivers"}
	cecBusResource     = schema.GroupVersionResource{Group: "equipment.liken.sh", Version: "v1alpha1", Resource: "cecbuses"}
	televisionResource = schema.GroupVersionResource{Group: "equipment.liken.sh", Version: "v1alpha1", Resource: "televisions"}
	displayResource    = schema.GroupVersionResource{Group: "display.liken.sh", Version: "v1alpha1", Resource: "displays"}
)

// restConfig answers the client-go configuration that reaches the same
// API server with the same credentials as the Client's own requests:
// the ServiceAccount's CA and token in a pod, and nothing in a test.
// client-go reads the token file again as the kubelet renews it, the
// way send does. The watches and the Deployment's leader election
// both use it.
func (c *Client) restConfig() *rest.Config {
	config := &rest.Config{Host: c.base}
	if c.credentials != "" {
		config.TLSClientConfig.CAFile = c.credentials + "/ca.crt"
		config.BearerTokenFile = c.credentials + "/token"
	}
	return config
}

// watcher answers the dynamic client the watches use. The client is
// built once and shared, so every watch uses one connection pool.
func (c *Client) watcher() (dynamic.Interface, error) {
	c.watchOnce.Do(func() {
		c.watchClient, c.watchErr = dynamic.NewForConfig(c.restConfig())
	})
	return c.watchClient, c.watchErr
}

// watchCollection keeps one collection current until the context ends,
// and sends each change to the handler.
//
// synced, when it is not nil, runs once, after the handler has taken
// every object of the first read. A caller that lists before the
// informer reads needs it: an object deleted between the two reads is
// in neither the informer's read nor any event.
//
// restarted, when it is not nil, runs for each watch the reflector
// opens after the first, and counts it in
// equipment_watch_restarts_total. The first read is itself a watch
// when the reflector reads with a streaming list.
//
// held, when it is not nil, holds the informer's store while the
// informer runs, so a pass reads the collection from it
// (objectcache.go).
//
// fieldSelector, when it is not empty, narrows the list and the watch
// to the objects it selects.
func watchCollection(ctx context.Context, client *Client, resource schema.GroupVersionResource, fieldSelector string, handler cache.ResourceEventHandler, synced, restarted func(), held *watchStore) {
	watcher, err := client.watcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watching %s: %v\n", resource.Resource, err)
		return
	}
	collection := watcher.Resource(resource)
	var opens sync.Mutex
	opened := false
	source := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return collection.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector
			opens.Lock()
			again := opened
			opened = true
			opens.Unlock()
			if again && restarted != nil {
				restarted()
			}
			return collection.Watch(ctx, options)
		},
	}
	store, informer := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: source,
		ObjectType:    &unstructured.Unstructured{},
		Handler:       handler,
		Transform:     dropManagedFields,
	})
	// The informer's handlers and synced both run before this returns,
	// so a caller that stops reading the wake channel after it returns
	// loses no send.
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
	held.hold(store, informer.HasSynced, fieldSelector != "")
	defer held.release()
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
// tombstone that holds the last copy the informer knew, or no copy.
//
// An object that does not convert has a field whose type differs from
// the operator's struct, so the CRD schema and the struct disagree.
// The error names the object, and the caller logs it, because an
// object that is dropped with no word leaves nobody a way to find out
// why the operator ignored an edit.
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
		return out, fmt.Errorf("%s %s does not convert: %w", item.GetKind(), item.GetName(), err)
	}
	return out, nil
}

// wakeOnEvery wakes the loop on every change the informer reports. A
// loop that reads the whole object, its status included, uses it. The
// informer reports a read after a gap in the watch as one update for
// each object it held, so a gap wakes the loop as well.
func wakeOnEvery(wake chan<- struct{}) cache.ResourceEventHandler {
	signal := func() { poke(wake) }
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { signal() },
		UpdateFunc: func(any, any) { signal() },
		DeleteFunc: func(any) { signal() },
	}
}

// markHandler wakes the loop when a change moves the part of an object
// that the loop reads, which mark returns. A new object and a removed
// object always wake it. The informer hands an update both the copy it
// held and the new copy, so the handler compares their marks and keeps
// no copy of its own. After a gap in the watch, the informer reports
// each difference from what it held as an addition, an update, or a
// deletion, so a change made during the gap reaches the handler too.
//
// An object that does not convert is logged and wakes the loop. The
// handler cannot tell what changed, and one extra pass costs less than
// a missed edit.
type markHandler[T any, M comparable] struct {
	what string
	wake chan<- struct{}
	mark func(T) M
}

func (h markHandler[T, M]) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    h.arrived,
		UpdateFunc: h.updated,
		DeleteFunc: h.arrived,
	}
}

// arrived takes a new or a removed object. Both change what the loop
// reads, so both wake it. The object is converted only to report one
// that does not convert.
func (h markHandler[T, M]) arrived(object any) {
	if _, err := convert[T](object); err != nil {
		reportUnconverted(h.what, err)
	}
	poke(h.wake)
}

// updated takes a change to an object the informer held, and wakes the
// loop only when the mark moved.
func (h markHandler[T, M]) updated(before, after any) {
	now, err := convert[T](after)
	if err != nil {
		reportUnconverted(h.what, err)
		poke(h.wake)
		return
	}
	// A held copy that does not convert was logged when it arrived.
	// Nothing says what it held, so the change counts as a move.
	was, err := convert[T](before)
	if err != nil || h.mark(was) != h.mark(now) {
		poke(h.wake)
	}
}

// reportUnconverted logs an object that convert refused.
func reportUnconverted(what string, err error) {
	fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
}

// watchReceivers is the Deployment's one watch of the Receivers, which
// both of its loops share, so the process holds each Receiver once. It
// wakes the Receiver loop on every change, its status included,
// because that pass reads both. It wakes the CECBus loop through
// specWake only for a change that watchReceiverSpecs would report,
// because that loop reads a Receiver's spec and not its status. held
// holds the store for the CECBus loop. specWake may be nil.
func watchReceivers(ctx context.Context, client *Client, wake, specWake chan<- struct{}, readings *metrics, held *watchStore) {
	handler := wakeOnEvery(wake)
	synced := func() { poke(wake) }
	if specWake != nil {
		specs := markHandler[Receiver, specMark]{what: "the Receivers", wake: specWake, mark: receiverSpecMark}
		handler = bothHandlers(handler, specs.handler())
		synced = func() {
			poke(wake)
			poke(specWake)
		}
	}
	watchCollection(ctx, client, receiverResource, "", handler, synced, readings.watchRestarted, held)
}

// bothHandlers sends each change to two handlers, in order.
func bothHandlers(first, second cache.ResourceEventHandler) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(object any) {
			first.OnAdd(object, false)
			second.OnAdd(object, false)
		},
		UpdateFunc: func(before, after any) {
			first.OnUpdate(before, after)
			second.OnUpdate(before, after)
		},
		DeleteFunc: func(object any) {
			first.OnDelete(object)
			second.OnDelete(object)
		},
	}
}
