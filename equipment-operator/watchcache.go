package main

// A pass reads the collections it acts on from the store of the
// informer that watches them, and sends no request for them. The
// informer's store holds every object of the collection as the last
// event left it, so a pass that the watch woke reads the change that
// woke it: the informer updates the store before it calls a handler.
//
// A pass reads the API server instead when the store has nothing to
// give it: before the informer's first read is done, after the watch
// stopped, when the kind has no watch yet because its definition is
// missing, and when an object in the store does not convert. So a
// pass never acts on an empty store as if the collection were empty.
//
// A pass reads from a store only the kinds it does not write. The event
// of a pass's own write can arrive after a later wake, so a store can
// hold the copy from before that write, and a pass that compared its
// write with that copy would write again. Each pass that writes a kind
// lists it from the API server, and says so where it lists.

import (
	"slices"
	"strings"
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
}

// hold records the store of an informer that runs, and release forgets
// it when the informer stops, so no pass reads a store that no watch
// keeps current.
func (w *watchStore) hold(store cache.Store, synced func() bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.store, w.synced = store, synced
}

func (w *watchStore) release() {
	w.hold(nil, nil)
}

// current answers the store while its informer runs and has finished
// its first read.
func (w *watchStore) current() (cache.Store, bool) {
	if w == nil {
		return nil, false
	}
	w.mu.Lock()
	store, synced := w.store, w.synced
	w.mu.Unlock()
	if store == nil || !synced() {
		return nil, false
	}
	return store, true
}

// cachedList answers every object in the store, sorted by name the way
// the API server sorts a list. It answers false when the store has
// nothing to give or an object does not convert, and the caller reads
// the API server.
func cachedList[T any](held *watchStore, what string) ([]T, bool) {
	store, ok := held.current()
	if !ok {
		return nil, false
	}
	objects := store.List()
	slices.SortFunc(objects, func(a, b any) int { return strings.Compare(storeKey(a), storeKey(b)) })
	items := make([]T, 0, len(objects))
	for _, object := range objects {
		item, err := convert[T](object)
		if err != nil {
			reportUnconverted(what, err)
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

// cachedGet answers one object by name, and whether the store holds
// it. The last answer is false when the store has nothing to give or
// the object does not convert, and the caller reads the API server.
func cachedGet[T any](held *watchStore, what, name string) (T, bool, bool) {
	var none T
	store, ok := held.current()
	if !ok {
		return none, false, false
	}
	object, exists, err := store.GetByKey(name)
	if err != nil {
		return none, false, false
	}
	if !exists {
		return none, false, true
	}
	item, err := convert[T](object)
	if err != nil {
		reportUnconverted(what, err)
		return none, false, false
	}
	return item, true, true
}

// storeKey is an object's key in the store: its name, because every
// kind this operator watches is cluster-scoped.
func storeKey(object any) string {
	key, _ := cache.MetaNamespaceKeyFunc(object)
	return key
}
