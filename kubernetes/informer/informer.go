// Package informer keeps an operator's copy of a Kubernetes collection
// current, and answers a pass's reads from that copy.
//
// The API server sends each change to a collection as it happens, on a
// watch, and a watch with no change to send costs nothing. client-go's
// reflector runs each watch. It reads the whole collection first, as a
// streaming list or as a plain list, and then watches from the version
// that read returned, so it receives every change made after the read.
// It resumes a watch that the API server closed from the last version
// it delivered, reads the collection again after a 410 Gone, and backs
// off while the API server fails. Upstream maintains and tests that
// loop, so no operator keeps one of its own.
//
// The package imports only three parts of client-go: the reflector and
// the informer in tools/cache, the dynamic client that lists and
// watches any kind with no generated code, and rest for the in-cluster
// configuration. The typed clientset and the informer factories link a
// client and an informer for every built-in kind, which doubles the
// size of a binary. The apiclient package sends every write, and every
// read that the copy does not answer (cache.go).
package informer

import (
	"context"
	"sync"
	"sync/atomic"
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

// InCluster builds the dynamic client for the watches from the pod's
// ServiceAccount, the same credentials apiclient.InCluster reads.
func InCluster() (dynamic.Interface, error) {
	return InClusterAt("")
}

// InClusterAt is InCluster with the API server's address in place of
// the one the environment names, for the reason that
// apiclient.InClusterOptions.Server gives. An empty server means the
// environment's address.
func InClusterAt(server string) (dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	if server != "" {
		config.Host = server
	}
	return dynamic.NewForConfig(config)
}

// Source names one collection and the part of it to watch. A watch
// covers only the objects the operator reads, so the API server filters
// the rest and never sends them. An empty Namespace watches a
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
	// Handler receives each change after the copy holds it. A nil
	// Handler receives nothing, for a watch that only keeps a copy for
	// the pass to read.
	Handler cache.ResourceEventHandler

	// Synced runs once, after the first read of the collection is in
	// the copy and the handler has taken each object of it. A pass that
	// started before then read the API server, and an edit made between
	// that read and the watch's first read is in no event. A wake here
	// makes the pass read again.
	Synced func()

	// Reopened runs each time the API server accepts a watch after the
	// first one it accepted. The API server ends a watch on its own
	// schedule, so a low rate is normal, and a high rate says the stream
	// breaks faster than the watch can use it. A refused watch opened
	// nothing, so it does not count, and neither does the quiet stream
	// of an absent collection.
	Reopened func()

	// Absent, when it is not nil, names the refusals that mean the
	// collection is not there to watch, such as the 404 of a kind whose
	// definition another operator installs. The copy then holds an empty
	// collection, and the watch finds the collection when it arrives
	// (absent.go).
	Absent func(error) bool

	// AbsentRecheck is how long the watch of an absent collection waits
	// before it asks the API server again. Zero means five minutes.
	AbsentRecheck time.Duration

	// ListFailed, when it is not nil, runs each time a list of the whole
	// collection fails, with the error. An operator that waits for its
	// first read uses it to stop waiting for a read that fails, such as
	// the list of a kind the cluster does not serve.
	ListFailed func(error)

	// Transform, when it is not nil, trims each object before the copy
	// holds it, after the managedFields are gone. A field that no pass
	// reads costs memory for every object of the kind, such as the list
	// of images a Node's runtime holds.
	Transform func(*unstructured.Unstructured)

	// Indexers name the indexes the copy keeps, for a pass that reads
	// the objects of one label value without a scan of the whole copy.
	// The store of a watch with indexers is a cache.Indexer.
	Indexers cache.Indexers
}

// Collection is the copy of one watched collection.
//
// A nil *Collection is valid and never syncs. Its View answers nothing,
// and each read goes to the API server.
type Collection struct {
	store      cache.Store
	controller cache.Controller
	done       chan struct{}

	// watching is true after the API server accepts a watch, and false
	// again after it forbids one. Synced says why it matters.
	watching atomic.Bool
}

