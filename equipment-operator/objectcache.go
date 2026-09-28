package main

// A pass reads the collections it acts on from the stores of the
// informers that watch them, not from the API server. Each store holds
// the whole collection, apart from the node workload's Display store,
// which holds the Displays of its machine. So a settled pass sends the
// API server no read of the Receivers, the CECBuses, the Televisions,
// or the Displays.
//
// A pass reads the API server instead when a store has nothing to give
// it: before the informer's first read is done, after the watch
// stopped, and when the kind has no watch yet because its definition
// is missing. So a pass never acts on an empty store as if the
// collection were empty. A copy in a store that does not convert is
// read from the API server as well.
//
// A copy in a store can be older than this operator's own last write,
// because the watch delivers the write a moment after the API server
// answers it, and later still while the watch is down. A pass that
// acted on such a copy would act again on a change it already made: a
// Receiver unit that took the settled state from the copy would send a
// receiver a setting again, and a status compared with the copy would
// be written again with a new lastTransitionTime. So the operator
// remembers the resourceVersion of each object's newest copy that it
// wrote or read from the API server (versionMemo), and reads an object
// from the API server when the store's copy has another version. Once
// the watch delivers the write, the versions match and the store
// answers again. A list also reads each object the operator created
// that the store does not hold yet, so a pass does not create it
// again, and leaves out each object the operator deleted that the
// store still holds.
//
// Every write here is a server-side apply, a create, or a delete. An
// apply states no resourceVersion, so a stale copy never makes the API
// server refuse it, and this operator needs no write that retries from
// a fresh copy. A 409 here means a create whose name exists, or an
// apply whose uid precondition names an object deleted since. The memos are the Client's (objectVersions), because
// every write goes through the Client.

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

// metaObject is a resource this operator reads, which carries its
// metadata in ObjectMeta.
type metaObject interface{ meta() *ObjectMeta }

func (r *Receiver) meta() *ObjectMeta   { return &r.Metadata }
func (b *CECBus) meta() *ObjectMeta     { return &b.Metadata }
func (t *Television) meta() *ObjectMeta { return &t.Metadata }
func (d *Display) meta() *ObjectMeta    { return &d.Metadata }

// storeKey is the key a store holds an object under: the name, because
// every kind this operator reads is cluster-scoped.
func storeKey(meta *ObjectMeta) string { return meta.Name }

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

// objectVersions is the memo of each kind this process writes.
type objectVersions struct {
	receivers, cecBuses, televisions *versionMemo
}

func newObjectVersions() objectVersions {
	return objectVersions{receivers: newVersionMemo(), cecBuses: newVersionMemo(), televisions: newVersionMemo()}
}

// written sends one write of an object, and notes the version of the
// copy the API server answered. A write that fails answers no copy.
func written[T any, P interface {
	*T
	metaObject
}](versions *versionMemo, key string, request func() (*T, error)) (*T, error) {
	var answer *T
	err := versions.send(key, func() (string, error) {
		copied, err := request()
		if err != nil {
			return "", err
		}
		answer = copied
		return P(copied).meta().ResourceVersion, nil
	})
	return answer, err
}
