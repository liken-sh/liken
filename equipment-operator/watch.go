package main

// A watch keeps this operator's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing. The shared
// informer package runs each watch on client-go's reflector, and keeps
// the copy of the collection that a pass reads (objectcache.go). The
// operator's own Client (apiclient.go) still sends every write, and
// every read the stores cannot answer.
//
// A handler only wakes the loop, and reads an object only to decide
// whether the change is one the pass must see.

import (
	"context"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
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
// way the shared client does. The watches and the Deployment's leader
// election both use it.
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
// and sends each change to the handler. It returns after the last
// handler call.
//
// synced, when it is not nil, runs once, after the handler has taken
// every object of the first read. A caller that lists before the
// informer reads needs it: an object deleted between the two reads is
// in neither the informer's read nor any event.
//
// restarted, when it is not nil, runs for each watch the API server
// accepts after the first, and counts it in
// equipment_watch_restarts_total. The first read is itself a watch
// when the reflector reads with a streaming list.
//
// held, when it is not nil, holds the informer's store while the
// informer runs, so a pass reads the collection from it
// (objectcache.go), and tells a caller that waits for a change when
// the store takes one.
//
// fieldSelector, when it is not empty, narrows the list and the watch
// to the objects it selects.
//
// absent, when it is not nil, names the refusals that mean the
// collection is not there to watch, such as the 404 of a kind whose
// definition another operator installs. The store then holds an empty
// collection until the kind arrives.
func watchCollection(ctx context.Context, client *Client, resource schema.GroupVersionResource, fieldSelector string, absent func(error) bool, handler cache.ResourceEventHandler, synced, restarted func(), held *watchStore) {
	watcher, err := client.watcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watching %s: %v\n", resource.Resource, err)
		return
	}
	if held != nil {
		handler = bothHandlers(handler, held.announcer())
	}
	collection := informer.Start(ctx, watcher, informer.Source{Resource: resource, FieldSelector: fieldSelector}, informer.Options{
		Handler:       handler,
		Synced:        synced,
		Reopened:      restarted,
		Absent:        absent,
		AbsentRecheck: optionalRecheck,
	})
	held.hold(collection.View(), fieldSelector != "")
	defer held.release()
	<-collection.Done()
}

// optionalRecheck is how long the watch of an absent collection waits
// before it asks the API server again (informer.Options.AbsentRecheck).
// It is a variable so a test waits milliseconds instead.
var optionalRecheck = 5 * time.Minute

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
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report(h.what, err)
	}
	poke(h.wake)
}

// updated takes a change to an object the informer held, and wakes the
// loop only when the mark moved.
func (h markHandler[T, M]) updated(before, after any) {
	now, err := informer.Convert[T](after)
	if err != nil {
		informer.Report(h.what, err)
		poke(h.wake)
		return
	}
	// A held copy that does not convert was logged when it arrived.
	// Nothing says what it held, so the change counts as a move.
	was, err := informer.Convert[T](before)
	if err != nil || h.mark(was) != h.mark(now) {
		poke(h.wake)
	}
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
	watchCollection(ctx, client, receiverResource, "", nil, handler, synced, readings.watchRestarted, held)
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
