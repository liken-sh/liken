//go:build !pod

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
// Each informer holds the collection it watches, and a pass reads the
// collection from that copy instead of listing it from the API server.
// The informer writes a change into its copy before it calls the
// handler, so the pass that a change wakes reads that change. The
// Libraries, the Catalogs, and the MetadataProviders are read through
// objectcache.go, because the operator writes them and the copy can be
// older than its own write.
//
// The operator imports only three parts of client-go for the watches:
// the reflector and informer in tools/cache, the dynamic client that
// lists and watches a custom resource with no generated code, and rest
// for the in-cluster configuration. The typed clientset and the
// informer factories link a client for every built-in kind. The
// operator's own Client (apiclient.go) still sends every other read and
// every write a pass makes.
//
// This file is not in the pod build. The pods and Jobs the operator
// creates run the same program with the build tag pod, and none of
// them watches anything (operate_pod.go says why).

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// The eight collections this operator watches.
var (
	libraryResource          = mustGroupVersion(libraryAPIVersion).WithResource("libraries")
	catalogResource          = mustGroupVersion(libraryAPIVersion).WithResource("catalogs")
	metadataProviderResource = mustGroupVersion(metadataProviderAPIVersion).WithResource("metadataproviders")
	playerResource           = mustGroupVersion(playerAPIVersion).WithResource("players")
	mediaPreferencesResource = mustGroupVersion(playerAPIVersion).WithResource("mediapreferences")
	playResource             = mustGroupVersion(playerAPIVersion).WithResource("plays")
	personResource           = mustGroupVersion(personAPIVersion).WithResource("people")
	podResource              = schema.GroupVersionResource{Version: podAPIVersion, Resource: "pods"}
)

func mustGroupVersion(apiVersion string) schema.GroupVersion {
	version, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		panic(err)
	}
	return version
}

// watches holds the eight informers and answers the pass from them.
type watches struct {
	libraries         *collection[Library]
	catalogs          *collection[NamespaceCatalog]
	memberPods        *collection[Pod]
	players           *collection[Player]
	mediaPreferences  *collection[MediaPreferences]
	metadataProviders *collection[MetadataProvider]
	plays             *collection[Play]
	people            *collection[Person]

	// The client and the memos objectcache.go reads the Libraries, the
	// Catalogs, and the MetadataProviders with. The memos are the
	// operator's own, because the operator's writes note them.
	client   *Client
	versions objectVersions

	group sync.WaitGroup
}

// informer is what startWatches and settle need of one collection,
// whatever struct it decodes into.
type informer interface {
	run(ctx context.Context)
	settledOrEnded(ctx context.Context) error
}

// startWatches starts every informer. Each change a watch delivers
// sends a wake, and wait returns when ctx has ended and every informer
// has stopped.
//
// Two rules decide which change wakes the pass, because the pass reads
// two kinds of object:
//
//   - A Library, a Catalog, and a MetadataProvider are objects a person
//     declares and whose status only this operator writes. A change
//     wakes the pass when it changes the spec, the deletion mark, or the
//     object's identity. So this operator's own status writes, and its
//     own finalizer patches on a Library, wake no pass.
//   - A Player, MediaPreferences, a Play, a Person, and a catalog member
//     pod are objects other writers change: media-operator,
//     people-operator, and the kubelet. The pass reads their status, so a
//     change wakes the pass when any field this operator reads changed.
//     A field outside the operator's structs, such as a status field that
//     only media-operator reads, wakes nothing.
func startWatches(ctx context.Context, client dynamic.Interface, wake chan<- struct{}, m *metrics,
	reader *Client, versions objectVersions) *watches {
	w := &watches{
		client:   reader,
		versions: versions,
		libraries: newCollection(client, wake, m, "libraries", kindLibrary, libraryResource, "",
			edited(func(library *Library) *ObjectMeta { return &library.Metadata })),
		catalogs: newCollection(client, wake, m, "catalogs", kindCatalog, catalogResource, "",
			edited(func(catalog *NamespaceCatalog) *ObjectMeta { return &catalog.Metadata })),
		memberPods: newCollection(client, wake, m, "catalog member pods", kindPod, podResource, catalogMemberSelector,
			readChanged(func(pod *Pod) *ObjectMeta { return &pod.Metadata })),
		players: newCollection(client, wake, m, "players", kindPlayer, playerResource, "",
			readChanged(func(player *Player) *ObjectMeta { return &player.Metadata })),
		mediaPreferences: newCollection(client, wake, m, "media preferences", kindMediaPreferences,
			mediaPreferencesResource, "",
			readChanged(func(preferences *MediaPreferences) *ObjectMeta { return &preferences.Metadata })),
		metadataProviders: newCollection(client, wake, m, "metadata providers", kindMetadataProvider,
			metadataProviderResource, "",
			edited(func(provider *MetadataProvider) *ObjectMeta { return &provider.Metadata })),
		plays: newCollection(client, wake, m, "plays", kindPlay, playResource, "",
			readChanged(func(play *Play) *ObjectMeta { return &play.Metadata })),
		people: newCollection(client, wake, m, "people", kindPerson, personResource, "",
			readChanged(func(person *Person) *ObjectMeta { return &person.Metadata })),
	}
	for _, one := range w.all() {
		w.group.Go(func() { one.run(ctx) })
	}
	return w
}

