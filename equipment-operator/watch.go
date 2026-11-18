package main

// kubernetes/informer runs each watch on client-go's reflector and
// keeps the copy of the collection that a reconcile pass reads
// (objectcache.go). The operator's Client (apiclient.go) sends every
// write, and every read that the informer's store cannot serve. A
// handler only wakes a loop, and reads an object only to decide whether
// the loop needs to see the change.

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

// The custom resources that the operator watches. Each is cluster-scoped.
var (
	receiverResource   = schema.GroupVersionResource{Group: "equipment.liken.sh", Version: "v1alpha1", Resource: "receivers"}
	cecBusResource     = schema.GroupVersionResource{Group: "equipment.liken.sh", Version: "v1alpha1", Resource: "cecbuses"}
	televisionResource = schema.GroupVersionResource{Group: "equipment.liken.sh", Version: "v1alpha1", Resource: "televisions"}
	displayResource    = schema.GroupVersionResource{Group: "display.liken.sh", Version: "v1alpha1", Resource: "displays"}
)

// restConfig returns a client-go configuration with the same API
// server and credentials as the Client's own requests: the
// ServiceAccount's CA and token in a pod, and the fake API server's
// transport in a test. It names the token file and not the token, so
// client-go reads the token again after the kubelet renews it. The
// watches and the Deployment's leader election use it.
func (c *Client) restConfig() *rest.Config {
	config := &rest.Config{Host: c.base, Transport: c.transport, Dial: c.dial}
	if c.credentials != "" {
		config.TLSClientConfig.CAFile = c.credentials + "/ca.crt"
		config.BearerTokenFile = c.credentials + "/token"
	}
	return config
}

// watcher returns the dynamic client that the watches use. It is built
// once, so the watches share its connection pool.
func (c *Client) watcher() (dynamic.Interface, error) {
	c.watchOnce.Do(func() {
		c.watchClient, c.watchErr = dynamic.NewForConfig(c.restConfig())
	})
	return c.watchClient, c.watchErr
}

// watchCollection watches source until ctx ends and sends each change
// to handler. It returns after the last handler call.
//
// absent, when not nil, reports the errors that mean the collection
// does not exist, such as the 404 for a kind whose CRD another operator
// installs. The store then has an empty collection until the kind
// exists.
//
// synced, when not nil, runs once, after the handler has received every
// object of the first list. A caller that lists the collection before
// the watch starts uses it to run a pass again, because an object
// deleted between the two lists is in neither the watch's list nor any
// event.
//
// restarted, when not nil, runs for each watch that the API server
// accepts after the first. When the reflector reads its first list as
// a streaming list, that list is the first watch. The callers pass a
// function that counts it in equipment_watch_restarts_total.
//
// store, when not nil, receives the informer's store while the informer
// runs, so a pass reads the collection from it (objectcache.go), and
// a caller that waits on store.changed is notified of each change.
func watchCollection(ctx context.Context, client *Client, source informer.Source, absent func(error) bool, handler cache.ResourceEventHandler, synced, restarted func(), store *watchStore) {
	watcher, err := client.watcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watching %s: %v\n", source, err)
		return
	}
	if store != nil {
		handler = bothHandlers(handler, store.announcer())
	}
	collection := informer.Start(ctx, watcher, source, informer.Options{
		Handler:       handler,
		Synced:        synced,
		Reopened:      restarted,
		Absent:        absent,
		AbsentRecheck: optionalRecheck,
	})
	store.hold(collection.View(), source.FieldSelector != "" || source.LabelSelector != "")
	defer store.release()
	<-collection.Done()
}

// optionalRecheck is how long the watch of an absent collection waits
// before it checks the API server again (informer.Options.AbsentRecheck).
const optionalRecheck = 5 * time.Minute

// wakeOnEvery wakes the loop on every change that the informer reports,
// for a loop that reads the whole object, status included.
func wakeOnEvery(wake chan<- struct{}) cache.ResourceEventHandler {
	signal := func() { sendWake(wake) }
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { signal() },
		UpdateFunc: func(any, any) { signal() },
		DeleteFunc: func(any) { signal() },
	}
}

// markHandler wakes the loop for a new or removed object, and for an
// update that changes mark, the part of the object that the loop reads.
// The informer passes an update both the old and the new copy, so the
// handler keeps no copy of its own. After the watch reconnects, the
// informer reports each difference from its copy as an add, an update,
// or a delete, so a change made while the watch was down still reaches
// the handler.
//
// An object that does not convert is logged and wakes the loop, because
// the handler cannot tell what changed, and an extra pass costs less
// than a missed edit.
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

// arrived handles a new or a removed object. It converts the object
// only to log one that does not convert.
func (h markHandler[T, M]) arrived(object any) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report(h.what, err)
	}
	sendWake(h.wake)
}

// updated wakes the loop when an update moved the mark.
func (h markHandler[T, M]) updated(before, after any) {
	now, err := informer.Convert[T](after)
	if err != nil {
		informer.Report(h.what, err)
		sendWake(h.wake)
		return
	}
	// An old copy that does not convert was logged when it arrived. Its
	// mark cannot be compared, so the update counts as a change.
	was, err := informer.Convert[T](before)
	if err != nil || h.mark(was) != h.mark(now) {
		sendWake(h.wake)
	}
}

// watchReceivers is the Deployment's watch of Receivers. The Receiver
// loop and the CECBus loop share it, so the process keeps one copy of
// each Receiver. It wakes the Receiver loop on every change, because
// that loop reads spec and status. When specWake is not nil, it wakes
// the CECBus loop through specWake only for a change that
// watchReceiverSpecs would report, because that loop reads only the
// spec. store receives the informer's store, which both loops read.
func watchReceivers(ctx context.Context, client *Client, wake, specWake chan<- struct{}, readings *metrics, store *watchStore) {
	handler := wakeOnEvery(wake)
	synced := func() { sendWake(wake) }
	if specWake != nil {
		specs := markHandler[Receiver, specMark]{what: "the Receivers", wake: specWake, mark: receiverSpecMark}
		handler = bothHandlers(handler, specs.handler())
		synced = func() {
			sendWake(wake)
			sendWake(specWake)
		}
	}
	watchCollection(ctx, client, informer.Source{Resource: receiverResource}, nil, handler, synced, readings.watchRestarted, store)
}

// bothHandlers calls first and then second for each change.
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
