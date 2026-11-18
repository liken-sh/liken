// Package informer keeps an operator's store of a Kubernetes collection
// current, and serves a reconcile pass's reads from that store.
//
// client-go's reflector runs each watch. It first reads the whole
// collection, as a streaming list or a plain list, and then watches
// from the resourceVersion that the read returned, so it receives every
// change made after the read. When the API server closes a watch, the
// reflector resumes from the last version it received. After a 410 Gone
// it reads the collection again, and it backs off while the API server
// fails. Upstream maintains and tests that loop, so no operator writes
// its own.
//
// The package imports only these parts of client-go: the reflector and
// informer in tools/cache, the dynamic client, which lists and watches
// any kind without generated code, and rest for the in-cluster
// configuration. The typed clientset and the informer factories link a
// client and an informer for every built-in kind, which makes a binary
// much larger. The apiclient package sends every write, and every
// read that the store cannot serve (see cache.go).
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

// InClusterAt is InCluster with an explicit API server address, for the
// reason given at apiclient.InClusterOptions.Server. An empty server
// uses the address from the pod's environment.
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

// Source is the collection to watch. The API server applies the
// selectors, so it never sends an object that they exclude. An empty
// Namespace watches a cluster-scoped kind, or every namespace of a
// namespaced kind.
type Source struct {
	Resource      schema.GroupVersionResource
	Namespace     string
	LabelSelector string
	FieldSelector string
}

// String describes the source for a log line.
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

// Options are the optional settings of a watch.
type Options struct {
	// Handler receives each change after the store is updated with
	// it. It can be nil when the pass only reads the store.
	Handler cache.ResourceEventHandler

	// Synced runs once, after the first read of the collection is
	// in the store and the handler has received each object of it.
	// A pass that runs before then reads the API server, and an
	// object that changes between that pass's read and the watch's
	// first read produces no update event. So Synced should start a
	// pass again.
	Synced func()

	// Reopened runs each time the API server accepts a watch after
	// the first, so a caller can count reconnections. The API
	// server closes watches on its own schedule, so an occasional
	// reopen is normal, and frequent ones mean that the connection
	// keeps breaking. Reopened does not run for a refused watch, or
	// for the empty watch that absent.go returns for an absent
	// collection.
	Reopened func()

	// Recovered runs when a watch is accepted after the previous
	// attempt to watch failed, such as at the end of an API server
	// outage. At that moment Synced can be true while the reflector
	// has not yet received the changes made during the outage. So a
	// pass that Recovered starts should read the API server, not
	// the store.
	Recovered func()

	// Absent, when it is not nil, reports whether a list error
	// means that the collection does not exist, such as the 404 for
	// a kind whose CRD another operator installs. The store is then
	// empty, and the watch lists the collection again after
	// AbsentRecheck (see absent.go).
	Absent func(error) bool

	// AbsentRecheck is how long the watch of an absent collection
	// waits before it lists the collection again. Zero means five
	// minutes.
	AbsentRecheck time.Duration

	// ListFailed, when it is not nil, runs with the error each time
	// a list of the whole collection fails. An operator that waits
	// for the first read can use it to stop waiting when that read
	// fails, such as the list of a kind the cluster does not serve.
	ListFailed func(error)

	// Transform, when it is not nil, edits each object before the
	// informer stores it, after metadata.managedFields is removed.
	// Use it to drop fields that no pass reads, such as a Node's
	// list of images, because otherwise the store keeps them in
	// memory for every object of the kind.
	Transform func(*unstructured.Unstructured)

	// UnreadyOnWatchError, when it is true, makes Synced return
	// false after any failed watch, not only after a 401 or a 403,
	// until the API server accepts a watch again. While the API
	// server restarts, the reflector backs off for up to a minute,
	// and a write made in that time is missing from the store. Set
	// it when a pass must never act on such a store, so the pass
	// reads the API server instead. liken's operators set it,
	// because a machine that acted on a store older than a
	// withdrawn rollout could stage the old target and reboot into
	// it.
	UnreadyOnWatchError bool

	// Indexers define indexes of the store, so a pass can read the
	// objects with one index value, such as one label value,
	// without scanning the whole store. With indexers, View().Store
	// is a cache.Indexer.
	Indexers cache.Indexers
}

// Collection is the store of one watched collection.
//
// A nil *Collection is valid and is never Synced. Its View serves
// nothing, so each read goes to the API server.
type Collection struct {
	store      cache.Store
	controller cache.Controller
	done       chan struct{}

	// watching is true after the API server accepts a watch, and
	// false after it refuses one with a 401 or a 403, or after any
	// failed watch when unreadyOnWatchError is true.
	watching atomic.Bool

	unreadyOnWatchError bool

	// absent is true while the last list failed with an error that
	// Options.Absent matched, so the store is empty because the
	// cluster does not serve the kind.
	absent atomic.Bool
}

