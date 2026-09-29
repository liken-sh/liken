package informer

// The watch of one object by name, such as a CA ConfigMap or a serving
// Secret. An API pod follows each of them and hands every version of
// the object to its owner, so a rotated certificate reaches the next
// TLS handshake when the API server writes it.
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

// One names the object a watch follows.
type One struct {
	Resource  schema.GroupVersionResource
	Namespace string
	Name      string

	// What names the object in the line that reports a copy that does
	// not convert, such as "the Secret display-api-tls".
	What string
}

// WatchOne calls seen with the object each time it changes, and with
// nil when the object does not exist. It returns at once, and the watch
// runs until the context ends. synced, when it is not nil, runs once
// after seen has taken the first read, whether that read found the
// object or not. Done on the answer closes after the last call to seen
// and to synced has returned.
//
// A copy that does not convert is reported, and seen does not run, so
// the owner keeps what it holds: the next version of the object can be
// valid. An owner that reads the object itself takes T as
// unstructured.Unstructured, which every copy converts to.
func WatchOne[T any](ctx context.Context, client dynamic.Interface, one One, seen func(held *T), synced func()) *Collection {
	report := &oneReport[T]{what: one.What, seen: seen}
	// Synced can run before Start returns the collection, so it takes
	// the store from a channel that fills after Start returns.
	started := make(chan cache.Store, 1)
	source := Source{Resource: one.Resource, Namespace: one.Namespace, FieldSelector: "metadata.name=" + one.Name}
	collection := Start(ctx, client, source, Options{
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    report.take,
			UpdateFunc: func(_, object any) { report.take(object) },
			DeleteFunc: func(any) { report.gone() },
		},
		Synced: func() {
			store := <-started
			report.firstRead(func() bool { return len(store.ListKeys()) == 0 })
			if synced != nil {
				synced()
			}
		},
	})
	started <- collection.store
	return collection
}

// oneReport turns the informer's calls into the owner's reports.
//
// An object absent from the first read has no event, so the watch
// reports it as nil once that read is done. The informer calls the
// handler on one goroutine and the end of the first read on another,
// and the lock runs one report at a time. The report of absence waits
// for both conditions:
//
//   - No event reached the handler. An object that does not convert
//     still exists, and an object that the read found and a later event
//     deleted was reported already, so neither gets a report of its
//     own.
//   - The store is empty. The informer adds an object to its store
//     before it calls the handler, so an object that arrived just after
//     the read is in the store before its event reaches the owner. A
//     report of absence would say the object does not exist while it
//     does.
//
// An object that arrives after an empty first read is reported last in
// either order: its event runs after the report of absence, or the
// report finds the object in the store and reports nothing.
type oneReport[T any] struct {
	what string
	seen func(held *T)

	mu      sync.Mutex
	arrived bool
}

func (r *oneReport[T]) take(object any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.arrived = true
	held, err := Convert[T](object)
	if err != nil {
		Report(r.what, err)
		return
	}
	r.seen(&held)
}

func (r *oneReport[T]) gone() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.arrived = true
	r.seen(nil)
}

// firstRead reports absence at the end of the first read. empty reads
// the store under the lock, so no event runs between that read and the
// report.
func (r *oneReport[T]) firstRead(empty func() bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.arrived && empty() {
		r.seen(nil)
	}
}
