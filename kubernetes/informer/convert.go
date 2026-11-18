package informer

import (
	"encoding/json"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// Convert decodes an object that an informer gave a handler into T. The
// object is an *unstructured.Unstructured, or a
// cache.DeletedFinalStateUnknown tombstone for an object that was
// deleted while the watch was disconnected. A tombstone holds the last
// copy the informer cached, and Convert decodes that copy. When the
// tombstone holds none, the error names the tombstone's key.
//
// A conversion fails when a field has a different type from the one in
// T, which means the CRD schema and T disagree. The error names the
// object. The caller should report it with Report, because without a
// log line nobody can find out why the operator ignored an edit.
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
	// The converter calls UnmarshalJSON only for a map, slice,
	// pointer, struct, or interface type. It converts a value of
	// any other kind directly, so it refuses a number for a string
	// type even when that type's UnmarshalJSON accepts one. A
	// Service's targetPort is a number or a name, and an operator
	// can read it into such a type. So decode the object from its
	// JSON again, the way apiclient decodes a response from the API
	// server.
	if body, marshalErr := json.Marshal(item.Object); marshalErr == nil {
		var decoded T
		if json.Unmarshal(body, &decoded) == nil {
			return decoded, nil
		}
	}
	return out, fmt.Errorf("%s %s does not convert: %w", item.GetKind(), objectName(item), err)
}

// objectName returns namespace/name, or only the name for a
// cluster-scoped object.
func objectName(item *unstructured.Unstructured) string {
	if item.GetNamespace() == "" {
		return item.GetName()
	}
	return item.GetNamespace() + "/" + item.GetName()
}

// Report writes the error from a failed Convert to standard error. what
// describes the watch or read, such as "the PairingRequests".
func Report(what string, err error) {
	fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
}
