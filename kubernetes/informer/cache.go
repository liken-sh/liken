package informer

// A reconcile pass reads objects from a watch's store instead of the
// API server, so a pass that changes nothing sends no reads of a
// watched kind.
//
// A pass reads the store only while it is ready (Collection.Synced):
// the store has the whole first list, and the API server has accepted a
// watch and has not refused one with a 401 or a 403 since. A watch that
// fails for another reason, such as while the API server restarts,
// leaves the store ready, unless the collection sets
// Options.UnreadyOnWatchError. While the store is not ready, every read
// goes to the API server. When a ready store does not have an object,
// ReadOne reads it from the API server too, because the operator may
// have just created it.
//
// A store's copy can be older than the operator's own last write. The
// memo records the version of each copy the API server returned, and a
// store copy at another version is replaced by a read from the API
// server until the watch delivers the write.
//
// The requests that record versions are in the memo package, so that a
// program that must not link client-go can send them. This package
// re-exports ReadFresh and SettleStatus, so a pass that reads a store
// and writes needs one import.

import (
	"errors"
	"slices"
	"strings"

	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// View is one watch's store and its Synced function. With the zero
// View, every read goes to the API server.
type View struct {
	Store  cache.Store
	Synced func() bool

	// Whole is true when the watch has no selector. CurrentList
	// then also includes the objects that the memo recorded and the
	// store does not have yet. Leave it false for a watch with a
	// selector, because an object the operator wrote can be outside
	// the selection.
	Whole bool

	// Absent is true when the store is empty because the cluster
	// does not serve the kind (Options.Absent). Check it before a
	// create of the kind, which the API server would refuse.
	Absent bool
}

// Ready reports whether the store can be read instead of the API
// server.
func (v View) Ready() bool {
	return v.Store != nil && v.Synced != nil && v.Synced()
}

// Held is what a pass reads one kind from: the View of the kind's
// watch, and the memo of the versions of that kind that the operator
// wrote or read from the API server.
type Held struct {
	View     View
	Versions *memo.Versions
}

// Meta is the part of an object's metadata that the cache reads.
type Meta = memo.Meta

type Object[T any] = memo.Object[T]

// Key returns the store key of an object (memo.Key).
func Key(meta Meta) string { return memo.Key(meta) }

// Cached returns the store's copy of the object with this key, while
// the store is ready. A copy that does not convert to T is reported and
// treated as missing.
func Cached[T any](view View, key string) (*T, bool) {
	if !view.Ready() {
		return nil, false
	}
	object, stored, err := view.Store.GetByKey(key)
	if err != nil || !stored {
		return nil, false
	}
	item, err := Convert[T](object)
	if err != nil {
		Report("the cached "+key, err)
		return nil, false
	}
	return &item, true
}

// CachedList returns every copy in the store, sorted by key, which is
// the order of a list from the API server. A copy that does not convert
// to T is reported and left out.
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

func objectKey(object any) string {
	key, _ := cache.MetaNamespaceKeyFunc(object)
	return key
}

// ReadOne returns the store's copy of an object when the store is ready
// and the copy is at the version the memo recorded. Otherwise it reads
// the object from the API server.
func ReadOne[T any, P Object[T]](c *apiclient.Client, watched Held, key, path string) (*T, error) {
	if copied, ok := Cached[T](watched.View, key); ok && watched.Versions.Current(key, P(copied).GetObjectMeta().GetResourceVersion()) {
		return copied, nil
	}
	return ReadFresh[T, P](c, watched.Versions, key, path)
}

func ReadFresh[T any, P Object[T]](c *apiclient.Client, versions *memo.Versions, key, path string) (*T, error) {
	return memo.ReadFresh[T, P](c, versions, key, path)
}

// CurrentList returns the store's copies sorted by key. A copy that is
// older than the operator's own last write, or that does not convert,
// is replaced by a read from the API server, and an object that returns
// 404 is left out. When any read fails, the list fails.
//
// When the store is Whole, the list also includes each object that the
// memo recorded and the store does not have yet (memo.Versions.Unheld),
// such as one the operator just created, so a pass does not create it
// again. path returns the API path of the object with a given key.
func CurrentList[T any, P Object[T]](c *apiclient.Client, watched Held, path func(key string) string) ([]T, error) {
	// Read the memo's keys before the store's. The informer can add
	// a created object between the two reads. In the other order,
	// the store's keys would miss it, and Unheld would skip it
	// because the store has it by then.
	var keys []string
	if watched.View.Whole {
		keys = watched.Versions.Unheld(watched.View.Store)
	}
	keys = append(keys, watched.View.Store.ListKeys()...)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	current := make([]T, 0, len(keys))
	for _, key := range keys {
		if copied, ok := Cached[T](watched.View, key); ok && watched.Versions.Current(key, P(copied).GetObjectMeta().GetResourceVersion()) {
			current = append(current, *copied)
			continue
		}
		fresh, err := ReadFresh[T, P](c, watched.Versions, key, path(key))
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

// List returns every object of one kind. While the store is ready it
// returns CurrentList. Otherwise it lists listPath from the API server
// and records the version of each object, because the store can become
// ready at an older version than that list (memo.Versions.SendList).
//
// listPath must have the same selector as the watch, and must not set
// resourceVersion. A list with no resourceVersion is a consistent read,
// so it has every write that the API server acknowledged before the
// list. With resourceVersion=0, the watch cache can serve an older
// list. A pass that reads a kind from a store must list it through
// List, because a list that bypasses List records no versions, and the
// next pass can act on an older copy.
func List[T any, P Object[T]](c *apiclient.Client, watched Held, listPath string, path func(key string) string) ([]T, error) {
	if watched.View.Ready() {
		return CurrentList[T, P](c, watched, path)
	}
	return memo.ReadList[T, P](c, watched.Versions, listPath)
}

// SettleStatus writes the status that apply sets on object, and after a
// 409 or a 404 reads the object again and retries once
// (memo.SettleStatus).
func SettleStatus[T any, P Object[T]](c *apiclient.Client, versions *memo.Versions, path string, object *T, apply func(*T) bool) (bool, error) {
	return memo.SettleStatus[T, P](c, versions, path, object, apply)
}
