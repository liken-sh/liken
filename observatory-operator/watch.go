package main

// The watches. One watch follows each of the 20 kinds of the group in
// the operator's namespace, and two more follow the pods and the
// Services the operator created, selected by its label. All of them run
// on client-go's reflector through the shared informer package. A
// handler only wakes the operator, and each goroutine that waits reads
// the stores again: the supervisor, the status writer, and each
// reservation's runner (reservation.go).

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// bell wakes every goroutine that waits for a change. A change
// closes the current channel and makes a new one, so a waiter that
// read the channel before the change wakes, and one that reads it
// after waits for the next change.
type bell struct {
	mu sync.Mutex
	ch chan struct{}
}

func newBell() *bell { return &bell{ch: make(chan struct{})} }

func (s *bell) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.ch)
	s.ch = make(chan struct{})
}

func (s *bell) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ch
}

func kindResource(kind observatory.Kind) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: observatory.Group, Version: observatory.Version, Resource: kind.Plural}
}

var (
	podsResource     = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	servicesResource = schema.GroupVersionResource{Version: "v1", Resource: "services"}
)

// stores holds the copy of each watched collection.
type stores struct {
	kinds    map[observatory.Kind]*informer.Collection
	pods     *informer.Collection
	services *informer.Collection
	// synced counts the watches whose first read is in their store.
	synced atomic.Int32
	// copies holds the typed copies of each collection's objects.
	copies map[*informer.Collection]*copies
}

// copies holds the typed copy of each object that one store holds, by
// the store's object. The store replaces an object on each change and
// never changes one in place, so an object that the store still holds
// has the same typed copy. A snapshot then converts only the objects
// that changed since the last one, because the conversion goes through
// reflection and costs more than the rest of a snapshot.
type copies struct {
	mu   sync.Mutex
	held map[any]any
}

// listOf answers the typed copy of each object in a collection, in the
// order of their keys. An object that does not convert is reported and
// left out, as informer.CachedList does.
func listOf[T any](s *stores, collection *informer.Collection) []T {
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

// watchCount is the number of watches: the 20 kinds, the pods, and the
// Services.
var watchCount = int32(len(observatory.Kinds) + 2)

// startWatches opens every watch. Each change notifies changed, and so
// does the end of each watch's first read.
func startWatches(ctx context.Context, client dynamic.Interface, namespace string, changed *bell) *stores {
	s := &stores{kinds: map[observatory.Kind]*informer.Collection{}, copies: map[*informer.Collection]*copies{}}
	options := informer.Options{
		Handler: wakeOnAnyChange(changed),
		Synced: func() {
			s.synced.Add(1)
			changed.notify()
		},
	}
	for _, kind := range observatory.Kinds {
		s.kinds[kind] = informer.Start(ctx, client, informer.Source{Resource: kindResource(kind), Namespace: namespace}, options)
	}
	own := labelManagedBy + "=" + managedBy
	s.pods = informer.Start(ctx, client, informer.Source{Resource: podsResource, Namespace: namespace, LabelSelector: own}, options)
	s.services = informer.Start(ctx, client, informer.Source{Resource: servicesResource, Namespace: namespace, LabelSelector: own}, options)
	for _, c := range s.kinds {
		s.copies[c] = &copies{}
	}
	s.copies[s.pods], s.copies[s.services] = &copies{}, &copies{}
	return s
}

// ready reports whether every watch has read its whole collection once.
func (s *stores) ready() bool { return s.synced.Load() == watchCount }

// done closes when every watch has stopped.
func (s *stores) done() {
	for _, c := range s.kinds {
		<-c.Done()
	}
	<-s.pods.Done()
	<-s.services.Done()
}

// wakeOnAnyChange wakes the operator for every add, update, and
// delete, its own status writes included. The operator's own reads of
// state run as fast as a store changes, and the status writer limits
// its rate with a clock (status.go). A runner that waits for a pod to
// become Ready, or for a reservation's deletion, needs the update that
// carries it, and that update changes no generation.
func wakeOnAnyChange(changed *bell) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { changed.notify() },
		UpdateFunc: func(any, any) { changed.notify() },
		DeleteFunc: func(any) { changed.notify() },
	}
}

// snapshot reads every store into one tree. An object that does not
// convert is reported and left out (listOf).
func (s *stores) snapshot(namespace string) *tree {
	t := &tree{
		namespace:     namespace,
		observatories: byName(listOf[observatory.Observatory](s, s.kinds[observatory.ObservatoryKind])),
		telescopes:    byName(listOf[observatory.Telescope](s, s.kinds[observatory.TelescopeKind])),
		tubes:         byName(listOf[observatory.OpticalTube](s, s.kinds[observatory.OpticalTubeKind])),
		trains:        byName(listOf[observatory.OpticalTrain](s, s.kinds[observatory.OpticalTrainKind])),
		guiders:       byName(listOf[observatory.Guider](s, s.kinds[observatory.GuiderKind])),
		reservations:  byName(listOf[observatory.Reservation](s, s.kinds[observatory.ReservationKind])),
		pods:          map[string]*pod{},
		services:      map[string]*service{},
	}
	for _, kind := range observatory.DeviceKinds {
		for _, object := range listOf[deviceObject](s, s.kinds[kind]) {
			t.devices = append(t.devices, &device{kind: kind, object: object})
		}
	}
	for _, p := range listOf[pod](s, s.pods) {
		t.pods[p.Metadata.Name] = &p
	}
	for _, svc := range listOf[service](s, s.services) {
		t.services[svc.Metadata.Name] = &svc
	}
	return t
}

// byName indexes the objects of one kind by name. Every kind is
// namespaced, and the operator watches one namespace.
func byName[S, T any](items []observatory.Object[S, T]) map[string]*observatory.Object[S, T] {
	out := make(map[string]*observatory.Object[S, T], len(items))
	for i := range items {
		out[items[i].Metadata.Name] = &items[i]
	}
	return out
}
