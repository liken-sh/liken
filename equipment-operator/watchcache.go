package main

import (
	"sync"

	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// watchStore records the informer.View of the running informer, so a
// pass can read the informer's store in place of the API server.
// objectcache.go explains when a pass reads the store and when it reads
// the API server. The zero value and a nil pointer have no view, so a
// caller with no watch, such as a test that runs one pass, reads the
// API server.
type watchStore struct {
	mu   sync.Mutex
	held informer.View
	// changes closes at the next change to the store. It is nil while
	// nobody waits.
	changes chan struct{}
}

// hold records the view of a running informer, and release clears it
// when the informer stops, so no pass reads a store that nothing keeps
// current. selected is true when the watch has a field or label
// selector. The store then has only part of the collection, so the view
// is not Whole.
func (w *watchStore) hold(view informer.View, selected bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	view.Whole = !selected
	w.held = view
}

func (w *watchStore) release() {
	w.hold(informer.View{}, false)
}

func (w *watchStore) view() informer.View {
	if w == nil {
		return informer.View{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.held
}

// changed returns a channel that closes at the next change to the
// store. A caller that waits for some state gets the channel before it
// reads the store, so a change during the read still closes the
// channel. A nil watchStore returns a nil channel, which never closes.
func (w *watchStore) changed() <-chan struct{} {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.changes == nil {
		w.changes = make(chan struct{})
	}
	return w.changes
}

// announce wakes every caller that waits on changed. client-go calls the
// event handlers after it updates the store, so a waiter that then
// reads the store sees the change.
func (w *watchStore) announce() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.changes != nil {
		close(w.changes)
		w.changes = nil
	}
}

func (w *watchStore) announcer() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { w.announce() },
		UpdateFunc: func(any, any) { w.announce() },
		DeleteFunc: func(any) { w.announce() },
	}
}
