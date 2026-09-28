package main

// A pass reads the Plays from the Play watch's store, through the same
// types and functions every liken-sh operator reads its stores with.
// The view (clusterview.go) holds the store, and the store holds every
// Play in the cluster, so a settled pass sends the API server no read of
// a Play. The pass reads the Plays only as a whole list, and the view
// exists only after every watch has read its collection once, so this
// file holds the list half of the shared code: currentList, and not
// readOne, cachedList, or storeView.ready.
//
// A Play's copy in the store can be older than this operator's own last
// write, because the watch delivers the write a moment after the API
// server answers it, and later still while the watch is down. The pass
// derives a unit's activity from each Play's phase, and publishes it on
// the bus before it reads anything else. A pass that read the copy its
// own write replaced would publish the phase the Play already left, and
// then, when the reconcile wrote the phase again, the phase it had
// moved to: Playing, Starting, Playing on a subscriber's screen. So the
// operator remembers the resourceVersion of each Play's newest copy that
// it wrote or read from the API server (versionMemo), and reads a Play
// from the API server when the store's copy has another version. Once
// the watch delivers the write, the versions match and the store
// answers again.
//
// A status write from a copy that another writer changed since carries
// an older resourceVersion, and the API server answers 409 Conflict.
// settleStatus then reads the Play from the API server, and writes once
// more if the fresh copy still needs the write.
//
// The memo covers only the Plays. The operator writes the status of
// Players and Remotes too, and their stores answer through
// clusterview.go with no memo, because no unit's activity on the bus
// comes from either status.

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"k8s.io/client-go/tools/cache"
)

// storeView is one watch's store. The zero value holds nothing.
type storeView struct {
	store cache.Store

	// whole says the watch takes every object of the kind, with no
	// selector. A list from such a store also holds each object this
	// operator created or wrote that the store does not hold yet. A
	// store with a selector leaves that out, because an object the
	// operator wrote can be outside the selection.
	whole bool
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

// metaObject is a custom resource of this operator's API, which carries
// its metadata in ObjectMeta.
type metaObject interface{ meta() *ObjectMeta }

func (p *Play) meta() *ObjectMeta { return &p.Metadata }

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

// get reads one object from the API server.
func get[T any](c *Client, path string) (*T, error) {
	out := new(T)
	if err := c.RequestJSON(http.MethodGet, path, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// replaceStatus writes an object's status subresource from the copy it
// holds, and puts the API server's answer, with its new
// resourceVersion, back on that copy.
func replaceStatus[T any](c *Client, path string, object *T) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	stored := new(T)
	if err := c.RequestJSON(http.MethodPut, path+"/status", body, stored); err != nil {
		return err
	}
	*object = *stored
	return nil
}

// playPathOf answers the API path of the Play a store key names.
func playPathOf(key string) string {
	namespace, name, _ := strings.Cut(key, "/")
	return playPath(namespace, name)
}

// playKey is the store key of one Play.
func playKey(play *Play) string { return storeKey(&play.Metadata) }

// Plays answers every Play: the store's copy of each Play when it holds
// a current one, and the API server's copy when it does not. A Play the
// API server no longer holds is left out. The view exists only once the
// Play watch has read the whole collection, so the list always comes
// from the store.
//
// A Play is created by a person or another program, and it goes when
// its run is over, so each one the memo noted is forgotten once the API
// server and the store both no longer hold it. Without that, the memo
// would keep a record of every Play for the life of the process.
func (v *clusterView) Plays(c *Client) ([]Play, error) {
	plays, err := currentList[Play](c, v.plays, playPathOf)
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(plays))
	for index := range plays {
		listed[playKey(&plays[index])] = true
	}
	v.plays.versions.forgetGone(v.plays.view.store, listed)
	return plays, nil
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
