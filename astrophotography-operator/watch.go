package main

// Every watch covers all namespaces and runs on client-go's reflector
// through kubernetes/informer. An event handler only notifies the
// changeNotifier, and each loop that waits on it then reads the
// informer caches.

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

// changeNotifier wakes every goroutine that waits for a change. notify
// closes the current channel and creates a new one. A goroutine that
// called wait before the change wakes. One that calls wait after it
// waits for the next change.
type changeNotifier struct {
	mu sync.Mutex
	ch chan struct{}
}

func newChangeNotifier() *changeNotifier { return &changeNotifier{ch: make(chan struct{})} }

func (b *changeNotifier) notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.ch)
	b.ch = make(chan struct{})
}

func (b *changeNotifier) wait() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

// watched is one collection that the operator watches.
type watched struct {
	// kind is the key of the collection in stores and its label in the
	// watch restarts metric.
	kind   string
	source informer.Source
	// fromObservatory is true for a kind of observatory-operator, whose
	// CRDs this operator does not install.
	fromObservatory bool
}

// heldBy is the field selector for the Reservations whose spec.holder
// is this operator. The Reservation CRD declares spec.holder as a
// selectable field, so the API server sends no other Reservation.
const heldBy = "spec.holder=" + managedBy

// observatoryKinds are the kinds of observatory-operator that the runner
// reads besides the Reservation: the site, the telescope with its
// optics for the plate scale, the horizon, the devices it drives, and
// the guider.
var observatoryKinds = []observatory.Kind{
	observatory.ObservatoryKind, observatory.TelescopeKind, observatory.HorizonKind,
	observatory.OpticalTrainKind, observatory.OpticalTubeKind,
	observatory.MountKind, observatory.CameraKind, observatory.FilterWheelKind,
	observatory.FocuserKind, observatory.GuiderKind,
}

// ownedKinds are the kinds the operator creates: the Deployment and
// Service of the worker and of the solver, the promotion Jobs, and
// their pods. The operator watches the pods because a pod's status
// holds a promotion's report and the reason the pod is not running.
var ownedKinds = []struct {
	name     string
	resource schema.GroupVersionResource
}{
	{"Pod", schema.GroupVersionResource{Version: "v1", Resource: "pods"}},
	{"Service", schema.GroupVersionResource{Version: "v1", Resource: "services"}},
	{"Deployment", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
	{"Job", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}},
}

// watches returns every collection the operator watches.
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
	out = append(out, watched{kind: claimKind, source: informer.Source{Resource: claimsResource}})
	return out
}

// The operator does not create or label the outbox and archive claims,
// so the watch reads every PersistentVolumeClaim. The
// promotion loop checks that an archive claim exists, and adds the
// finalizer to each outbox claim that has frames.
const claimKind = "PersistentVolumeClaim"

var claimsResource = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}

func observatoryResource(kind observatory.Kind) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: observatory.Group, Version: observatory.Version, Resource: kind.Plural}
}

// stores holds the cache of each watched collection, by kind name.
type stores struct {
	collections map[string]*informer.Collection
}

// startWatches opens every watch. Each event notifies changed, and so
// does the end of each watch's first list. Each reopened watch adds one
// to m's watch restarts metric for its kind.
func startWatches(ctx context.Context, client dynamic.Interface, changed *changeNotifier, m *metrics) *stores {
	s := &stores{collections: map[string]*informer.Collection{}}
	for _, w := range watches() {
		options := informer.Options{
			Handler:  wakeOnAnyChange(changed.notify),
			Synced:   changed.notify,
			Reopened: m.watchReopened(w.kind),
		}
		// The API server returns 404 for observatory-operator's kinds
		// until its CRDs are installed. With Absent set, the cache holds
		// an empty collection, and the informer asks again every
		// AbsentRecheck, five minutes by default, until the kind exists.
		if w.fromObservatory {
			options.Absent = apierrors.IsNotFound
		}
		s.collections[w.kind] = informer.Start(ctx, client, w.source, options)
	}
	return s
}

func (s *stores) synced() bool {
	for _, c := range s.collections {
		if !c.Synced() {
			return false
		}
	}
	return true
}

// done waits until every watch has stopped.
func (s *stores) done() {
	for _, c := range s.collections {
		<-c.Done()
	}
}

// wakeOnAnyChange calls changed for every add, update, and delete. It
// does not filter updates by generation, because the runner waits for a
// pod to become Ready and for a Reservation's phase, and neither change
// bumps metadata.generation.
func wakeOnAnyChange(changed func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { changed() },
		UpdateFunc: func(any, any) { changed() },
		DeleteFunc: func(any) { changed() },
	}
}
