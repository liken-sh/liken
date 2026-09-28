//go:build !pod

package main

// The pass reads the Libraries, the Catalogs, and the MetadataProviders
// from the watches' stores, and this file answers those reads with the
// memo of versionmemo.go. A store's copy at the version the operator
// last wrote or read is current, and the store answers it. A copy at
// another version is older than the operator's own write, and the pass
// reads that object from the API server once. So a settled pass sends
// the API server no read of the three kinds, and no pass acts on a copy
// older than its own write.
//
// A list comes from a store only after the store holds the whole first
// read. Before that, watch.go answers the error of the informer's last
// read, as it does for every other collection, so a pass never reads a
// store that holds part of the collection as the whole collection, and a
// kind the cluster does not serve costs no request on each pass. A copy
// that does not convert is read from the API server, so a list leaves out
// no object.
//
// The operator creates none of the three kinds: a person declares each
// one. So every object the operator wrote is one the store held first,
// and a list needs no second source for an object the store does not
// hold yet.
//
// A write from an older copy states an older resourceVersion, and the
// API server answers 409 Conflict. The memo then notes that the
// operator holds no current copy, and the next pass reads the object
// from the API server. The backstop tick runs that pass (operate.go),
// and the pass derives the whole status again from the fresh copy.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
}

// ready reports whether a list can come from the store.
func (v storeView) ready() bool {
	return v.store != nil && v.synced != nil && v.synced()
}

// heldObjects is one kind's store, and the memo of the copies this
// operator wrote or read.
type heldObjects struct {
	view     storeView
	versions *versionMemo
}

// metaObject is a resource this operator reads from a store and writes,
// which carries its metadata in ObjectMeta.
type metaObject interface{ meta() *ObjectMeta }

func (l *Library) meta() *ObjectMeta          { return &l.Metadata }
func (c *NamespaceCatalog) meta() *ObjectMeta { return &c.Metadata }
func (p *MetadataProvider) meta() *ObjectMeta { return &p.Metadata }

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

// readFresh reads one object from the API server and notes its version.
func readFresh[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, versions *versionMemo, key, path string) (*T, error) {
	fresh := new(T)
	err := versions.send(key, func() (string, error) {
		if err := c.RequestJSON(ctx, http.MethodGet, path, nil, fresh); err != nil {
			return "", err
		}
		return P(fresh).meta().ResourceVersion, nil
	})
	return fresh, err
}

// currentList answers every object of one kind from a store that holds
// its first read. The list is the store's copies in the order of their
// keys, which is the order of a list from the API server. A copy older
// than this operator's own last write, or one that does not convert, is
// replaced by the API server's copy, and an object the API server no
// longer holds is left out. A read that fails in any other way fails the
// list.
func currentList[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, held heldObjects, path func(key string) string) ([]T, error) {
	keys := storeKeys(held.view.store)
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

// currentOrStored answers currentList, or every copy the store holds
// where a read from the API server fails. The pass reads a failed read of
// the MetadataProviders as no provider at all, and every Library then
// reports each of its sources as missing. One provider whose read fails
// must not cost every Library that. The store's copy of that provider can
// be older than the operator's own write, and a status write from it gets
// a 409, which sends the next pass to the API server again.
func currentOrStored[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, held heldObjects, path func(key string) string) []T {
	items, err := currentList[T, P](ctx, c, held, path)
	if err == nil {
		return items
	}
	fmt.Fprintf(os.Stderr, "reading one object from the API server: %v; the pass reads the watch's copies\n", err)
	var stored []T
	for _, key := range storeKeys(held.view.store) {
		if copied, ok := cachedCopy[T](held.view, key); ok {
			stored = append(stored, *copied)
		}
	}
	return stored
}

// storeKeys answers the store's keys in order, which is the order of a
// list from the API server.
func storeKeys(store cache.Store) []string {
	keys := store.ListKeys()
	slices.Sort(keys)
	return keys
}

// keyPath turns a store key into the path of one object, for a kind
// whose objects are namespaced.
func keyPath(path func(namespace, name string) string) func(key string) string {
	return func(key string) string {
		namespace, name, _ := strings.Cut(key, "/")
		return path(namespace, name)
	}
}