// noteWatch sets c.watching from the result of opening a watch. A
// success sets it, and a 401 or a 403 clears it. Any other failure,
// such as a refused connection while the API server restarts or a 5xx,
// does not show whether the watch is permitted, so the flag keeps its
// value unless Options.UnreadyOnWatchError is set.
func (c *Collection) noteWatch(err error) {
	switch {
	case err == nil:
		c.watching.Store(true)
	case c.unreadyOnWatchError, apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		c.watching.Store(false)
	}
}

// Start opens the watch and returns at once. The watch runs until the
// context ends. Done closes after the informer has stopped and the last
// Handler and Synced calls have returned, so a caller can close a
// channel that those callbacks send on once Done has closed.
func Start(ctx context.Context, client dynamic.Interface, source Source, options Options) *Collection {
	var collection dynamic.ResourceInterface = client.Resource(source.Resource)
	if source.Namespace != "" {
		collection = client.Resource(source.Resource).Namespace(source.Namespace)
	}
	c := &Collection{done: make(chan struct{}), unreadyOnWatchError: options.UnreadyOnWatchError}
	scope := func(list *metav1.ListOptions) {
		list.LabelSelector = source.LabelSelector
		list.FieldSelector = source.FieldSelector
	}
	var opened atomic.Int64
	// failed is true after a failed watch, so the next accepted
	// watch calls Options.Recovered.
	var failed atomic.Bool
	accepted := func() {
		if failed.Swap(false) && options.Recovered != nil {
			options.Recovered()
		}
	}
	absent := options.absence()
	lister := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, list metav1.ListOptions) (runtime.Object, error) {
			scope(&list)
			listed, err := collection.List(ctx, list)
			c.absent.Store(absent.is(err))
			items, err := absent.list(listed, err)
			if err != nil && options.ListFailed != nil {
				options.ListFailed(err)
			}
			return items, err
		},
		WatchFuncWithContext: func(ctx context.Context, list metav1.ListOptions) (watch.Interface, error) {
			scope(&list)
			if quiet, ok := absent.watch(ctx, list); ok {
				// absent.go returns a watch with no
				// events for an absent collection, so
				// the empty store is Synced and a pass
				// can read it. The API server did not
				// accept this watch, so it does not
				// count toward Reopened.
				c.noteWatch(nil)
				accepted()
				return quiet, nil
			}
			stream, err := collection.Watch(ctx, list)
			c.noteWatch(err)
			if err != nil {
				failed.Store(true)
			} else {
				// An accepted watch shows that the
				// collection exists. A streaming list
				// makes no call to List, so this is the
				// place that clears absent after a
				// streaming list.
				c.absent.Store(false)
				accepted()
			}
			// The first accepted watch is part of the first
			// read: it is the streaming list, or the watch
			// that follows a plain list. Each later accepted
			// watch is a reopen.
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

// Done returns a channel that closes when the watch has stopped.
func (c *Collection) Done() <-chan struct{} { return c.done }

// Synced reports whether the store has the whole collection and a
// watch keeps it current. Before the first read finishes, the store
// could lack an object that exists.
//
// A store is not current when the API server forbids its watch, even
// after a list. This happens, for example, when a new binary runs under
// the previous release's RBAC, which grants list and not watch. The
// reflector then lists the collection again after each backoff, which
// grows to between thirty and sixty seconds, and the store receives no
// change in between.
// So after a 401 or a 403 on a watch, Synced is false and the pass
// reads the API server, until the API server accepts a watch again.
//
// A watch that fails for another reason does not make Synced false,
// unless Options.UnreadyOnWatchError is set. While the
// API server is down, reads from it fail too, so the operator keeps
// working from the store. When the API server returns,
// the reflector resumes the watch from the store's last version, or
// lists again, so the store receives each change it missed.
func (c *Collection) Synced() bool {
	return c != nil && c.controller.HasSynced() && c.watching.Load()
}

// View returns the store in the form a pass reads it (see cache.go).
func (c *Collection) View() View {
	if c == nil {
		return View{}
	}
	return View{Store: c.store, Synced: c.Synced, Absent: c.absent.Load()}
}

// trim returns the transform that removes metadata.managedFields from
// each object before the informer stores it, and then calls custom if
// it is not nil. The field records which client set each field. No
// operator reads it, and without the transform every object in the
// store would keep it.
func trim(custom func(*unstructured.Unstructured)) cache.TransformFunc {
	return func(object any) (any, error) {
		if item, ok := object.(*unstructured.Unstructured); ok {
			item.SetManagedFields(nil)
			if custom != nil {
				custom(item)
			}
		}
		return object, nil
	}
}
