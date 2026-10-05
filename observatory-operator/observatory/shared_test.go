package observatory

// The 14 device CRDs repeat the fields that every device shares,
// because a CRD cannot refer to another. This test holds each copy
// equal to the Mount's, descriptions and rules included, so an edit to
// one copy fails until every copy has it.

import (
	"reflect"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// sharedDeviceFields are the fields of DeviceSpec and DeviceStatus, by
// their place in the schema.
var sharedDeviceFields = map[string][]string{
	"spec":   sortedKeys(jsonFields(reflect.TypeFor[DeviceSpec]())),
	"status": sortedKeys(jsonFields(reflect.TypeFor[DeviceStatus]())),
}

func TestTheDeviceCRDsShareTheirCommonFields(t *testing.T) {
	model := schemaOf(t, MountKind)
	for _, kind := range DeviceKinds[1:] {
		t.Run(kind.Name, func(t *testing.T) {
			schema := schemaOf(t, kind)
			for half, names := range sharedDeviceFields {
				for _, name := range names {
					if !reflect.DeepEqual(sharedField(schema, half, name), sharedField(model, half, name)) {
						t.Errorf("%s.%s differs from the Mount's", half, name)
					}
				}
			}
		})
	}
}

func sharedField(schema *apiextensionsv1.JSONSchemaProps, half, name string) apiextensionsv1.JSONSchemaProps {
	return schema.Properties[half].Properties[name]
}