// all is every informer, the three the operator needs first.
func (w *watches) all() []informer {
	return []informer{w.libraries, w.catalogs, w.memberPods,
		w.players, w.mediaPreferences, w.metadataProviders, w.plays, w.people}
}

// wait returns when every informer has stopped and made its last
// handler call.
func (w *watches) wait() { w.group.Wait() }

// settle waits until every collection has been read once, or has
// failed its first read. The pass acts on what it reads, so a first
// pass on a copy that is still empty would read every Library as gone.
//
// The Libraries, the Catalogs, and the member pods must be read: a
// failure there ends the operator, as a failed first list always has.
// The other five belong to operators a cluster may not run, and a
// collection nobody serves reads as empty until its informer reads it.
func (w *watches) settle(ctx context.Context) error {
	for index, one := range w.all() {
		err := one.settledOrEnded(ctx)
		if err == nil {
			continue
		}
		// An optional collection that has not answered within the bound
		// reads as not read yet, and its informer keeps trying, as a
		// failed list of it never ended the operator.
		if index < 3 {
			return err
		}
	}
	return nil
}

// A read of the Libraries or the Catalogs that fails ends the pass, the
// way a failed list of them always has.
func (w *watches) readLibraries(ctx context.Context) (*LibraryList, error) {
	if !w.libraries.view().ready() {
		items, err := w.libraries.items()
		return &LibraryList{Items: items}, err
	}
	items, err := currentList[Library](ctx, w.client,
		heldObjects{view: w.libraries.view(), versions: w.versions.libraries}, keyPath(libraryPath))
	return &LibraryList{Items: items}, err
}

func (w *watches) readCatalogs(ctx context.Context) (*CatalogList, error) {
	if !w.catalogs.view().ready() {
		items, err := w.catalogs.items()
		return &CatalogList{Items: items}, err
	}
	items, err := currentList[NamespaceCatalog](ctx, w.client,
		heldObjects{view: w.catalogs.view(), versions: w.versions.catalogs}, keyPath(catalogPath))
	return &CatalogList{Items: items}, err
}

func (w *watches) readMemberPods() (*PodList, error) {
	items, err := w.memberPods.items()
	return &PodList{Items: items}, err
}

func (w *watches) readPlayers() (*PlayerList, error) {
	items, err := w.players.items()
	return &PlayerList{Items: items}, err
}

func (w *watches) readMediaPreferences() (*MediaPreferencesList, error) {
	items, err := w.mediaPreferences.items()
	return &MediaPreferencesList{Items: items}, err
}

func (w *watches) readMetadataProviders(ctx context.Context) (*MetadataProviderList, error) {
	if !w.metadataProviders.view().ready() {
		items, err := w.metadataProviders.items()
		return &MetadataProviderList{Items: items}, err
	}
	items := currentOrStored[MetadataProvider](ctx, w.client,
		heldObjects{view: w.metadataProviders.view(), versions: w.versions.providers}, keyPath(metadataProviderPath))
	return &MetadataProviderList{Items: items}, nil
}

func (w *watches) readPlays() (*PlayList, error) {
	items, err := w.plays.items()
	return &PlayList{Items: items}, err
}

func (w *watches) readPeople() (*PersonList, error) {
	items, err := w.people.items()
	return &PersonList{Items: items}, err
}

// collection is one informer, and the state of its first read.
type collection[T any] struct {
	// what is the plural a log line names.
	what string
	// changed answers whether an update from before to after wakes the
	// pass.
	changed func(before, after T) bool

	store    cache.Store
	informer cache.Controller

	// settled closes when the first read has succeeded or has failed.
	// failure holds the last failed read, until a read succeeds.
	settled    chan struct{}
	settleOnce sync.Once
	mu         sync.Mutex
	failure    error
}

// newCollection builds one informer and starts nothing. A label
// selector, when it is not empty, narrows the list and the watch to the
// objects that carry that label. The watch of a namespaced resource
// covers every namespace.
func newCollection[T any](client dynamic.Interface, wake chan<- struct{}, m *metrics,
	what, kind string, resource schema.GroupVersionResource, selector string,
	changed func(before, after T) bool) *collection[T] {
	c := &collection[T]{what: what, changed: changed, settled: make(chan struct{})}
	source := client.Resource(resource)
	var opened atomic.Int64
	lister := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = selector
			list, err := source.List(ctx, options)
			if err != nil {
				c.failed(err)
			}
			return list, err
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = selector
			// Every watch after the first is one the reflector opened
			// again, after the API server closed one or a read failed.
			if opened.Add(1) > 1 {
				m.recordWatchRestart(kind)
			}
			return source.Watch(ctx, options)
		},
	}
	signal := func() { poke(wake) }
	c.store, c.informer = cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: lister,
		ObjectType:    &unstructured.Unstructured{},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    func(object any) { c.added(object, signal) },
			UpdateFunc: func(before, after any) { c.updated(before, after, signal) },
			DeleteFunc: func(object any) { c.removed(object, signal) },
		},
		Transform: dropManagedFields,
	})
	return c
}

