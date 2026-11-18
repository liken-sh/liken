package main

// Neither the kernel nor bluetoothd reports that someone created a
// PairingRequest or approved a device in one. So the operator watches
// the PairingRequests in every namespace, and after each change it
// wakes the loop when any request needs a pass. The pass that follows
// opens a new request's pairing window, or pairs an approved device.
//
// A finished request needs one more pass when its TTL expires, so the
// pass can delete it, and no event marks that moment. So the watcher
// also keeps a timer set to the earliest TTL expiry still in the
// future. The timer is a deadline and reads nothing from the API
// server.
//
// An open pairing window needs no timer here, because the pass that
// serves the window schedules its own next pass until the window's
// deadline.

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// watchPairingRequests starts the watch on PairingRequests. It returns
// the channel of wakes, which closes when the watch ends, and the
// informer's cache of every request, which the pass reads in place of
// the API server (objectcache.go).
func watchPairingRequests(ctx context.Context, client dynamic.Interface, now func() time.Time) (<-chan struct{}, informer.View) {
	wake := make(chan struct{}, 1)
	watcher := &requestWatcher{now: now, wake: wake, requests: map[string]PairingRequest{}}
	requests := informer.Start(ctx, client, informer.Source{Resource: pairingRequestResource}, informer.Options{Handler: watcher.handler()})
	go func() {
		defer close(wake)
		defer watcher.stop()
		<-requests.Done()
	}()
	return wake, requests.View()
}

// requestWatcher keeps the PairingRequests the watch reported, keyed by
// namespace/name, and the timer for the next TTL expiry. The watch
// handlers and the timer callback run on different goroutines, so both
// take mu.
type requestWatcher struct {
	mu       sync.Mutex
	now      func() time.Time
	wake     chan<- struct{}
	requests map[string]PairingRequest
	timer    *time.Timer
	stopped  bool
}

// handler passes each add, update, and delete to apply. After a relist,
// the informer reports each request that changed or was deleted while
// the watch was down, so requests stays equal to the collection.
func (w *requestWatcher) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(object any) { w.apply(object, false) },
		UpdateFunc: func(_, object any) { w.apply(object, false) },
		DeleteFunc: func(object any) { w.apply(object, true) },
	}
}

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
		delete(w.requests, requestKey(request))
	} else {
		w.requests[requestKey(request)] = request
	}
	w.evaluate()
}

// forget removes a deleted request that did not convert. A
// DeletedFinalStateUnknown tombstone can lack the last copy of the
// request, but its key is namespace/name, the same key as requests.
// Without forget, a deleted request would stay in requests and keep
// waking the loop and setting the timer.
func (w *requestWatcher) forget(object any, deleted bool) {
	tombstone, ok := object.(cache.DeletedFinalStateUnknown)
	if !deleted || !ok {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.requests, tombstone.Key)
	w.evaluate()
}

func (w *requestWatcher) fire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.evaluate()
}

// stop cancels the timer and marks the watcher stopped. The wake
// channel closes after stop returns, and a timer that fired after the
// close would panic when it sent on the channel.
func (w *requestWatcher) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
}

// evaluate wakes the loop when a request needs a pass, and resets the
// timer to the next TTL expiry. The caller holds mu.
func (w *requestWatcher) evaluate() {
	if w.stopped {
		return
	}
	now := w.now()
	requests := make([]PairingRequest, 0, len(w.requests))
	for _, request := range w.requests {
		requests = append(requests, request)
	}
	if requestsNeedAPass(requests, now) {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if due, ok := nextExpiry(requests, now); ok {
		w.timer = time.AfterFunc(due.Sub(now), w.fire)
	}
}

func requestKey(request PairingRequest) string {
	return request.Metadata.Namespace + "/" + request.Metadata.Name
}

// requestsNeedAPass reports whether any request is unfinished, finished
// with no status.finishedAt, or finished with an expired TTL. The pass
// serves an unfinished request, writes the missing finish time, or
// deletes the expired request.
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

// nextExpiry returns the earliest time after now at which a finished
// request's TTL expires, and false when there is none. It skips a TTL
// that has already expired, because requestsNeedAPass wakes the loop
// for that request.
func nextExpiry(requests []PairingRequest, now time.Time) (time.Time, bool) {
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
