package main

// The pass reads this machine's Sinks and Sources from the two watches'
// stores, not from the API server. Each store holds every resource
// whose status.node is this machine, the same selection the pass lists,
// so a settled pass sends the API server no read at all.
//
// A resource the store does not hold is read from the API server. That
// is a resource no machine has written status.node on yet, one whose
// status.node names another machine, such as a Bluetooth speaker that
// moved here, or any resource while the watch has not finished its
// first read. A list comes from a store only after the store holds the
// whole first read, because a store that holds part of it would leave
// a resource out of the sweep.
//
// A copy in a store can be older than this operator's own last write,
// because the watch delivers the write a moment after the API server
// answers it, and later still while the watch is down. The pass writes
// status only where it differs from the copy it read, so a pass that
// read such a copy would skip a write the resource needs: a claim
// released after the write that recorded it matches the older copy,
// and the API server would keep the claim. So the operator remembers
// the resourceVersion of each resource's newest copy that it wrote or
// read from the API server (versionMemo), and reads a resource from the
// API server when the store's copy has another version. Once the watch
// delivers the write, the versions match and the store answers again.
//
// A write from a copy that another writer changed since carries an
// older resourceVersion, and the API server answers 409 Conflict. The
// write then reads the resource from the API server and writes once
// more, if the fresh copy still needs the write. A copy of a resource
// somebody deleted answers 404, and each caller handles that the way
// it handles a resource that is absent.

import (
	"errors"
	"slices"
	"strings"
	"sync"

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

// versionMemo remembers, for each object by its store key, the
// resourceVersion of the newest copy this operator wrote or read from
// the API server. A version is compared only for equality, because the
// API server gives it no order. A nil memo remembers nothing, and every
// copy in a store is current to it.
type versionMemo struct {
	mu   sync.Mutex
	seen map[string]string

	// requests holds, for each object, one request at a time with the
	// note of its answer, so the memo notes the answers in the order the
	// API server gave them. Two goroutines that write one object could
	// otherwise note the older answer last, and a store's copy at that
	// older version would then count as current. Requests about other
	// objects do not wait.
	requests map[string]*sync.Mutex
}

func newVersionMemo() *versionMemo {
	return &versionMemo{seen: map[string]string{}, requests: map[string]*sync.Mutex{}}
}

// requestsOf answers the lock of one object's requests.
func (m *versionMemo) requestsOf(key string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	held, ok := m.requests[key]
	if !ok {
		held = &sync.Mutex{}
		m.requests[key] = held
	}
	return held
}

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
// version records an object the API server no longer holds, or one
// another writer changed, and no copy in a store matches it.
func (m *versionMemo) note(key, version string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[key] = version
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

// send runs one request about one object, and notes the version of the
// copy the API server answered. A failed request notes the empty
// version: after a 404 or a 409 the operator holds no copy of the API
// server's, and a write whose answer was lost may have landed. The
// next read of the object then goes to the API server.
func (m *versionMemo) send(key string, request func() (version string, err error)) error {
	if m == nil {
		_, err := request()
		return err
	}
	requests := m.requestsOf(key)
	requests.Lock()
	defer requests.Unlock()
	version, err := request()
	if err != nil {
		version = ""
	}
	m.note(key, version)
	return err
}

// heldObjects is one kind's store, and the memo of the copies this
// operator wrote or read.
type heldObjects struct {
	view     storeView
	versions *versionMemo
}

// metaObject is a resource of this operator's API, which carries its
// metadata in EndpointMeta.
type metaObject interface{ meta() *EndpointMeta }

func (s *Sink) meta() *EndpointMeta   { return &s.Metadata }
func (s *Source) meta() *EndpointMeta { return &s.Metadata }

// storeKey is the key a store holds an object under: the name, because
// both kinds are cluster-scoped.
func storeKey(meta *EndpointMeta) string { return meta.Name }

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
	var fresh *T
	err := versions.send(key, func() (string, error) {
		var err error
		if fresh, err = get[T](c, path); err != nil {
			return "", err
		}
		return P(fresh).meta().ResourceVersion, nil
	})
	return fresh, err
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
}](c *Client, held heldObjects, path func(key string) string) ([]T, error) {
	keys := held.view.store.ListKeys()
	if held.view.whole {
		keys = append(keys, held.versions.unheld(held.view.store)...)
	}
	slices.Sort(keys)
	// The store can take a key between the two reads, so a key can
	// appear twice.
	keys = slices.Compact(keys)
	current := make([]T, 0, len(keys))
	for _, key := range keys {
		if copied, ok := cachedCopy[T](held.view, key); ok && held.versions.current(key, P(copied).meta().ResourceVersion) {
			current = append(current, *copied)
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
// is noted in versions. After an error, held carries the status that
// apply set, which the API server did not take.
func settleStatus[T any, P interface {
	*T
	metaObject
}](c *Client, versions *versionMemo, path string, held *T, apply func(*T) bool) (bool, error) {
	key := storeKey(P(held).meta())
	write := func() error {
		return versions.send(key, func() (string, error) {
			if err := replaceStatus(c, path, held); err != nil {
				return "", err
			}
			return P(held).meta().ResourceVersion, nil
		})
	}
	if !apply(held) {
		return false, nil
	}
	err := write()
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
	if err := write(); err != nil {
		return false, err
	}
	return true, nil
}

// objectCache is the stores the pass reads, and the memos of the copies
// it wrote or read. The zero value holds nothing, and every read goes to
// the API server.
type objectCache struct {
	sinks, sources heldObjects
}
