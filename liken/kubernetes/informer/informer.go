// Package informer keeps a copy of one Kubernetes collection current
// in memory, so a reconcile pass reads the copy instead of sending a
// request to the API server.
//
// The API server sends each change to a collection as it happens, on a
// watch, and a watch with no change to send costs nothing. client-go's
// reflector runs each watch. It reads the whole collection first, as a
// streaming list or as a plain list, and then watches from the version
// that read returned, so it receives every change made after the read.
// It resumes a watch that the API server closed from the last version
// it delivered, reads the collection again after a 410 Gone, and backs
// off while the API server fails. Upstream maintains and tests that
// loop, so liken keeps none of its own.
//
// This package imports only three parts of client-go: the reflector
// and the informer in tools/cache, the dynamic client that lists and
// watches any kind with no generated code, and rest for the in-cluster
// configuration. The typed clientset and the informer factories link a
// client and an informer for every built-in kind, which doubles the
// size of a binary. The shared apiclient.Client still sends
// every write, and every read that a copy cannot answer.
//
// This is a package of its own, apart from the kubernetes package,
// because the liken CLI imports the kubernetes package and watches
// nothing. A separate package keeps client-go out of the CLI.
package informer

import (
	"context"
	"fmt"
	"os"
	"sync"
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

// InCluster builds the dynamic client for the watches from the pod's
// ServiceAccount, the same credentials kubernetes.InClusterClient
// reads. base, when it is not empty, replaces the API server address
// that the environment names, for the reason InClusterClient gives:
// the machine operator runs on the host's network and has a better
// address than the Service's virtual IP.
func InCluster(base string) (dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	if base != "" {
		config.Host = base
	}
	return dynamic.NewForConfig(config)
}

// Source names one collection and the part of it to watch. A watch
// covers only the objects the operator reads, so the API server
// filters the rest and never sends them. An empty Namespace watches a
// cluster-scoped kind, or every namespace of a namespaced kind.
type Source struct {
	Resource      schema.GroupVersionResource
	Namespace     string
	LabelSelector string
	FieldSelector string
}

// String names the source in a log line.
func (s Source) String() string {
	name := s.Resource.Resource
	if s.Namespace != "" {
		name = s.Namespace + "/" + name
	}
	if s.FieldSelector != "" {
		name += " (" + s.FieldSelector + ")"
	}
	if s.LabelSelector != "" {
		name += " [" + s.LabelSelector + "]"
	}
	return name
}

// Options are the optional parts of a watch.
type Options struct {
	// Handler receives each change after the copy holds it. The
	// wake handlers in wake.go are the usual choice.
	Handler cache.ResourceEventHandler

	// Synced runs once, after the first read of the collection is in
	// the copy and the handler has taken each object of it. A pass
	// that started before then read the API server, and an edit made
	// between that read and the watch's first read is in no event.
	// A wake here makes the pass read again.
	Synced func()

	// Reopened runs each time the reflector opens a watch after its
	// first. The API server ends a watch on its own schedule, so a
	// low rate is normal, and a high rate says the stream breaks
	// faster than the watch can use it. The operators count these in
	// liken_watch_restarts_total.
	Reopened func()

	// Indexers name the indexes the copy keeps, for a pass that looks
	// objects up by a label instead of by name.
	Indexers cache.Indexers
}

// Collection is the in-memory copy of one watched collection.
//
// A nil *Collection is valid and has never synced. Every read on it
// answers that the copy cannot answer, and the caller reads the API
// server. The tests of a pass use that to run the pass against a fake
// API server with no watch at all.
type Collection struct {
	source     Source
	store      cache.Store
	controller cache.Controller
	done       chan struct{}

	// watching is true while the last watch the reflector opened was
	// accepted. Synced says why it matters.
	watching atomic.Bool

	// written holds this process's writes that the copy does not hold
	// yet (writes.go).
	written writes
}

// Start opens the watch and returns at once. The watch runs until the
// context ends. Done closes after the informer has stopped and the
// last handler call and the Synced call have returned, so a caller that
// closes a channel after Done never sends on a closed channel.
func Start(ctx context.Context, client dynamic.Interface, source Source, options Options) *Collection {
	var collection dynamic.ResourceInterface = client.Resource(source.Resource)
	if source.Namespace != "" {
		collection = client.Resource(source.Resource).Namespace(source.Namespace)
	}
	c := &Collection{source: source, done: make(chan struct{})}
	var opened atomic.Int64
	lister := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, list metav1.ListOptions) (runtime.Object, error) {
			list.LabelSelector = source.LabelSelector
			list.FieldSelector = source.FieldSelector
			return collection.List(ctx, list)
		},
		WatchFuncWithContext: func(ctx context.Context, list metav1.ListOptions) (watch.Interface, error) {
			list.LabelSelector = source.LabelSelector
			list.FieldSelector = source.FieldSelector
			w, err := collection.Watch(ctx, list)
			c.watching.Store(err == nil)
			// The first accepted watch is the streaming list of the
			// first read. Each accepted one after it is a watch opened
			// again. A refused watch opened nothing, so it does not
			// count.
			if err == nil && opened.Add(1) > 1 && options.Reopened != nil {
				options.Reopened()
			}
			return w, err
		},
	}
	c.store, c.controller = cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: lister,
		ObjectType:    &unstructured.Unstructured{},
		Handler:       c.written.handler(options.Handler),
		Transform:     dropManagedFields,
		Indexers:      options.Indexers,
	})
	go func() {
		defer close(c.done)
		var group sync.WaitGroup
		if options.Synced != nil {
			group.Go(func() {
				select {
				case <-c.controller.HasSyncedChecker().Done():
					options.Synced()
				case <-ctx.Done():
				}
			})
		}
		c.controller.RunWithContext(ctx)
		group.Wait()
	}()
	return c
}

