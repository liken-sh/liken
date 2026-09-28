package main

// A watch keeps this program's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs one open
// connection and no reads.
//
// The shared informer package runs each watch on client-go's reflector,
// and keeps the copy of the collection that a pass reads
// (objectcache.go). The shared apiclient package sends every read and
// write that a watch's copy does not answer.
//
// Five collections are watched, each as narrowly as its reader needs:
//
//   - The operator watches its machine's Sinks and Sources with the
//     field selector status.node (endpointwatch.go).
//   - audio-api watches the Secret audio-capture-server and the
//     ConfigMap extension-apiserver-authentication, each with the
//     field selector metadata.name (api.go). RBAC authorizes a list
//     or a watch against resourceNames only when the request selects
//     that one name, so the selector is what lets a Role grant list
//     and watch on one object.
//   - audio-api watches the operator's pods with the label selector
//     app=audio-operator (apipods.go).

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// The collections this program watches.
var (
	sinkResource      = schema.GroupVersionResource{Group: EndpointGroup, Version: EndpointVersion, Resource: "sinks"}
	sourceResource    = schema.GroupVersionResource{Group: EndpointGroup, Version: EndpointVersion, Resource: "sources"}
	secretResource    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	configMapResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	podResource       = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
)

// unwrap answers the object a handler received, for a handler that
// reads the object's metadata and not its fields. The informer hands a
// handler an *unstructured.Unstructured, or, for an object that was
// deleted while the watch was down, a tombstone that holds the last
// copy the informer knew. A tombstone can hold no copy at all.
func unwrap(object any) (*unstructured.Unstructured, error) {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		object = tombstone.Obj
	}
	item, ok := object.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("the watch delivered a %T, not an object", object)
	}
	return item, nil
}
