package observatory

import (
	"reflect"
	"testing"
)

// The operator decodes a spec's settings into the type that Settings
// answers, so that type must be the one the kind's spec embeds, or a
// field the spec states would never reach the device. Every device kind
// is one interface, so each has a standard set.
func TestEachDeviceKindsSettingsAreTheOnesItsSpecEmbeds(t *testing.T) {
	for _, kind := range DeviceKinds {
		t.Run(kind.Name, func(t *testing.T) {
			standard, ok := Standards[kind]
			if !ok {
				t.Fatalf("%s has no standard set", kind.Name)
			}
			spec, _ := reflect.TypeOf(kindTypes[kind.Name]).FieldByName("Spec")
			embedded := ""
			for field := range spec.Type.Fields() {
				if field.Anonymous && field.Type.Name() == kind.Name+"Settings" {
					embedded = field.Type.Name()
				}
			}
			answered := ""
			if standard.Settings != nil {
				answered = reflect.TypeOf(standard.Settings()).Elem().Name()
			}
			if answered != embedded {
				t.Errorf("Settings answers %q, and the spec embeds %q", answered, embedded)
			}
		})
	}
}
