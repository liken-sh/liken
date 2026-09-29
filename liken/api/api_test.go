package api

import "testing"

// The metadata answers the parts of a store key and the version of the
// copy. Every liken kind is cluster-scoped, so the namespace is empty
// and the key is the name alone.
func TestObjectMetaAnswersTheKeyAndTheVersion(t *testing.T) {
	var meta Meta = ObjectMeta{Name: "node-1", ResourceVersion: "42"}
	if meta.GetName() != "node-1" || meta.GetNamespace() != "" || meta.GetResourceVersion() != "42" {
		t.Errorf("meta = %q %q %q, want node-1, no namespace, and 42",
			meta.GetName(), meta.GetNamespace(), meta.GetResourceVersion())
	}
}
