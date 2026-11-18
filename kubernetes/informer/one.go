package informer

// WatchOne follows one object by name, such as a CA ConfigMap or a
// serving Secret, and passes each version of it to a callback. A pod
// that serves TLS uses it to switch to a rotated certificate as soon as
// the watch receives it.
//
// The watch lists and watches the collection with the field selector
// metadata.name=<name>. The API server reads the name from that
// selector when it checks the request against a Role's resourceNames,
// so a Role that lists only this object permits the list and the watch.
// A GET on the object's own path with watch=true does not work, because
// the API server handles it as a plain get: it returns the object once
// and ends the response.

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// One identifies the object that WatchOne follows.
type One struct {
	Resource  schema.GroupVersionResource
	Namespace string
	Name      string

	// What describes the object in the log line for a version that
	// does not convert, such as "the Secret display-api-tls".
	What string
}

// WatchOne calls deliver with the object each time it changes, and with
// nil when the object does not exist. It returns at once, and the watch
// runs until the context ends. synced, when it is not nil, runs once
// after deliver has received the result of the first read, whether the
// read found the object or not. The Done channel of the returned
// Collection closes after the last call to deliver and synced has
// returned.
//
// When a version of the object does not convert to T, WatchOne logs it
// and does not call deliver, so the caller keeps the last good version
// until a later version converts. A caller that parses the object
// itself can use unstructured.Unstructured for T, which every version
// converts to.
func WatchOne[T any](ctx context.Context, client dynamic.Interface, one One, deliver func(object *T), synced func()) *Collection {
	report := &oneReport[T]{what: one.What, deliver: deliver}
	// The Synced callback can run before Start returns the
	// collection, so it waits to receive the store from this
	// channel.
	started := make(chan cache.Store, 1)
	source := Source{Resource: one.Resource, Namespace: one.Namespace, FieldSelector: "metadata.name=" + one.Name}
	collection := Start(ctx, client, source, Options{
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    report.onChange,
			UpdateFunc: func(_, object any) { report.onChange(object) },
			DeleteFunc: func(any) { report.onDelete() },
		},
		Synced: func() {
			store := <-started
			report.firstReadDone(func() bool { return len(store.ListKeys()) == 0 })
			if synced != nil {
				synced()
			}
		},
	})
	started <- collection.store
	return collection
}

// oneReport converts the informer's events into calls to deliver.
//
// An object that does not exist at the first read produces no event, so
// firstReadDone calls deliver(nil) itself. The informer calls the
// handler on one goroutine and Synced on another, so mu serializes
// them. firstReadDone delivers nil only when both are true:
//
//   - No event has reached the handler. An object whose version does not
//     convert still exists, and a deletion was already delivered as nil.
//   - The store is empty. The informer stores an object before it calls
//     the handler, so an object created just after the first read can
//     be in the store before its event arrives. A nil then would report
//     that an existing object does not exist.
//
// So an object created after an empty first read is always delivered
// last: either its event runs after the nil, or firstReadDone finds it
// in the store and delivers nothing.
type oneReport[T any] struct {
	what    string
	deliver func(object *T)

	mu        sync.Mutex
	eventSeen bool
}

func (r *oneReport[T]) onChange(object any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventSeen = true
	converted, err := Convert[T](object)
	if err != nil {
		Report(r.what, err)
		return
	}
	r.deliver(&converted)
}

func (r *oneReport[T]) onDelete() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventSeen = true
	r.deliver(nil)
}

// firstReadDone calls empty while it holds the lock, so no event is
// handled between the check of the store and the call to deliver.
func (r *oneReport[T]) firstReadDone(empty func() bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.eventSeen && empty() {
		r.deliver(nil)
	}
}
