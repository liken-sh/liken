// Package watch holds what liken's two operators add to the shared
// watches in kubernetes/informer: the handlers that wake a pass
// (wake.go), and the reads a pass makes from a watch's store.
//
// A pass reads the store instead of the API server, so a settled pass
// sends no read of a watched kind. The shared informer package answers
// a read only while the store is ready: it holds the whole first read,
// and the API server accepted a watch and forbade none since. Until
// then, the pass reads the API server.
//
// liken's watches each select exactly the objects a pass reads, often
// one object by name. So a ready store that does not hold an object
// answers that the object does not exist, and the pass sends no
// request to learn it. That is the one rule these reads add to the
// shared ones.
//
// The CLI imports the kubernetes package and watches nothing. This is
// a package of its own, apart from that one, so the CLI links no
// client-go.
package watch

import (
	"errors"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// Get reads one object from a store by its key: the name of a
// cluster-scoped object, or namespace/name. ok is false when the store
// cannot answer: it is not ready, or the object does not convert
// (which is logged). The caller then reads the API server. found is
// false when a ready store holds no such object.
func Get[T any](view informer.View, key string) (item *T, found, ok bool) {
	if !view.Ready() {
		return nil, false, false
	}
	object, exists, err := view.Store.GetByKey(key)
	if err != nil {
		return nil, false, false
	}
	if !exists {
		return nil, false, true
	}
	out, err := informer.Convert[T](object)
	if err != nil {
		informer.Report("the cached "+key, err)
		return nil, false, false
	}
	return &out, true, true
}

// List reads every object in a store, in the order of their keys. ok
// is false when the store cannot answer, and the caller then reads the
// API server. An object that does not convert makes the whole answer
// unusable, because a pass that judges a collection must not judge it
// with an object missing: a heartbeat Lease left out would read as a
// machine that stopped renewing.
func List[T any](view informer.View) (items []T, ok bool) {
	if !view.Ready() {
		return nil, false
	}
	return convertAll[T](view.Store.List())
}

// ByIndex reads the objects of one index value, the same way List
// reads the whole store.
func ByIndex[T any](view informer.View, index, value string) (items []T, ok bool) {
	if !view.Ready() {
		return nil, false
	}
	indexer, isIndexer := view.Store.(cache.Indexer)
	if !isIndexer {
		return nil, false
	}
	objects, err := indexer.ByIndex(index, value)
	if err != nil {
		return nil, false
	}
	return convertAll[T](objects)
}

func convertAll[T any](objects []any) ([]T, bool) {
	slices.SortFunc(objects, func(a, b any) int { return strings.Compare(objectKey(a), objectKey(b)) })
	items := make([]T, 0, len(objects))
	for _, object := range objects {
		item, err := informer.Convert[T](object)
		if err != nil {
			informer.Report("the cached objects", err)
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

// objectKey is the key a store holds an object under.
func objectKey(object any) string {
	key, _ := cache.MetaNamespaceKeyFunc(object)
	return key
}

// LabelIndex is an index of the objects by the value of one label.
func LabelIndex(label string) cache.IndexFunc {
	return func(object any) ([]string, error) {
		item, ok := object.(*unstructured.Unstructured)
		if !ok {
			return nil, nil
		}
		value, set := item.GetLabels()[label]
		if !set {
			return nil, nil
		}
		return []string{value}, nil
	}
}

// ReadOne reads one object of a kind this operator writes, through the
// memo of its writes (the shared memo package). The store's copy
// answers when it is at the version of the operator's last write or
// read, and the API server answers otherwise, once, and then the store
// answers again.
//
// A ready store that holds no such object, and whose memo holds no
// record of it, answers apiclient.ErrNotFound with no request. An
// object the memo noted, such as one whose write failed, is read from
// the API server until the store holds it or the API server answers
// that it is gone. Then the memo forgets it, so the next read answers
// from the store and sends nothing.
func ReadOne[T any, P informer.Object[T]](c *apiclient.Client, held informer.Held, key, path string) (*T, error) {
	if held.View.Ready() {
		if _, stored, _ := held.View.Store.GetByKey(key); !stored && !held.Versions.Noted(key) {
			return nil, apiclient.ErrNotFound
		}
	}
	found, err := informer.ReadOne[T, P](c, held, key, path)
	if errors.Is(err, apiclient.ErrNotFound) {
		held.Versions.Forget(key)
	}
	return found, err
}
