package main

// The watches. One watch follows each kind of the
// astrophotography.liken.sh group in every namespace, one follows the
// Reservations that name this operator as their holder, and one follows
// each kind of observatory.liken.sh that the runner reads (plan 01).
// Four more follow the pods, Services, Deployments, and Jobs that the
// operator creates, selected by its label. All of them run on
// client-go's reflector through the shared informer package. A handler
// only rings the bell, and each loop that waits on it reads the stores
// again.

import (
	"context"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// bell wakes every goroutine that waits for a change. A change closes
// the current channel and makes a new one, so a waiter that read the
// channel before the change wakes, and one that reads it after waits
// for the next change.
type bell struct {
	mu sync.Mutex
	ch chan struct{}
}

func newBell() *bell { return &bell{ch: make(chan struct{})} }

func (b *bell) notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.ch)
	b.ch = make(chan struct{})
}

func (b *bell) wait() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

// watched is one collection that the operator watches.
type watched struct {
	// kind names the collection in the watch restarts series.
	kind   string
	source informer.Source
	// fromObservatory marks a kind of observatory-operator, whose CRDs
	// this operator does not install.
	fromObservatory bool
}

// heldBy selects the Reservations whose spec.holder names this
// operator. The Reservation's CRD declares spec.holder as a selectable
// field (plan 08), so the API server sends no other reservation.
const heldBy = "spec.holder=" + managedBy

// observatoryKinds are the kinds of observatory-operator that the runner
// reads besides the Reservation: the site, the telescope and its optics
// for the plate scale, the devices it drives, and the guider (plan 01).
var observatoryKinds = []observatory.Kind{
	observatory.ObservatoryKind, observatory.TelescopeKind,
	observatory.OpticalTrainKind, observatory.OpticalTubeKind,
	observatory.MountKind, observatory.CameraKind, observatory.FilterWheelKind,
	observatory.FocuserKind, observatory.GuiderKind,
}

// ownedKinds are the kinds the operator creates: the worker's and the
// solver's Deployment and Service, the promotion Jobs, and the pods of
// all three, whose status holds a promotion's report and the reason a
// pod waits.
var ownedKinds = []struct {
	name     string
	resource schema.GroupVersionResource
}{
	{"Pod", schema.GroupVersionResource{Version: "v1", Resource: "pods"}},
	{"Service", schema.GroupVersionResource{Version: "v1", Resource: "services"}},
	{"Deployment", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
	{"Job", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}},
}

// watches answers every collection the operator watches.
func watches() []watched {
	var out []watched
	for _, kind := range astrophotography.Kinds {
		out = append(out, watched{kind: kind.Name, source: informer.Source{Resource: schema.GroupVersionResource{
			Group: astrophotography.Group, Version: astrophotography.Version, Resource: kind.Plural}}})
	}
	out = append(out, watched{kind: observatory.ReservationKind.Name, fromObservatory: true, source: informer.Source{
		Resource:      observatoryResource(observatory.ReservationKind),
		FieldSelector: heldBy,
	}})
	for _, kind := range observatoryKinds {
		out = append(out, watched{kind: kind.Name, fromObservatory: true, source: informer.Source{Resource: observatoryResource(kind)}})
	}
	for _, kind := range ownedKinds {
		out = append(out, watched{kind: kind.name, source: informer.Source{
			Resource: kind.resource, LabelSelector: labelManagedBy + "=" + managedBy}})
	}
	return out
}

func observatoryResource(kind observatory.Kind) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: observatory.Group, Version: observatory.Version, Resource: kind.Plural}
}

// stores holds the copy of each watched collection, by its kind's name.
type stores struct {
	collections map[string]*informer.Collection
}

// startWatches opens every watch, across every namespace. Each change
// rings changed, and so does the end of each watch's first read. Each
// watch that the API server closes and the operator opens again counts
// in m's watch restarts, under the watch's kind.
func startWatches(ctx context.Context, client dynamic.Interface, changed *bell, m *metrics) *stores {
	s := &stores{collections: map[string]*informer.Collection{}}
	for _, w := range watches() {
		options := informer.Options{
			Handler:  wakeOnAnyChange(changed.notify),
			Synced:   changed.notify,
			Reopened: m.watchReopened(w.kind),
		}
		// A cluster with no observatory-operator yet serves none of its
		// kinds. The copy then holds an empty collection, and the watch
		// finds the kind when observatory-operator's CRDs arrive.
		if w.fromObservatory {
			options.Absent = apierrors.IsNotFound
		}
		s.collections[w.kind] = informer.Start(ctx, client, w.source, options)
	}
	return s
}

// synced reports whether every watch has read its whole collection
// once.
func (s *stores) synced() bool {
	for _, c := range s.collections {
		if !c.Synced() {
			return false
		}
	}
	return true
}

// done returns when every watch has stopped.
func (s *stores) done() {
	for _, c := range s.collections {
		<-c.Done()
	}
}

// wakeOnAnyChange wakes the loops for every add, update, and delete.
// The runner waits for a pod to become Ready and for a Reservation's
// phase, and neither changes a generation, so no update is filtered.
func wakeOnAnyChange(changed func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { changed() },
		UpdateFunc: func(any, any) { changed() },
		DeleteFunc: func(any) { changed() },
	}
}
