package main

// The loop's wakes come from the kernel and from bluetoothd, and
// neither of them reports that somebody created a PairingRequest or
// approved a device in one. So the operator watches the requests in
// every namespace, and wakes the loop when a change leaves a request
// that needs a pass. A new request opens its window, and an approval
// pairs its device, in the pass that follows the event.
//
// A finished request needs one more pass, when its TTL is up, and no
// event marks that moment. So the watcher also keeps a clock, set to
// the earliest TTL still ahead, and wakes the loop when the clock
// fires. The clock is a deadline, not a poll: it reads nothing.
//
// An open window needs no clock here. The pass that serves the window
// asks for its own follow-up pass while the window is open, and that
// pass closes the window at its deadline.

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// watchPairingRequests wakes the loop while a request needs attention.
// The channel closes when the context ends. The store it answers holds
// every request, and the pass reads it in place of the API server
// (objectcache.go).
func watchPairingRequests(ctx context.Context, client dynamic.Interface, now func() time.Time) (<-chan struct{}, informer.View) {
	wake := make(chan struct{}, 1)
	watcher := &requestWatcher{now: now, wake: wake, held: map[string]PairingRequest{}}
	requests := informer.Start(ctx, client, informer.Source{Resource: pairingRequestResource}, informer.Options{Handler: watcher.handler()})
	go func() {
		defer close(wake)
		defer watcher.stop()
		<-requests.Done()
	}()
	return wake, requests.View()
}

// requestWatcher holds the requests the watch reported, and the clock
// for the next collection. The watch and the clock run on different
// goroutines, so both go through the lock.
type requestWatcher struct {
	mu      sync.Mutex
	now     func() time.Time
	wake    chan<- struct{}
	held    map[string]PairingRequest
	clock   *time.Timer
	stopped bool
}

// handler sends each change the informer reports to apply. The
// informer reports the first read as one addition for each object, and
// a later read as the difference from what it held, so held follows
// the collection through every gap in the watch.
func (w *requestWatcher) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(object any) { w.apply(object, false) },
		UpdateFunc: func(_, object any) { w.apply(object, false) },
		DeleteFunc: func(object any) { w.apply(object, true) },
	}
}

// apply takes one change from the watch.
func (w *requestWatcher) apply(object any, deleted bool) {
	request, err := informer.Convert[PairingRequest](object)
	if err != nil {
		informer.Report("the PairingRequests", err)
		w.forget(object, deleted)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if deleted {
		delete(w.held, requestKey(request))
	} else {
		w.held[requestKey(request)] = request
	}
	w.look()
}

// forget removes a deleted request that did not convert. A tombstone
// can hold no copy of the request, but its key is namespace/name, the
// same key held uses. Without this, a removed request would stay in
// held, and keep waking the loop and setting the clock.
func (w *requestWatcher) forget(object any, deleted bool) {
	tombstone, ok := object.(cache.DeletedFinalStateUnknown)
	if !deleted || !ok {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.held, tombstone.Key)
	w.look()
}

// fire is the clock's callback.
func (w *requestWatcher) fire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.look()
}

// stop ends the clock. The wake channel closes after this, and a clock
// that fired after the close would send on a closed channel.
func (w *requestWatcher) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	if w.clock != nil {
		w.clock.Stop()
	}
}

// look wakes the loop when a request needs a pass, and sets the clock
// for the next collection. The caller holds the lock.
func (w *requestWatcher) look() {
	if w.stopped {
		return
	}
	now := w.now()
	requests := make([]PairingRequest, 0, len(w.held))
	for _, request := range w.held {
		requests = append(requests, request)
	}
	if requestsNeedAPass(requests, now) {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	if w.clock != nil {
		w.clock.Stop()
		w.clock = nil
	}
	if due, ok := nextCollection(requests, now); ok {
		w.clock = time.AfterFunc(due.Sub(now), w.fire)
	}
}

func requestKey(request PairingRequest) string {
	return request.Metadata.Namespace + "/" + request.Metadata.Name
}

// requestsNeedAPass reports whether any request needs the loop to run.
func requestsNeedAPass(requests []PairingRequest, now time.Time) bool {
	for _, request := range requests {
		if !request.Status.finished() {
			return true
		}
		finished := parseTimestamp(request.Status.FinishedAt)
		if finished.IsZero() || !now.Before(finished.Add(request.Spec.ttl())) {
			return true
		}
	}
	return false
}

// nextCollection answers the earliest moment a finished request's TTL
// is up, among the moments still ahead of now. A request whose TTL is
// already up is not counted: requestsNeedAPass wakes the loop for it,
// and the pass deletes it.
func nextCollection(requests []PairingRequest, now time.Time) (time.Time, bool) {
	var next time.Time
	for _, request := range requests {
		finished := parseTimestamp(request.Status.FinishedAt)
		if !request.Status.finished() || finished.IsZero() {
			continue
		}
		due := finished.Add(request.Spec.ttl())
		if !due.After(now) {
			continue
		}
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	return next, !next.IsZero()
}
