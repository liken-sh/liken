package main

// This file reads one collection from its watch's store while
// informer.View.Ready is true, and from the API server otherwise. The
// store stops being ready when the API server refuses the watch with a
// 401 or a 403, because the store then misses every change after its
// last list. That happens when a new binary runs under the previous
// release's RBAC, which grants list but not watch. Any other watch
// failure, such as a refused connection while the API server restarts,
// leaves the store ready.
//
// The reads from the API server pass a nil memo, so they record no
// resourceVersion. These kinds need none, because a pass does not read
// back a status it wrote to them (objectcache.go), and it reads the API
// server again before it creates or deletes an object (clusterview.go).

import (
	"errors"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// watchedSource reads one collection of the view.
type watchedSource struct {
	view     informer.View
	client   *apiclient.Client
	resource schema.GroupVersionResource
	// labels is the label selector of the watch. The list from the API
	// server uses it too, so both reads return the same objects.
	labels string
}

// List returns every object of the collection.
func (s watchedSource) List() ([]any, error) {
	if s.view.Ready() {
		return s.view.Store.List(), nil
	}
	path := collectionPath(s.resource)
	if s.labels != "" {
		path += "?labelSelector=" + url.QueryEscape(s.labels)
	}
	list, err := memo.ReadList[objectFields](s.client, nil, path)
	// A 404 means the cluster does not serve the resource, which the
	// watch of an optional resource also reads as an empty collection
	// (watch.go).
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(list))
	for _, fields := range list {
		items = append(items, &unstructured.Unstructured{Object: fields})
	}
	return items, nil
}

// GetByKey returns the object with this key, namespace/name or name.
func (s watchedSource) GetByKey(key string) (any, bool, error) {
	if s.view.Ready() {
		return s.view.Store.GetByKey(key)
	}
	fields, err := memo.ReadFresh[objectFields](s.client, nil, key, objectPath(s.resource, key))
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &unstructured.Unstructured{Object: *fields}, true, nil
}

// objectFields is one object as decoded from the API server's JSON.
type objectFields map[string]any

func (f *objectFields) GetObjectMeta() memo.Meta { return &unstructured.Unstructured{Object: *f} }

// Stored returns the object from the watch's store, and never reads
// the API server, even while the store is not ready.
func (s watchedSource) Stored(key string) (any, bool) {
	if s.view.Store == nil {
		return nil, false
	}
	item, held, err := s.view.Store.GetByKey(key)
	return item, held && err == nil
}

// collectionPath is the API path that lists the collection in every
// namespace.
func collectionPath(resource schema.GroupVersionResource) string {
	return groupPath(resource) + "/" + resource.Resource
}

// objectPath is the API path of the object with this key:
// namespace/name, or the name alone for a cluster-scoped object.
func objectPath(resource schema.GroupVersionResource, key string) string {
	namespace, name, namespaced := strings.Cut(key, "/")
	if !namespaced {
		return collectionPath(resource) + "/" + key
	}
	return groupPath(resource) + "/namespaces/" + namespace + "/" + resource.Resource + "/" + name
}

// groupPath is the API path prefix of a resource's group and version.
func groupPath(resource schema.GroupVersionResource) string {
	if resource.Group == "" {
		return "/api/" + resource.Version
	}
	return "/apis/" + resource.Group + "/" + resource.Version
}
