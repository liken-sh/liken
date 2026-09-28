//go:build !pod

package main

// The shared functions that read a watch's store. versionmemo.go says
// what the memo guards and why the functions are split across the two
// files.
//
// A list comes from a store only after the store holds the whole first
// read. Before that, watch.go answers the error of the informer's last
// read, as it does for every other collection, so a pass never reads a
// store that holds part of the collection as the whole collection, and a
// kind the cluster does not serve costs no request on each pass. A copy
// in a store that does not convert is read from the API server.
//
// The operator creates none of the three kinds: a person declares each
// one. So no store sets whole, and a list reads no object that the store
// does not hold yet.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"k8s.io/client-go/tools/cache"
)

// storeView is one watch's store, and whether the store holds the
// whole first read. The zero value holds nothing, and every read goes
// to the API server.
type storeView struct {
	store  cache.Store
	synced func() bool

	// whole says the watch takes every object of the kind, with no
	// selector. A list from such a store also holds each object this
	// operator created or wrote that the store does not hold yet. A
	// store with a selector leaves that out, because an object the
	// operator wrote can be outside the selection.
	whole bool
}

// ready reports whether a list can come from the store.
func (v storeView) ready() bool {
	return v.store != nil && v.synced != nil && v.synced()
}

// unheld answers each key the memo noted at a version, which the store
// does not hold.
func (m *versionMemo) unheld(store cache.Store) []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for key, version := range m.seen {
		if _, held, _ := store.GetByKey(key); !held && version != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// heldObjects is one kind's store, and the memo of the copies this
// operator wrote or read.
type heldObjects struct {
	view     storeView
	versions *versionMemo
}

// cachedCopy answers the store's copy of one object, by its key. A copy
// that does not convert is logged and not answered, so the caller reads
// the object from the API server.
func cachedCopy[T any](view storeView, key string) (*T, bool) {
	if view.store == nil {
		return nil, false
	}
	object, held, err := view.store.GetByKey(key)
	if err != nil || !held {
		return nil, false
	}
	item, err := convert[T](object)
	if err != nil {
		reportUnconverted("the cached "+key, err)
		return nil, false
	}
	return &item, true
}

// storedObjects answers every object in the store, in the order of
// their keys, which is the order of a list from the API server.
func storedObjects(view storeView) []any {
	objects := view.store.List()
	slices.SortFunc(objects, func(a, b any) int { return strings.Compare(objectKey(a), objectKey(b)) })
	return objects
}

// objectKey is the key a store holds an object under.
func objectKey(object any) string {
	key, _ := cache.MetaNamespaceKeyFunc(object)
	return key
}

// cachedList answers every copy in the store, in the order of their
// keys. A copy that does not convert is logged and left out, the same
// as a watch event that does not convert.
func cachedList[T any](view storeView) []T {
	var items []T
	for _, object := range storedObjects(view) {
		item, err := convert[T](object)
		if err != nil {
			reportUnconverted("the cached objects", err)
			continue
		}
		items = append(items, item)
	}
	return items
}

// readOne answers one object: the store's copy when it holds a current
// one, and the API server's copy when it does not.
func readOne[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, held heldObjects, key, path string) (*T, error) {
	if copied, ok := cachedCopy[T](held.view, key); ok && held.versions.current(key, P(copied).meta().ResourceVersion) {
		return copied, nil
	}
	return readFresh[T, P](ctx, c, held.versions, key, path)
}

// currentList answers the store's copies, in the order of their keys.
// A copy that is older than this operator's own last write, or that
// does not convert, is replaced by the API server's copy, and an object
// the API server no longer holds is left out. A store that holds the
// whole collection also answers each object the memo noted and the
// store does not hold yet, such as one this operator created a moment
// ago, so a pass does not create it again.
func currentList[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, held heldObjects, path func(key string) string) ([]T, error) {
	// The informer can take a created object between the two reads, so
	// the memo's keys are read first. In the other order, the store's
	// keys miss the object, the memo skips it because the store now
	// holds it, and the list leaves it out, so a pass creates it again.
	// In this order the key is in one read or in both.
	var keys []string
	if held.view.whole {
		keys = held.versions.unheld(held.view.store)
	}
	keys = append(keys, held.view.store.ListKeys()...)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	current := make([]T, 0, len(keys))
	for _, key := range keys {
		if copied, ok := cachedCopy[T](held.view, key); ok && held.versions.current(key, P(copied).meta().ResourceVersion) {
			current = append(current, *copied)
			continue
		}
		fresh, err := readFresh[T, P](ctx, c, held.versions, key, path(key))
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		current = append(current, *fresh)
	}
	return current, nil
}

// namespacedPath answers the API path of the object a store key names,
// from the path of one object by its namespace and name.
func namespacedPath(path func(namespace, name string) string) func(key string) string {
	return func(key string) string {
		namespace, name, _ := strings.Cut(key, "/")
		return path(namespace, name)
	}
}

// currentOrCached answers currentList, or the store's copies where a
// read from the API server fails. This operator alone needs it. The pass
// reads a failed read of the MetadataProviders as no provider at all,
// and every Library then reports each of its sources as missing, so one
// provider whose read fails must not fail the list. The store's copy of
// that provider can be older than the operator's own write. A status
// write from it goes through settleStatus, which reads the provider
// again on a 409.
func currentOrCached[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, held heldObjects, path func(key string) string) []T {
	items, err := currentList[T, P](ctx, c, held, path)
	if err == nil {
		return items
	}
	fmt.Fprintf(os.Stderr, "reading one object from the API server: %v; the pass reads the watch's copies\n", err)
	return cachedList[T](held.view)
}

// readStood answers one object this operator stands by name: the store's
// copy when it holds a current one, and the API server's copy when it does
// not. This operator alone needs it. The store holds every object the
// watch selects, so once it holds its first read, an object it does not
// hold and the memo has not noted does not exist, and the read costs the
// API server nothing. An object the memo noted, such as one this operator
// created a moment ago or one whose create met a conflict, is read from
// the API server until the store holds it or the API server answers that
// it is gone.
func readStood[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, held heldObjects, key, path string) (*T, error) {
	if held.view.ready() {
		if _, stored, _ := held.view.store.GetByKey(key); !stored && !held.versions.noted(key) {
			return nil, ErrNotFound
		}
	}
	found, err := readOne[T, P](ctx, c, held, key, path)
	if errors.Is(err, ErrNotFound) {
		// The object is gone, and the store holds no copy of it or
		// holds the last one until the watch delivers the delete. The
		// memo forgets it, so the next read answers from the store and
		// sends nothing.
		held.versions.forget(key)
	}
	return found, err
}

// forget drops the memo's record of one object.
func (m *versionMemo) forget(key string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seen, key)
	delete(m.requests, key)
}

// noted reports whether the memo holds a record of the key, at any
// version, the empty one included.
func (m *versionMemo) noted(key string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, noted := m.seen[key]
	return noted
}

// forgetGone drops the record of each object that the list did not
// answer and the store does not hold. The API server answered 404 for
// such an object, and its watch delivered the delete. A key the store
// still holds keeps its record, so a copy of an object the API server
// already deleted is not current again before the watch removes it.
func (m *versionMemo) forgetGone(store cache.Store, listed map[string]bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.seen {
		if listed[key] {
			continue
		}
		if _, held, _ := store.GetByKey(key); held {
			continue
		}
		delete(m.seen, key)
		delete(m.requests, key)
	}
}
