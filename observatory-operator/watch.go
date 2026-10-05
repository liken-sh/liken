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
}

// watchCount is the number of watches: the 20 kinds, the pods, and the
// Services.
var watchCount = int32(len(observatory.Kinds) + 2)

// startWatches opens every watch. Each change notifies changed, and so
// does the end of each watch's first read.
func startWatches(ctx context.Context, client dynamic.Interface, namespace string, changed *bell) *stores {
	s := &stores{kinds: map[observatory.Kind]*informer.Collection{}}
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
// convert is reported and left out (informer.CachedList).
func (s *stores) snapshot(namespace string) *tree {
	t := &tree{
		namespace:     namespace,
		observatories: byName(informer.CachedList[observatory.Observatory](s.kinds[observatory.ObservatoryKind].View())),
		telescopes:    byName(informer.CachedList[observatory.Telescope](s.kinds[observatory.TelescopeKind].View())),
		tubes:         byName(informer.CachedList[observatory.OpticalTube](s.kinds[observatory.OpticalTubeKind].View())),
		trains:        byName(informer.CachedList[observatory.OpticalTrain](s.kinds[observatory.OpticalTrainKind].View())),
		guiders:       byName(informer.CachedList[observatory.Guider](s.kinds[observatory.GuiderKind].View())),
		reservations:  byName(informer.CachedList[observatory.Reservation](s.kinds[observatory.ReservationKind].View())),
		pods:          map[string]*pod{},
		services:      map[string]*service{},
	}
	for _, kind := range observatory.DeviceKinds {
		for _, object := range informer.CachedList[deviceObject](s.kinds[kind].View()) {
			t.devices = append(t.devices, &device{kind: kind, object: object})
		}
	}
	for _, p := range informer.CachedList[pod](s.pods.View()) {
		t.pods[p.Metadata.Name] = &p
	}
	for _, svc := range informer.CachedList[service](s.services.View()) {
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
