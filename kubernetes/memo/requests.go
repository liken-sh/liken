package memo

// The requests whose answers the memo notes: a read of one object from
// the API server, a write that answers the stored copy, and a status
// write that settles on the API server's copy. None of them reads a
// watch's store, so they import nothing from k8s.io, and a program that
// must not link client-go can link them. library-operator's pod build
// is such a program: it runs no pass, but it compiles the pass's code,
// which writes status through these functions, so it links them. The
// informer package names the read and the status write as well, for a
// pass that also reads a store.
//
// A write from a copy that another writer changed since carries an
// older resourceVersion, and the API server answers 409 Conflict.
// SettleStatus then reads the object from the API server and writes
// once more, if the fresh copy still needs the write. A copy of an
// object somebody deleted answers 404, and each caller handles that the
// way it handles an object that is absent.

import (
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// Meta is the part of an object's metadata that the memo and a store
// read.
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
// namespaced object and the name for a cluster-scoped one. The memo
// records each object under the same key.
func Key(meta Meta) string {
	if meta.GetNamespace() == "" {
		return meta.GetName()
	}
	return meta.GetNamespace() + "/" + meta.GetName()
}

// NamespacedPath answers the API path of the object a key names, from
// the path of one object by its namespace and name. A list that reads
// an object again from the API server has only the key. A key with no
// namespace names a cluster-scoped object, and path takes an empty
// namespace for it.
func NamespacedPath(path func(namespace, name string) string) func(key string) string {
	return func(key string) string {
		namespace, name, namespaced := strings.Cut(key, "/")
		if !namespaced {
			return path("", key)
		}
		return path(namespace, name)
	}
}

// ReadFresh reads one object from the API server and notes its version.
func ReadFresh[T any, P Object[T]](c *apiclient.Client, versions *Versions, key, path string) (*T, error) {
	return Written[T, P](versions, key, func() (*T, error) { return apiclient.Get[T](c, path) })
}

// Written sends one request that answers an object, such as a create,
// an apply, or an update, and notes the version of the copy the API
// server answered. A request that fails answers no copy, and notes that
// the operator holds no current copy.
func Written[T any, P Object[T]](versions *Versions, key string, request func() (*T, error)) (*T, error) {
	var answer *T
	err := versions.Send(key, func() (string, error) {
		copied, err := request()
		if err != nil {
			return "", err
		}
		answer = copied
		return P(copied).GetObjectMeta().GetResourceVersion(), nil
	})
	return answer, err
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
func SettleStatus[T any, P Object[T]](c *apiclient.Client, versions *Versions, path string, held *T, apply func(*T) bool) (bool, error) {
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
