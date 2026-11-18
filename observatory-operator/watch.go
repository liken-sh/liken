package main

// The operator watches each kind of its API group across all
// namespaces, and the pods, Services, ConfigMaps, and Jobs that it
// created, selected by its managed-by label. The watches run on
// client-go's reflector through the informer package. An event only
// wakes the goroutines that wait for a change: each namespace's
// supervisor and status writer, and each Reservation's runner. Each of
// them then reads a snapshot of the stores.

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// changeNotifier wakes every goroutine that waits for a change. notify closes the
// current channel and creates a new one. A waiter that got the channel
// before the change wakes up, and a waiter that gets it after the
// change waits for the next one.
type changeNotifier struct {
	mu sync.Mutex
	ch chan struct{}
	// also is notified each time this notifier is notified, when it is
	// not nil.
	also *changeNotifier
}

func newChangeNotifier(also *changeNotifier) *changeNotifier {
	return &changeNotifier{ch: make(chan struct{}), also: also}
}

func (s *changeNotifier) notify() {
	s.mu.Lock()
	close(s.ch)
	s.ch = make(chan struct{})
	s.mu.Unlock()
	if s.also != nil {
		s.also.notify()
	}
}

func (s *changeNotifier) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ch
}

func kindResource(kind observatory.Kind) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: observatory.Group, Version: observatory.Version, Resource: kind.Plural}
}

var (
	podsResource       = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	servicesResource   = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	configMapsResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	jobsResource       = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
)

// stores holds the cache of each watched collection.
type stores struct {
	kinds      map[observatory.Kind]*informer.Collection
	pods       *informer.Collection
	services   *informer.Collection
	configMaps *informer.Collection
	jobs       *informer.Collection
	// synced counts the watches that have finished their first list.
	synced atomic.Int32
	// version counts the changes to the stores. It increases after the
	// store has the change and before changed is notified.
	version atomic.Uint64
	copies  map[*informer.Collection]*copies
}

// copies caches the typed form of each object in one store, keyed by
// the store's object. The store replaces an object on each change and
// never modifies one in place, so a cached form stays valid while the
// store still holds that object. So a snapshot converts only the
// objects that changed since the previous one. The cache exists because
// the conversion uses reflection and costs more than the rest of a
// snapshot.
type copies struct {
	mu   sync.Mutex
	held map[any]any
}

// listOf returns the typed form of each object of one namespace in a
// collection, in key order. An object that does not convert is reported
// and left out, as informer.CachedList does. It keeps the cached forms
// of other namespaces' objects, so a snapshot of one namespace does not
// clear the cache of another.
func listOf[T any](s *stores, collection *informer.Collection, namespace string) []T {
	c := s.copies[collection]
	c.mu.Lock()
	defer c.mu.Unlock()
	objects := collection.View().Store.List()
	keys := make(map[any]string, len(objects))
	for _, object := range objects {
		keys[object], _ = cache.MetaNamespaceKeyFunc(object)
	}
	slices.SortFunc(objects, func(a, b any) int { return strings.Compare(keys[a], keys[b]) })
	held := make(map[any]any, len(objects))
	var items []T
	for _, object := range objects {
		if !strings.HasPrefix(keys[object], namespace+"/") {
			if item, ok := c.held[object]; ok {
				held[object] = item
			}
			continue
		}
		item, ok := c.held[object].(T)
		if !ok {
			var err error
			if item, err = informer.Convert[T](object); err != nil {
				informer.Report("the cached objects", err)
				continue
			}
		}
		held[object] = item
		items = append(items, item)
	}
	c.held = held
	return items
}

// watchCount is the number of watches: one for each kind, plus the
// pods, Services, ConfigMaps, and Jobs.
var watchCount = int32(len(observatory.Kinds) + 4)

// startWatches opens every watch. Each change notifies changed, and so
// does the end of each watch's first list. m counts each reopened watch
// under the watch's kind.
func startWatches(ctx context.Context, client dynamic.Interface, changed *changeNotifier, m *metrics) *stores {
	s := &stores{kinds: map[observatory.Kind]*informer.Collection{}, copies: map[*informer.Collection]*copies{}}
	options := informer.Options{
		Handler: wakeOnAnyChange(func() {
			s.version.Add(1)
			changed.notify()
		}),
		Synced: func() {
			s.synced.Add(1)
			s.version.Add(1)
			changed.notify()
		},
	}
	counted := func(kind string) informer.Options {
		counted := options
		counted.Reopened = m.watchReopened(kind)
		return counted
	}
	for _, kind := range observatory.Kinds {
		kindOptions := counted(kind.Name)
		// The status fields that composedFields returns make up most of
		// a device object, and no pass reads them, so the cache does not
		// store them. As a result, the status writer's comparison with
		// the cached status always differs for a connected device
		// (status.go), and it sends a PUT that the API server finds
		// unchanged.
		if dropped := composedFields(kind); len(dropped) > 0 {
			kindOptions.Transform = func(object *unstructured.Unstructured) {
				for _, field := range dropped {
					unstructured.RemoveNestedField(object.Object, "status", field)
				}
			}
		}
		s.kinds[kind] = informer.Start(ctx, client, informer.Source{Resource: kindResource(kind)}, kindOptions)
	}
	own := labelManagedBy + "=" + managedBy
	s.pods = informer.Start(ctx, client, informer.Source{Resource: podsResource, LabelSelector: own}, counted("Pod"))
	s.services = informer.Start(ctx, client, informer.Source{Resource: servicesResource, LabelSelector: own}, counted("Service"))
	s.configMaps = informer.Start(ctx, client, informer.Source{Resource: configMapsResource, LabelSelector: own}, counted("ConfigMap"))
	s.jobs = informer.Start(ctx, client, informer.Source{Resource: jobsResource, LabelSelector: own}, counted("Job"))
	for _, c := range s.kinds {
		s.copies[c] = &copies{}
	}
	s.copies[s.pods], s.copies[s.services], s.copies[s.configMaps], s.copies[s.jobs] = &copies{}, &copies{}, &copies{}, &copies{}
	return s
}

