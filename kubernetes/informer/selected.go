package informer

// A read of one object from a store whose watch selects every object
// that the read can ask for. An operator's watch of the objects it
// creates selects them by the operator's label, and each object it
// creates carries that label. A watch that follows one object by name
// selects that object. A ready store of such a watch holds each object
// that exists, except one the operator wrote a moment ago, which the
// memo noted. So the store's absence of an object that the memo has not
// noted means that the object does not exist, and the read sends the
// API server nothing.
//
// ReadOne cannot rely on that, because a store with a narrower
// selection can lack an object that exists, so it reads each object
// that the store does not hold from the API server.

import (
	"errors"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// ReadSelected answers one object whose store's watch selects it: the
// store's copy when it is at the version of the operator's last write
// or read, and the API server's copy otherwise. A ready store that does
// not hold the key, and whose memo has not noted it, answers
// apiclient.ErrNotFound with no request. An object the memo noted, such
// as one the operator created a moment ago or one whose write failed,
// is read from the API server until the store holds it or the API
// server answers that it is gone.
//
// After a 404 the memo forgets the key, so the next read answers from
// the store and sends nothing. When the store's copy is at the noted
// version, the memo drops its record too (ForgetStored).
func ReadSelected[T any, P Object[T]](c *apiclient.Client, held Held, key, path string) (*T, error) {
	if held.View.Ready() {
		if _, stored, _ := held.View.Store.GetByKey(key); !stored && !held.Versions.Noted(key) {
			return nil, apiclient.ErrNotFound
		}
	}
	found, err := ReadOne[T, P](c, held, key, path)
	if errors.Is(err, apiclient.ErrNotFound) {
		held.Versions.Forget(key)
	}
	ForgetStored(held, key)
	return found, err
}

// ForgetStored drops the memo's record of each key whose copy in a
// ready store is at the version the memo noted. From then on the store
// holds the operator's write, and each later copy is newer, so the
// record guards nothing. A record that stayed would make each later
// write from another writer cost one read from the API server, because
// the store's copy would differ from the record.
//
// ForgetStored, like the Forget in ReadSelected, drops the memo's
// request lock of the key (memo.Versions.Forget says what that allows).
// So an operator calls it only from the goroutine that sends the
// requests about the key, or while none of them runs.
func ForgetStored(held Held, keys ...string) {
	if held.Versions == nil || !held.View.Ready() {
		return
	}
	for _, key := range keys {
		object, stored, err := held.View.Store.GetByKey(key)
		if err != nil || !stored {
			continue
		}
		if copied, ok := object.(interface{ GetResourceVersion() string }); ok {
			held.Versions.ForgetAt(key, copied.GetResourceVersion())
		}
	}
}
