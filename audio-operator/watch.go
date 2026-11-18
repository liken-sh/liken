package main

// The operator and audio-api read Kubernetes objects through watches,
// not timers, so an idle watch costs one open connection and no reads.
// The kubernetes/informer package runs each watch on client-go's
// reflector, and each watch selects only the objects its reader needs:
// endpointwatch.go, api.go, and apipods.go start them.

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

var (
	sinkResource      = schema.GroupVersionResource{Group: EndpointGroup, Version: EndpointVersion, Resource: "sinks"}
	sourceResource    = schema.GroupVersionResource{Group: EndpointGroup, Version: EndpointVersion, Resource: "sources"}
	secretResource    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	configMapResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	podResource       = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
)

// unwrap returns the object that an informer event handler received.
// When the watch missed a deletion, the informer passes a
// cache.DeletedFinalStateUnknown, whose Obj is the last stored copy
// and can be nil.
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
