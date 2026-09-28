package informer

import (
	"encoding/json"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// Convert decodes one object from a watch into the operator's own
// struct. The informer hands a handler an *unstructured.Unstructured,
// or, for an object that was deleted while the watch was down, a
// tombstone that holds the last copy the informer had. A tombstone can
// hold no copy at all, and then the error names the tombstone's key.
//
// An object that does not convert has a field whose type differs from
// the operator's struct, so the CRD schema and the struct disagree.
// The error names the object, and the caller reports it, because an
// object that is dropped with no word leaves nobody a way to find out
// why the operator ignored an edit.
func Convert[T any](object any) (T, error) {
	var out T
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		if tombstone.Obj == nil {
			return out, fmt.Errorf("the tombstone for %s holds no copy of the object", tombstone.Key)
		}
		object = tombstone.Obj
	}
	item, ok := object.(*unstructured.Unstructured)
	if !ok {
		return out, fmt.Errorf("the watch delivered a %T, not an object", object)
	}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, &out)
	if err == nil {
		return out, nil
	}
	// The converter refuses a number for a field whose type is a string
	// with its own UnmarshalJSON, and does not call that method. A
	// Service's targetPort is a number or a name, and an operator holds
	// it as text that its UnmarshalJSON reads from either, so the
	// converter refuses a Service that a read from the API server
	// decodes. An object the converter refuses is decoded again from its
	// JSON, the same way a read from the API server decodes it.
	if body, marshalErr := json.Marshal(item.Object); marshalErr == nil {
		var decoded T
		if json.Unmarshal(body, &decoded) == nil {
			return decoded, nil
		}
	}
	return out, fmt.Errorf("%s %s does not convert: %w", item.GetKind(), objectName(item), err)
}

// objectName is namespace/name for a namespaced object and name for a
// cluster-scoped one.
func objectName(item *unstructured.Unstructured) string {
	if item.GetNamespace() == "" {
		return item.GetName()
	}
	return item.GetNamespace() + "/" + item.GetName()
}

// Report logs an object that Convert refused. what names the watch or
// the read, such as "the PairingRequests".
func Report(what string, err error) {
	fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
}
