package memo

// These requests record what the API server returns in the memo, and
// none of them reads a watch's store, so this file imports nothing from
// k8s.io. A program that must not link client-go can call them.
// library-operator's pod build is one: it links the reconcile code,
// which writes status through SettleStatus.

import (
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// Meta is the part of an object's metadata that the memo and a store
// read. It is an alias of an interface literal, not a named type, so a
// package can implement it without importing this module. liken/api
// does that, because init links liken/api and must not link net/http,
// which apiclient imports.
type Meta = interface {
	GetNamespace() string
	GetName() string
	GetResourceVersion() string
}

// Object is a pointer to an operator's struct for one kind. It returns
// the object's metadata.
type Object[T any] interface {
	*T
	GetObjectMeta() Meta
}

// Key returns the key that a store and the memo use for an object:
// namespace/name for a namespaced object and the name for a
// cluster-scoped one.
func Key(meta Meta) string {
	if meta.GetNamespace() == "" {
		return meta.GetName()
	}
	return meta.GetNamespace() + "/" + meta.GetName()
}

// NamespacedPath converts a path function of namespace and name into
// one of a store key, because a list that reads an object again has
// only its key. For a cluster-scoped key, path receives an empty
// namespace.
func NamespacedPath(path func(namespace, name string) string) func(key string) string {
	return func(key string) string {
		namespace, name, namespaced := strings.Cut(key, "/")
		if !namespaced {
			return path("", key)
		}
		return path(namespace, name)
	}
}

// ReadFresh reads one object from the API server and records its
// version.
func ReadFresh[T any, P Object[T]](c *apiclient.Client, versions *Versions, key, path string) (*T, error) {
	return Written[T, P](versions, key, func() (*T, error) { return apiclient.Get[T](c, path) })
}

// ReadList reads a collection from the API server and records the
// version of each object (Versions.SendList). listPath has the selector
// of the watch whose store the list replaces, and must not set
// resourceVersion.
func ReadList[T any, P Object[T]](c *apiclient.Client, versions *Versions, listPath string) ([]T, error) {
	var items []T
	err := versions.SendList(func() (map[string]string, error) {
		list, err := apiclient.Get[struct {
			Items []T `json:"items"`
		}](c, listPath)
		if err != nil {
			return nil, err
		}
		items = list.Items
		listed := make(map[string]string, len(items))
		for index := range items {
			meta := P(&items[index]).GetObjectMeta()
			listed[Key(meta)] = meta.GetResourceVersion()
		}
		return listed, nil
	})
	return items, err
}

// Written sends one request that returns an object, such as a create,
// an apply, or an update, and records the version of the returned copy.
// After a 404 the memo records the object as gone, and after any other
// failure as unknown (Versions.Send).
func Written[T any, P Object[T]](versions *Versions, key string, request func() (*T, error)) (*T, error) {
	var written *T
	err := versions.Send(key, func() (string, error) {
		copied, err := request()
		if err != nil {
			return "", err
		}
		written = copied
		return P(copied).GetObjectMeta().GetResourceVersion(), nil
	})
	return written, err
}

// SettleStatus writes the status that apply sets on a copy of an
// object. apply computes the status from the copy it receives, sets it
// on that copy, and returns whether the copy needs the write.
//
// When the write fails with 409 or 404 (apiclient.Stale), SettleStatus
// reads the object again, calls apply on that copy, and writes once
// more. It returns true when a write succeeded, and
// apiclient.ErrNotFound when the object no longer exists. After an
// error, object has the status that apply set, which the API server did
// not accept.
func SettleStatus[T any, P Object[T]](c *apiclient.Client, versions *Versions, path string, object *T, apply func(*T) bool) (bool, error) {
	key := Key(P(object).GetObjectMeta())
	write := func() error {
		return versions.Send(key, func() (string, error) {
			if err := apiclient.ReplaceStatus(c, path, object); err != nil {
				return "", err
			}
			return P(object).GetObjectMeta().GetResourceVersion(), nil
		})
	}
	if !apply(object) {
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
	*object = *current
	if !apply(object) {
		return false, nil
	}
	if err := write(); err != nil {
		return false, err
	}
	return true, nil
}
