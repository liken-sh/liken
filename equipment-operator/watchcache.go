package main

// The store of each informer a pass reads, while the informer runs.
// objectcache.go holds how a pass reads a store and when it reads the
// API server instead.

import (
	"sync"

	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// watchStore holds the store of one running informer. The zero value
// and a nil pointer hold nothing, so a caller with no watch, such as a
// test that runs one pass, reads the API server.
type watchStore struct {
	mu   sync.Mutex
	held informer.View
	// changes is closed at the next change the informer takes, and
	// is nil while nothing waits for one.
	changes chan struct{}
}

// hold records the store of an informer that runs, and release forgets
// it when the informer stops, so no pass reads a store that no watch
// keeps current.
//
// selected says the informer's watch has a selector, so its store holds
// part of the collection and is not whole (informer.View.Whole).
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

// view answers the store while its informer runs, and the zero view,
// which holds nothing, when no informer runs.
func (w *watchStore) view() informer.View {
	if w == nil {
		return informer.View{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.held
}

// changed answers a channel that closes at the next change the
// informer takes into the store. A caller that waits for a state takes
// the channel before it reads the store, so a change that lands during
// the read closes the channel it then waits on. A nil pointer answers a
// nil channel, which never closes.
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

// announce closes the channel of every caller that waits for a change.
// The informer calls it after the store takes the change, so a waiter
// that reads the store then finds the change.
func (w *watchStore) announce() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.changes != nil {
		close(w.changes)
		w.changes = nil
	}
}

// announcer is the handler that announces each change the informer
// takes.
func (w *watchStore) announcer() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { w.announce() },
		UpdateFunc: func(any, any) { w.announce() },
		DeleteFunc: func(any) { w.announce() },
	}
}
