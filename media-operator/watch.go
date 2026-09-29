package main

// A watch keeps this program's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing. The
// operator watches the collections its pass reads (clusterwatch.go),
// and the api role watches the named objects that hold its
// certificates (informer.WatchOne, with namedwatch.go).
//
// The shared informer package runs each watch on client-go's reflector.
// The program's own client (apiclient.go) still sends every write, and
// every read that must include this program's own last write.

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

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
	// defines, which a cluster may not have installed. The watch reads
	// the API server's 404 as an empty collection, and finds the
	// resource when it arrives (informer.Options.Absent).
	optional bool

	// handler, when it is not nil, takes each change the informer
	// reports. A watch with no handler only keeps its store current.
	handler cache.ResourceEventHandler
	// synced, when it is not nil, runs once, after the handler has
	// taken every object of the first read. It receives the view of the
	// informer's store, which holds that read and every change after it.
	synced func(informer.View)
	// reopened, when it is not nil, runs each time the API server
	// accepts a watch after the first one it accepted.
	reopened func()
}

// watchCollection keeps one collection current until the context ends.
// It returns after the last call to the handler and to synced, so a
// caller that closes a channel after it returns never has a send on
// the closed channel.
func watchCollection(ctx context.Context, client dynamic.Interface, w collectionWatch) {
	options := informer.Options{Handler: w.handler, Reopened: w.reopened, AbsentRecheck: optionalRecheck}
	if w.optional {
		options.Absent = apierrors.IsNotFound
	}
	// Synced can run before Start returns the collection, so it takes
	// the collection's view from a channel that Start's caller fills.
	started := make(chan informer.View, 1)
	if w.synced != nil {
		options.Synced = func() { w.synced(<-started) }
	}
	source := informer.Source{Resource: w.resource, Namespace: w.namespace, LabelSelector: w.labels, FieldSelector: w.fields}
	collection := informer.Start(ctx, client, source, options)
	started <- collection.View()
	<-collection.Done()
}

// optionalRecheck is how long the watch of an absent optional
// collection waits before it asks the API server again
// (informer.Options.AbsentRecheck). It is a variable so a test drives it
// in milliseconds.
var optionalRecheck = 5 * time.Minute

// poke never blocks, and the wake channel buffers exactly one. A
// wake already queued says everything a second one would say,
// because the pass that answers it reads the whole collection.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