// run keeps the collection current until ctx ends.
func (c *collection[T]) run(ctx context.Context) {
	var group sync.WaitGroup
	group.Go(func() {
		select {
		case <-c.informer.HasSyncedChecker().Done():
			c.succeeded()
		case <-ctx.Done():
		}
	})
	// RunWithContext returns after the informer's last handler call, and
	// the goroutine above ends with ctx, so run returns only after both.
	c.informer.RunWithContext(ctx)
	group.Wait()
}

// failed records a read that failed, and settles the collection if it
// has not been read yet.
func (c *collection[T]) failed(err error) {
	c.mu.Lock()
	c.failure = err
	c.mu.Unlock()
	c.settleOnce.Do(func() { close(c.settled) })
}

// succeeded clears a failure once the informer has read the whole
// collection.
func (c *collection[T]) succeeded() {
	c.mu.Lock()
	c.failure = nil
	c.mu.Unlock()
	c.settleOnce.Do(func() { close(c.settled) })
}

// settledOrEnded waits for the first read. It answers the read's error,
// or ctx's error when ctx ends first.
func (c *collection[T]) settledOrEnded(ctx context.Context) error {
	select {
	case <-c.settled:
	case <-ctx.Done():
		return fmt.Errorf("reading the %s: %w", c.what, ctx.Err())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil && !c.informer.HasSynced() {
		return c.failure
	}
	return nil
}

// view is the informer's store, for objectcache.go.
func (c *collection[T]) view() storeView {
	return storeView{store: c.store, synced: c.informer.HasSynced}
}

// items answers every object the informer holds, in namespace and name
// order, the order a list from the API server takes. Before the first
// read succeeds it answers that read's error. An object that does not
// convert fails the whole read, the way one object that does not decode
// fails a list, so a pass never reads a Library that is there as one
// that is gone.
func (c *collection[T]) items() ([]T, error) {
	if !c.informer.HasSynced() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.failure != nil {
			return nil, c.failure
		}
		return nil, fmt.Errorf("the %s have not been read yet", c.what)
	}
	keys := c.store.ListKeys()
	slices.Sort(keys)
	items := make([]T, 0, len(keys))
	for _, key := range keys {
		object, held, err := c.store.GetByKey(key)
		if err != nil || !held {
			continue
		}
		item, err := convert[T](object)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// added wakes the pass for a new object. The informer also reports the
// first read of the collection this way, one object at a time, and a
// read after a gap in the watch as the differences from what it held.
func (c *collection[T]) added(object any, wake func()) {
	if _, err := convert[T](object); err != nil {
		reportUnconverted(c.what, err)
	}
	wake()
}

// removed wakes the pass for an object the API server removed.
func (c *collection[T]) removed(object any, wake func()) {
	if _, err := convert[T](object); err != nil {
		reportUnconverted(c.what, err)
	}
	wake()
}

// updated wakes the pass when the collection's rule says the change is
// one the pass acts on. A copy that does not convert is reported, and
// the change wakes the pass, because nothing says what it changed.
func (c *collection[T]) updated(before, after any, wake func()) {
	now, err := convert[T](after)
	if err != nil {
		reportUnconverted(c.what, err)
		wake()
		return
	}
	was, err := convert[T](before)
	if err != nil || c.changed(was, now) {
		wake()
	}
}

// edited is the rule for an object a person declares and only this
// operator writes the status of. metadata.generation counts the changes
// to the spec, and a write to the status subresource or to the metadata
// does not raise it. The deletion mark starts a departure. The UID is in
// the compare because an object that somebody deleted and created again
// with the same name during a gap in the watch reaches the handler as an
// update, and the new object can have the old one's generation.
func edited[T any](meta func(*T) *ObjectMeta) func(before, after T) bool {
	return func(before, after T) bool {
		was, now := meta(&before), meta(&after)
		return was.UID != now.UID || was.Generation != now.Generation || was.deleting() != now.deleting()
	}
}

// readChanged is the rule for an object another writer changes. The
// resourceVersion changes with every write, so the compare leaves it
// out, and what is left is every field this operator reads.
func readChanged[T any](meta func(*T) *ObjectMeta) func(before, after T) bool {
	return func(before, after T) bool {
		meta(&before).ResourceVersion = ""
		meta(&after).ResourceVersion = ""
		return !reflect.DeepEqual(before, after)
	}
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
// tombstone that holds the last copy the informer knew. A tombstone can
// hold no copy at all.
//
// An object that does not convert has a field whose type differs from
// the operator's struct, so the CRD schema and the struct disagree. The
// error names the object, and the caller logs it, because an object
// that is dropped with no word leaves nobody a way to find out why the
// operator ignored an edit.
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

// reportUnconverted logs an object that convert refused.
func reportUnconverted(what string, err error) {
	fmt.Fprintf(os.Stderr, "watching the %s: %v\n", what, err)
}
