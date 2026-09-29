package informer

// A pass reads the objects a watch holds from the watch's store, not
// from the API server, so a settled pass sends the API server no read
// of a watched kind.
//
// A store answers only while it is ready: it holds the whole first
// read, and the API server accepted a watch and forbade none since, so
// a watch keeps the store current. A store that holds part of the first read would
// leave objects out of a list, and a store whose watch the API server
// forbids holds no change made since its last list. A store whose
// watch fails while the API server is down stays ready (Synced in
// informer.go). While a store is not ready, every read goes to the API
// server. An object that a ready store does not hold is read from the
// API server too, because it can be an object the operator created a
// moment ago.
//
// A store's copy can be older than the operator's own last write. The
// memo package records the version of each copy the API server
// answered, and a store's copy at another version is read from the API
// server once. When the watch delivers the write, the store answers
// again.
//
// A write from a copy that another writer changed since carries an
// older resourceVersion, and the API server answers 409 Conflict.
// SettleStatus then reads the object from the API server and writes
// once more, if the fresh copy still needs the write. A copy of an
// object somebody deleted answers 404, and each caller handles that the
// way it handles an object that is absent.

import (
	"errors"
	"slices"
	"strings"

	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// View is one watch's store, and whether the store holds the whole
// first read. The zero View holds nothing, and every read goes to the
// API server.
type View struct {
	Store  cache.Store
	Synced func() bool

	// Whole says the watch takes every object of the kind, with no
	// selector. A list from such a store also holds each object the
	// operator created or wrote that the store does not hold yet. Leave
	// it false for a store with a selector, because an object the
	// operator wrote can be outside the selection.
	Whole bool
}

// Ready reports whether a list can come from the store.
func (v View) Ready() bool {
	return v.Store != nil && v.Synced != nil && v.Synced()
}

// Held is one kind's store, and the memo of the copies the operator
// wrote or read.
type Held struct {
	View     View
	Versions *memo.Versions
}

// Meta is the part of an object's metadata that the cache reads.
type Meta interface {
	GetNamespace() string
	GetName() string
	GetResourceVersion() string
}

// Object is a pointer to an operator's struct for one kind, which
// answers the object's metadata.
type Object[T any] interface {
	*T
	GetObjectMeta() Meta
}

// Key is the key a store holds an object under: namespace/name for a
// namespaced object and the name for a cluster-scoped one.
func Key(meta Meta) string {
	if meta.GetNamespace() == "" {
		return meta.GetName()
	}
	return meta.GetNamespace() + "/" + meta.GetName()
}

// Cached answers the store's copy of one object, by its key, while the
// store is ready. A copy that does not convert is reported and not
// answered. The caller reads the API server when Cached answers
// nothing.
func Cached[T any](view View, key string) (*T, bool) {
	if !view.Ready() {
		return nil, false
	}
	object, held, err := view.Store.GetByKey(key)
	if err != nil || !held {
		return nil, false
	}
	item, err := Convert[T](object)
	if err != nil {
		Report("the cached "+key, err)
		return nil, false
	}
	return &item, true
}

// CachedList answers every copy in the store, in the order of their
// keys, which is the order of a list from the API server. A copy that
// does not convert is reported and left out, the same as a watch event
// that does not convert.
func CachedList[T any](view View) []T {
	objects := view.Store.List()
	slices.SortFunc(objects, func(a, b any) int { return strings.Compare(objectKey(a), objectKey(b)) })
	var items []T
	for _, object := range objects {
		item, err := Convert[T](object)
		if err != nil {
			Report("the cached objects", err)
			continue
		}
		items = append(items, item)
	}
	return items
}

// objectKey is the key a store holds an object under.
func objectKey(object any) string {
	key, _ := cache.MetaNamespaceKeyFunc(object)
	return key
}

// ReadOne answers one object: the store's copy when the store is ready
// and holds a current copy, and the API server's copy otherwise.
func ReadOne[T any, P Object[T]](c *apiclient.Client, held Held, key, path string) (*T, error) {
	if copied, ok := Cached[T](held.View, key); ok && held.Versions.Current(key, P(copied).GetObjectMeta().GetResourceVersion()) {
		return copied, nil
	}
	return ReadFresh[T, P](c, held.Versions, key, path)
}

// ReadFresh reads one object from the API server and notes its version.
func ReadFresh[T any, P Object[T]](c *apiclient.Client, versions *memo.Versions, key, path string) (*T, error) {
	var fresh *T
	err := versions.Send(key, func() (string, error) {
		var err error
		if fresh, err = apiclient.Get[T](c, path); err != nil {
			return "", err
		}
		return P(fresh).GetObjectMeta().GetResourceVersion(), nil
	})
	return fresh, err
}

// CurrentList answers the store's copies, in the order of their keys. A
// copy that is older than the operator's own last write, or that does
// not convert, is replaced by the API server's copy, and an object the
// API server no longer holds is left out. A store that holds the whole
// collection also answers each object the memo noted and the store does
// not hold yet, such as one the operator created a moment ago, so a
// pass does not create it again. path names the object of each key.
func CurrentList[T any, P Object[T]](c *apiclient.Client, held Held, path func(key string) string) ([]T, error) {
	// The informer can take a created object between the two reads, so
	// the memo's keys are read first. In the other order, the store's
	// keys miss the object, the memo skips it because the store now
	// holds it, and the list leaves it out, so a pass creates it again.
	// In this order the key is in one read or in both.
	var keys []string
	if held.View.Whole {
		keys = held.Versions.Unheld(held.View.Store)
	}
	keys = append(keys, held.View.Store.ListKeys()...)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	current := make([]T, 0, len(keys))
	for _, key := range keys {
		if copied, ok := Cached[T](held.View, key); ok && held.Versions.Current(key, P(copied).GetObjectMeta().GetResourceVersion()) {
			current = append(current, *copied)
			continue
		}
		fresh, err := ReadFresh[T, P](c, held.Versions, key, path(key))
		if errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		current = append(current, *fresh)
	}
	return current, nil
}

// SettleStatus writes the status that apply sets on a copy of an
// object. apply composes the status from the copy it is given, sets it,
// and reports whether the copy needs the write. A write refused because
// the copy is older than the API server's, or because the object is
// gone, reads the object again, applies again to the fresh copy, and
// writes once more. SettleStatus reports whether a write landed, and an
// object that is gone answers apiclient.ErrNotFound. Each copy the API
// server answers is noted in versions. After an error, held holds the
// status that apply set, which the API server did not take.
func SettleStatus[T any, P Object[T]](c *apiclient.Client, versions *memo.Versions, path string, held *T, apply func(*T) bool) (bool, error) {
	key := Key(P(held).GetObjectMeta())
	write := func() error {
		return versions.Send(key, func() (string, error) {
			if err := apiclient.ReplaceStatus(c, path, held); err != nil {
				return "", err
			}
			return P(held).GetObjectMeta().GetResourceVersion(), nil
		})
	}
	if !apply(held) {
		return false, nil
	}
	err := write()
	if !apiclient.Stale(err) {
		return err == nil, err
	}
	current, err := ReadFresh[T, P](c, versions, key, path)
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
