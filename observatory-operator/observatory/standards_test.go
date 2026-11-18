package observatory

import (
	"reflect"
	"testing"
)

// The operator decodes a spec's settings into the type that Settings
// returns, so that type must be the one the kind's spec embeds, or a
// field set in the spec would never reach the device. Every device kind
// is one INDI interface, so Standards has an entry for each kind.
func TestEachDeviceKindsSettingsAreTheOnesItsSpecEmbeds(t *testing.T) {
	for _, kind := range DeviceKinds {
		t.Run(kind.Name, func(t *testing.T) {
			standard, ok := Standards[kind]
			if !ok {
				t.Fatalf("Standards has no entry for %s", kind.Name)
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
				t.Errorf("Settings returns a %q, want the %q that the spec embeds", answered, embedded)
			}
		})
	}
}

// The status writer fills the type that Status returns, and the API
// server keeps only the fields of the kind's status, so that type must
// be the one the kind's status embeds.
func TestEachDeviceKindsStandardIsTheOneItsStatusEmbeds(t *testing.T) {
	for _, kind := range DeviceKinds {
		t.Run(kind.Name, func(t *testing.T) {
			status, _ := reflect.TypeOf(kindTypes[kind.Name]).FieldByName("Status")
			answered := reflect.TypeOf(Standards[kind].Status()).Elem()
			field, embedded := status.Type.FieldByName(answered.Name())
			if !embedded || !field.Anonymous {
				t.Errorf("Status returns a %s, which the status of a %s does not embed", answered.Name(), kind.Name)
			}
		})
	}
}
