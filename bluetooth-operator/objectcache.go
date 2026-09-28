package main

// The pass reads the Adapters, the Peripherals of its radio, the
// PairingRequests, and the bond Secrets from the watches' stores, not
// from the API server. Each store holds the same selection that the
// pass would list, so a settled pass sends the API server no read of
// these four kinds.
//
// An object that a store does not hold is read from the API server.
// That is an object that does not exist yet, or any object while its
// watch has not finished its first read. A list comes from a store
// only after the store holds the whole first read, because a store
// that holds part of it would leave objects out of the pass. The
// Peripheral watch follows the radio the pass reads, so on the first
// pass for a radio its store is not ready, and the pass lists the
// Peripherals from the API server.
//
// A copy in a store can be older than this operator's own last write,
// because the watch delivers the write a moment after the API server
// answers it, and later still while the watch is down. A pass that
// acted on such a copy would act again on a change it already made,
// such as opening the pairing window for a request it already paired.
// So the operator remembers the resourceVersion of each object's newest
// copy that it wrote or read from the API server (versionMemo), and
// reads an object from the API server when the store's copy has another
// version. Once the watch delivers the write, the versions match and
// the store answers again.
//
// A write from a copy that another writer changed since carries an
// older resourceVersion, and the API server answers 409 Conflict. The
// write then reads the object from the API server and writes once
// more, if the fresh copy still needs the write. A copy of an object
// somebody deleted answers 404, and each caller handles that the way
// it handles an object that is absent.

import (
	"errors"
	"sync"

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

// versionMemo remembers, for each object by its store key, the
// resourceVersion of the newest copy this operator wrote or read from
// the API server. A version is compared only for equality, because the
// API server gives it no order. A nil memo remembers nothing, and every
// copy in a store is current to it.
type versionMemo struct {
	mu   sync.Mutex
	seen map[string]string
}

func newVersionMemo() *versionMemo { return &versionMemo{seen: map[string]string{}} }

// current reports whether a store's copy at this version is at least as
// new as every copy this operator wrote or read.
func (m *versionMemo) current(key, version string) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen, noted := m.seen[key]
	return !noted || seen == version
}

// note records the version of a copy the API server answered. An empty
// version records an object the API server no longer holds, and no
// copy in a store matches it.
func (m *versionMemo) note(key, version string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[key] = version
}

// heldObjects is one kind's store, and the memo of the copies this
// operator wrote or read.
type heldObjects struct {
	view     storeView
	versions *versionMemo
}

// metaObject is a custom resource of this operator's API, which carries
// its metadata in ObjectMeta.
type metaObject interface{ meta() *ObjectMeta }

func (a *Adapter) meta() *ObjectMeta        { return &a.Metadata }
func (p *Peripheral) meta() *ObjectMeta     { return &p.Metadata }
func (r *PairingRequest) meta() *ObjectMeta { return &r.Metadata }

// storeKey is the key a store holds an object under: namespace/name for
// a namespaced object and the name for a cluster-scoped one.
func storeKey(meta *ObjectMeta) string {
	if meta.Namespace == "" {
		return meta.Name
	}
	return meta.Namespace + "/" + meta.Name
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

// cachedList answers every copy in the store. A copy that does not
// convert is logged and left out, the same as a watch event that does
// not convert.
func cachedList[T any](view storeView) []T {
	var items []T
	for _, object := range view.store.List() {
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
}](c *Client, held heldObjects, key, path string) (*T, error) {
	if copied, ok := cachedCopy[T](held.view, key); ok && held.versions.current(key, P(copied).meta().ResourceVersion) {
		return copied, nil
	}
	return readFresh[T, P](c, held.versions, key, path)
}

// readFresh reads one object from the API server and notes its version.
func readFresh[T any, P interface {
	*T
	metaObject
}](c *Client, versions *versionMemo, key, path string) (*T, error) {
	fresh, err := get[T](c, path)
	switch {
	case err == nil:
		versions.note(key, P(fresh).meta().ResourceVersion)
	case errors.Is(err, ErrNotFound):
		versions.note(key, "")
	}
	return fresh, err
}

// currentList answers the store's copies, with each copy that is older
// than this operator's own last write replaced by the API server's
// copy, and each object the API server no longer holds left out.
func currentList[T any, P interface {
	*T
	metaObject
}](c *Client, held heldObjects, path func(key string) string) ([]T, error) {
	items := cachedList[T](held.view)
	current := items[:0]
	for index := range items {
		meta := P(&items[index]).meta()
		key := storeKey(meta)
		if held.versions.current(key, meta.ResourceVersion) {
			current = append(current, items[index])
			continue
		}
		fresh, err := readFresh[T, P](c, held.versions, key, path(key))
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

// stale reports whether a write failed because the copy it was made
// from is not the API server's copy.
func stale(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound)
}

// settleStatus writes the status that apply sets on a copy of an
// object. apply composes the status from the copy it is given, sets it,
// and reports whether the copy needs the write. A write refused because
// the copy is older than the API server's, or because the object is
// gone, reads the object again, applies again to the fresh copy, and
// writes once more. It reports whether a write landed, and an object
// that is gone answers ErrNotFound. Each copy the API server answers
// is noted in versions.
func settleStatus[T any, P interface {
	*T
	metaObject
}](c *Client, versions *versionMemo, path string, held *T, apply func(*T) bool) (bool, error) {
	key := storeKey(P(held).meta())
	if !apply(held) {
		return false, nil
	}
	err := replaceStatus(c, path, held)
	if err == nil {
		versions.note(key, P(held).meta().ResourceVersion)
	}
	if !stale(err) {
		return err == nil, err
	}
	current, err := readFresh[T, P](c, versions, key, path)
	if err != nil {
		return false, err
	}
	*held = *current
	if !apply(held) {
		return false, nil
	}
	if err := replaceStatus(c, path, held); err != nil {
		return false, err
	}
	versions.note(key, P(held).meta().ResourceVersion)
	return true, nil
}

// followedView is the store of a watch whose selector follows one
// radio, and the radio the store is for. A reader asks for the store
// of the radio it holds, and gets nothing while the watch for that
// radio has not started.
type followedView struct {
	mu   sync.Mutex
	key  string
	view storeView
}

func (f *followedView) set(key string, view storeView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key, f.view = key, view
}

// of answers the store for one radio, or the zero view when the watch
// holds another radio's objects.
func (f *followedView) of(key string) storeView {
	if f == nil {
		return storeView{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.key != key {
		return storeView{}
	}
	return f.view
}

// objectCache is the stores the inventory pass reads, and the memos of
// the copies it wrote or read. The zero value holds nothing, and every
// read goes to the API server.
type objectCache struct {
	adapters    heldObjects
	requests    heldObjects
	peripherals *followedView

	// peripheralVersions is the memo for the Peripherals, which
	// outlives the watch of any one radio.
	peripheralVersions *versionMemo
}

// peripheralsOf answers the Peripheral store for one radio, with its
// memo.
func (c objectCache) peripheralsOf(adapterKey string) heldObjects {
	return heldObjects{view: c.peripherals.of(adapterKey), versions: c.peripheralVersions}
}
