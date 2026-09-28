package main

// The store of each informer a pass reads, while the informer runs.
// objectcache.go holds how a pass reads a store and when it reads the
// API server instead.

import (
	"sync"

	"k8s.io/client-go/tools/cache"
)

// watchStore holds the store of one running informer. The zero value
// and a nil pointer hold nothing, so a caller with no watch, such as a
// test that runs one pass, reads the API server.
type watchStore struct {
	mu     sync.Mutex
	store  cache.Store
	synced func() bool
	// selected says the informer's watch has a selector, so its store
	// holds part of the collection.
	selected bool
}

// hold records the store of an informer that runs, and release forgets
// it when the informer stops, so no pass reads a store that no watch
// keeps current.
func (w *watchStore) hold(store cache.Store, synced func() bool, selected bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.store, w.synced, w.selected = store, synced, selected
}

func (w *watchStore) release() {
	w.hold(nil, nil, false)
}

// view answers the store while its informer runs, and the zero view,
// which holds nothing, when no informer runs.
func (w *watchStore) view() storeView {
	if w == nil {
		return storeView{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return storeView{store: w.store, synced: w.synced, whole: !w.selected}
}
