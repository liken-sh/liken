package main

// A watch's store answers the view only while it is ready
// (informer.View.Ready): it holds the whole first read, and the API
// server has not forbidden the watch since. A store whose watch the API
// server forbids with a 401 or a 403 holds no change made since its
// last list. The case is a release skew: a new binary under the
// previous release's RBAC, which grants list and not watch. While the
// store is not ready, each read of the collection goes to the API
// server. A watch that fails for another reason, such as a refused
// connection while the API server restarts, leaves the store ready, so
// the pass keeps its local work going from the store.

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// watchedSource is one collection of the view: the watch's store, and
// the API server's copy of the same selection while the store is not
// ready.
type watchedSource struct {
	view     informer.View
	client   *apiclient.Client
	resource schema.GroupVersionResource
	// labels is the watch's label selector, which a list from the API
	// server states too, so both answer the same selection.
	labels string
}

// List answers every object of the collection.
func (s watchedSource) List() ([]any, error) {
	if s.view.Ready() {
		return s.view.Store.List(), nil
	}
	path := collectionPath(s.resource)
	if s.labels != "" {
		path += "?labelSelector=" + url.QueryEscape(s.labels)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	err := s.client.RequestJSON(http.MethodGet, path, nil, &list)
	// A collection the API server does not serve holds no object, the
	// way the watch of an optional collection reads it (watch.go).
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(list.Items))
	for _, fields := range list.Items {
		items = append(items, &unstructured.Unstructured{Object: fields})
	}
	return items, nil
}

// GetByKey answers one object by its key, namespace/name or name.
func (s watchedSource) GetByKey(key string) (any, bool, error) {
	if s.view.Ready() {
		return s.view.Store.GetByKey(key)
	}
	var fields map[string]any
	err := s.client.RequestJSON(http.MethodGet, objectPath(s.resource, key), nil, &fields)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &unstructured.Unstructured{Object: fields}, true, nil
}

// Stored answers one object from the watch's store alone.
func (s watchedSource) Stored(key string) (any, bool) {
	if s.view.Store == nil {
		return nil, false
	}
	item, held, err := s.view.Store.GetByKey(key)
	return item, held && err == nil
}

// collectionPath is the path that lists a collection in every
// namespace, or a cluster-scoped collection.
func collectionPath(resource schema.GroupVersionResource) string {
	return groupPath(resource) + "/" + resource.Resource
}

// objectPath is the path of one object by its key, namespace/name for a
// namespaced object and the name for a cluster-scoped one.
func objectPath(resource schema.GroupVersionResource, key string) string {
	namespace, name, namespaced := strings.Cut(key, "/")
	if !namespaced {
		return collectionPath(resource) + "/" + key
	}
	return groupPath(resource) + "/namespaces/" + namespace + "/" + resource.Resource + "/" + name
}

// groupPath is the path of a resource's group and version: /api/v1 for
// the core group.
func groupPath(resource schema.GroupVersionResource) string {
	if resource.Group == "" {
		return "/api/" + resource.Version
	}
	return "/apis/" + resource.Group + "/" + resource.Version
}