// Done closes when the watch has stopped.
func (c *Collection) Done() <-chan struct{} { return c.done }

// Synced answers whether the copy holds the whole collection and the
// watch keeps it current. Until the first read is done, a read of the
// copy could miss an object that exists.
//
// A copy whose watch the API server refuses is not current, even
// after a list. The case is a release skew: a new binary under the
// previous release's RBAC, which grants list and not watch. The
// reflector then lists the collection again after each backoff, up to
// thirty seconds apart, and in between the copy holds no change at
// all. A pass that read it could judge a live machine by a heartbeat
// thirty seconds old. So the copy answers only while the last watch
// the reflector opened was accepted, and otherwise the pass reads the
// API server.
func (c *Collection) Synced() bool {
	return c != nil && c.controller.HasSynced() && c.watching.Load()
}

// Get reads one object from the copy by its key: the name of a
// cluster-scoped object, or namespace/name. ok is false when the copy
// cannot answer: it has not synced, it does not hold a write this
// process made to the object yet (Wrote), or the object does not
// convert (which is logged). The caller then reads the API server.
// found is false when the copy holds no such object.
func Get[T any](c *Collection, key string) (item *T, found, ok bool) {
	if !c.Synced() || c.written.pending(key) {
		return nil, false, false
	}
	object, exists, err := c.store.GetByKey(key)
	if err != nil {
		return nil, false, false
	}
	if !exists {
		return nil, false, true
	}
	out, err := Convert[T](object)
	if err != nil {
		report(c.source, err)
		return nil, false, false
	}
	return &out, true, true
}

// List reads every object in the copy. ok is false when the copy
// cannot answer, which includes a copy that does not hold a write this
// process made to any of its objects yet. The caller then reads the
// API server. An object
// that does not convert makes the whole answer unusable, because a
// pass that judges a collection must not judge it with an object
// missing.
func List[T any](c *Collection) (items []T, ok bool) {
	if !c.Synced() || c.written.any() {
		return nil, false
	}
	return convertAll[T](c.source, c.store.List())
}

// ByIndex reads the objects of one index value, the same way List
// reads the whole copy.
func ByIndex[T any](c *Collection, index, value string) (items []T, ok bool) {
	if !c.Synced() || c.written.any() {
		return nil, false
	}
	indexer, isIndexer := c.store.(cache.Indexer)
	if !isIndexer {
		return nil, false
	}
	objects, err := indexer.ByIndex(index, value)
	if err != nil {
		return nil, false
	}
	return convertAll[T](c.source, objects)
}

func convertAll[T any](source Source, objects []any) ([]T, bool) {
	items := make([]T, 0, len(objects))
	for _, object := range objects {
		item, err := Convert[T](object)
		if err != nil {
			report(source, err)
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

// LabelIndex is an index of the objects by the value of one label.
func LabelIndex(label string) cache.IndexFunc {
	return func(object any) ([]string, error) {
		item, ok := object.(*unstructured.Unstructured)
		if !ok {
			return nil, nil
		}
		value, set := item.GetLabels()[label]
		if !set {
			return nil, nil
		}
		return []string{value}, nil
	}
}

// dropManagedFields removes metadata.managedFields from each object
// before the informer stores it. The field records which client set
// each field of the object. No liken operator reads it, and without
// the transform the copy holds it for every object.
func dropManagedFields(object any) (any, error) {
	if item, ok := object.(*unstructured.Unstructured); ok {
		item.SetManagedFields(nil)
	}
	return object, nil
}

// Convert decodes one object from a watch into the operator's own
// struct. The informer hands a handler an *unstructured.Unstructured,
// or, for an object that was deleted while the watch was down, a
// tombstone that holds the last copy the informer had.
//
// An object that does not convert has a field whose type differs from
// the operator's struct, so the CRD schema and the struct disagree.
// The error names the object, and the caller logs it, because an
// object that is dropped with no word leaves nobody a way to find out
// why the operator ignored it.
func Convert[T any](object any) (T, error) {
	var out T
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		object = tombstone.Obj
	}
	item, ok := object.(*unstructured.Unstructured)
	if !ok {
		return out, fmt.Errorf("the watch delivered a %T, not an object", object)
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

// report logs an object that Convert refused.
func report(source Source, err error) {
	fmt.Fprintf(os.Stderr, "watching %s: %v\n", source, err)
}