// noteWatch records whether the API server grants the watch. An
// accepted watch grants it, and a 401 or a 403 refuses it. Any other
// failure, such as a refused connection while the API server restarts,
// or a 5xx, says nothing about the permission, so the flag keeps its
// last value. The dynamic client answers each refusal as a
// *errors.StatusError, which carries the HTTP status.
func (c *Collection) noteWatch(err error) {
	switch {
	case err == nil:
		c.watching.Store(true)
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		c.watching.Store(false)
	}
}

// Start opens the watch and returns at once. The watch runs until the
// context ends. Done closes after the informer has stopped and the last
// handler call and the Synced call have returned, so a caller that
// closes a channel after Done never sends on a closed channel.
func Start(ctx context.Context, client dynamic.Interface, source Source, options Options) *Collection {
	var collection dynamic.ResourceInterface = client.Resource(source.Resource)
	if source.Namespace != "" {
		collection = client.Resource(source.Resource).Namespace(source.Namespace)
	}
	c := &Collection{done: make(chan struct{})}
	scope := func(list *metav1.ListOptions) {
		list.LabelSelector = source.LabelSelector
		list.FieldSelector = source.FieldSelector
	}
	var opened atomic.Int64
	absent := options.absence()
	lister := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, list metav1.ListOptions) (runtime.Object, error) {
			scope(&list)
			items, err := absent.list(collection.List(ctx, list))
			if err != nil && options.ListFailed != nil {
				options.ListFailed(err)
			}
			return items, err
		},
		WatchFuncWithContext: func(ctx context.Context, list metav1.ListOptions) (watch.Interface, error) {
			scope(&list)
			if quiet, ok := absent.watch(ctx, list); ok {
				// The quiet stream stands in for an accepted watch of an
				// empty collection, so the empty copy answers a pass. It
				// is no watch the API server accepted, so Reopened does
				// not count it.
				c.noteWatch(nil)
				return quiet, nil
			}
			stream, err := collection.Watch(ctx, list)
			c.noteWatch(err)
			// The first accepted watch is the streaming list of the first
			// read, or the watch after a plain list.
			if err == nil && opened.Add(1) > 1 && options.Reopened != nil {
				options.Reopened()
			}
			return stream, err
		},
	}
	handler := options.Handler
	if handler == nil {
		handler = cache.ResourceEventHandlerFuncs{}
	}
	c.store, c.controller = cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: lister,
		ObjectType:    &unstructured.Unstructured{},
		Handler:       handler,
		Transform:     trim(options.Transform),
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
// A copy whose watch the API server forbids is not current, even after
// a list. The case is a release skew: a new binary under the previous
// release's RBAC, which grants list and not watch. The reflector then
// lists the collection again after each backoff, which grows to between
// thirty and sixty seconds, and in between the copy holds no change at
// all. So after a 401
// or a 403 on a watch, the copy does not answer and the pass reads the
// API server, until the API server accepts a watch again.
//
// A watch that fails for another reason does not stop the copy. While
// the API server is down, a read of the API server fails too, and the
// copy lets an operator keep its local work going. The reflector
// resumes the watch from the copy's last version when the API server
// returns, or lists again, so the copy then receives each change it
// missed.
func (c *Collection) Synced() bool {
	return c != nil && c.controller.HasSynced() && c.watching.Load()
}

// View answers the copy for a pass to read (cache.go).
func (c *Collection) View() View {
	if c == nil {
		return View{}
	}
	return View{Store: c.store, Synced: c.Synced}
}

// trim answers the transform that removes metadata.managedFields from
// each object before the informer stores it, and then runs the
// operator's own. The field records which client set each field of the
// object. No operator reads it, and without the transform the copy
// holds it for every object.
func trim(operators func(*unstructured.Unstructured)) cache.TransformFunc {
	return func(object any) (any, error) {
		if item, ok := object.(*unstructured.Unstructured); ok {
			item.SetManagedFields(nil)
			if operators != nil {
				operators(item)
			}
		}
		return object, nil
	}
}
