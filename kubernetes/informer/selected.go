package informer

import (
	"errors"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// ReadSelected is ReadOne for a store whose watch selects every object
// that a caller can read with it, such as a watch on the label that the
// operator sets on the objects it creates, or a watch of one object by
// name. When such a store is ready and lacks a key that watched.Versions
// has no record of, the object does not exist, and ReadSelected returns
// apiclient.ErrNotFound without a request. ReadOne cannot do that,
// because its store can leave out an object that exists.
//
// A key with a record and no copy in the store, such as an object that
// the operator created a moment ago or one whose write failed, is read
// from the API server on each call, until the watch adds the object to
// the store or the API server returns 404. A 404 drops the record, so
// the next read returns ErrNotFound without a request.
func ReadSelected[T any, P Object[T]](c *apiclient.Client, watched Held, key, path string) (*T, error) {
	if watched.View.Ready() {
		if _, stored, _ := watched.View.Store.GetByKey(key); !stored && !watched.Versions.Noted(key) {
			return nil, apiclient.ErrNotFound
		}
	}
	found, err := ReadOne[T, P](c, watched, key, path)
	if errors.Is(err, apiclient.ErrNotFound) {
		watched.Versions.Forget(key)
	}
	ForgetStored(watched, key)
	return found, err
}

// ForgetStored drops the record in watched.Versions of each key whose copy
// in a ready store is at the recorded version (memo.Versions.ForgetAt).
//
// Dropping a record also drops the key's request lock, so call it only
// from the goroutine that sends requests about the key, or while none
// of those goroutines runs (memo.Versions.Forget).
func ForgetStored(watched Held, keys ...string) {
	if watched.Versions == nil || !watched.View.Ready() {
		return
	}
	for _, key := range keys {
		object, stored, err := watched.View.Store.GetByKey(key)
		if err != nil || !stored {
			continue
		}
		if copied, ok := object.(interface{ GetResourceVersion() string }); ok {
			watched.Versions.ForgetAt(key, copied.GetResourceVersion())
		}
	}
}