// namespaces returns the namespaces that hold an object of the API
// group or an object that the operator created. A namespace with only
// the operator's pods left still needs its supervisor, which deletes
// them.
func (s *stores) namespaces() map[string]bool {
	held := map[string]bool{}
	collections := []*informer.Collection{s.pods, s.services, s.configMaps, s.jobs}
	for _, c := range s.kinds {
		collections = append(collections, c)
	}
	for _, c := range collections {
		for _, key := range c.View().Store.ListKeys() {
			if namespace, _, found := strings.Cut(key, "/"); found {
				held[namespace] = true
			}
		}
	}
	return held
}

// ready reports whether every watch has finished its first list.
func (s *stores) ready() bool { return s.synced.Load() == watchCount }

// done waits until every watch has stopped.
func (s *stores) done() {
	for _, c := range s.kinds {
		<-c.Done()
	}
	<-s.pods.Done()
	<-s.services.Done()
	<-s.configMaps.Done()
	<-s.jobs.Done()
}

// wakeOnAnyChange wakes the operator for every add, update, and delete,
// including the operator's own status writes. It does not filter by
// generation, because a runner that waits for a pod to become Ready or
// for a Reservation to be deleted needs updates that do not change the
// generation. The status writer limits its own write rate (status.go).
func wakeOnAnyChange(changed func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { changed() },
		UpdateFunc: func(any, any) { changed() },
		DeleteFunc: func(any) { changed() },
	}
}

// snapshot reads every store into one tree and applies the sessions'
// records to it.
func (s *stores) snapshot(namespace string) *tree {
	t := s.read(namespace)
	t.applyRecords()
	return t
}

// read reads every store into one tree, without applying the sessions'
// records.
func (s *stores) read(namespace string) *tree {
	t := &tree{
		namespace:     namespace,
		observatories: byName(listOf[observatory.Observatory](s, s.kinds[observatory.ObservatoryKind], namespace)),
		telescopes:    byName(listOf[observatory.Telescope](s, s.kinds[observatory.TelescopeKind], namespace)),
		tubes:         byName(listOf[observatory.OpticalTube](s, s.kinds[observatory.OpticalTubeKind], namespace)),
		trains:        byName(listOf[observatory.OpticalTrain](s, s.kinds[observatory.OpticalTrainKind], namespace)),
		guiders:       byName(listOf[observatory.Guider](s, s.kinds[observatory.GuiderKind], namespace)),
		reservations:  byName(listOf[observatory.Reservation](s, s.kinds[observatory.ReservationKind], namespace)),
		horizons:      byName(listOf[observatory.Horizon](s, s.kinds[observatory.HorizonKind], namespace)),
		pods:          map[string]*pod{},
		services:      map[string]*service{},
		configMaps:    map[string]*configMap{},
		jobs:          map[string]*job{},
	}
	for _, kind := range observatory.DeviceKinds {
		for _, object := range listOf[deviceObject](s, s.kinds[kind], namespace) {
			t.devices = append(t.devices, &device{kind: kind, object: object})
		}
	}
	for _, p := range listOf[pod](s, s.pods, namespace) {
		t.pods[p.Metadata.Name] = &p
	}
	for _, svc := range listOf[service](s, s.services, namespace) {
		t.services[svc.Metadata.Name] = &svc
	}
	for _, files := range listOf[configMap](s, s.configMaps, namespace) {
		t.configMaps[files.Metadata.Name] = &files
	}
	for _, j := range listOf[job](s, s.jobs, namespace) {
		t.jobs[j.Metadata.Name] = &j
	}
	return t
}

// byName indexes the objects of one kind by name. Names are unique
// because every kind is namespaced and a tree holds one namespace.
func byName[S, T any](items []observatory.Object[S, T]) map[string]*observatory.Object[S, T] {
	out := make(map[string]*observatory.Object[S, T], len(items))
	for i := range items {
		out[items[i].Metadata.Name] = &items[i]
	}
	return out
}

// composedFields returns the status fields of a device kind that the
// status writer composes from INDI and no pass reads: the typed fields
// of its standard properties, status.limits, and status.properties. It
// returns none for a kind that is not a device.
func composedFields(kind observatory.Kind) []string {
	standard, ok := observatory.Standards[kind]
	if !ok {
		return nil
	}
	fields := []string{"limits", "properties"}
	for f := range reflect.TypeOf(standard.Status()).Elem().Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		fields = append(fields, name)
	}
	return fields
}
