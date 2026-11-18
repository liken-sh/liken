package main

// The operator keeps its view of the cluster current with watches, not
// a timer, so an idle collection costs the API server no requests.
// informer.Start runs each watch on client-go's reflector. Writes, and
// reads that must see the operator's own last write, go through the
// client in apiclient.go.

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// collectionWatch describes one watch: which objects it covers and what
// it runs for each change.
type collectionWatch struct {
	resource schema.GroupVersionResource
	// namespace is empty to watch every namespace, and for a
	// cluster-scoped resource.
	namespace string
	// labels and fields are selectors that the API server applies to
	// both the list and the watch.
	labels string
	fields string

	// optional marks a resource that another operator defines and that
	// the cluster may not have. The watch treats the API server's 404 as
	// an empty collection and lists again every optionalRecheck.
	optional bool

	// handler receives each add, update, and delete. When it is nil, the
	// watch only keeps the informer's store current.
	handler cache.ResourceEventHandler
	// synced runs once, after the handler has received every object of
	// the first list. It receives a view of the informer's store, which
	// stays current with later changes.
	synced func(informer.View)
	// reopened runs each time the API server accepts a watch after the
	// first one.
	reopened func()
}

// watchCollection watches one collection until ctx ends. It returns
// only after the last call to the handler and to synced, so a caller
// can close a channel that those callbacks send on as soon as it
// returns.
func watchCollection(ctx context.Context, client dynamic.Interface, w collectionWatch) {
	options := informer.Options{Handler: w.handler, Reopened: w.reopened, AbsentRecheck: optionalRecheck}
	if w.optional {
		options.Absent = apierrors.IsNotFound
	}
	// Options.Synced can run before informer.Start returns, so it reads
	// the view from a channel that is filled after Start returns.
	started := make(chan informer.View, 1)
	if w.synced != nil {
		options.Synced = func() { w.synced(<-started) }
	}
	source := informer.Source{Resource: w.resource, Namespace: w.namespace, LabelSelector: w.labels, FieldSelector: w.fields}
	collection := informer.Start(ctx, client, source, options)
	started <- collection.View()
	<-collection.Done()
}

// optionalRecheck is how often the watch of an absent optional
// collection lists it again. The resource appears only when someone
// installs the operator that defines it, so finding it a few minutes
// late is acceptable.
const optionalRecheck = 5 * time.Minute

// signalWake wakes the pass without blocking. When the channel's one buffered
// value is already there, signalWake drops the new one, because the pass that
// it starts reads every collection.
func signalWake(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
