package machine

// The API server prunes every field that a structural schema does not
// declare. A field the operator writes into status and the schema
// lacks is dropped on each write, so the operator reads back a status
// without it, finds its own status different, and writes again on
// every pass. That write stores nothing new and changes no
// resourceVersion, so nothing reports it but the backstop. This test
// walks the Go types and requires a schema property for every field
// they can write.

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

// readMachineSchema answers the schema of one top-level property of the
// Machine, spec or status, as plain maps.
func readMachineSchema(t *testing.T, property string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("manifests/machines-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd map[string]any
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	version := crd["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)
	root := version["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
	return root["properties"].(map[string]any)[property].(map[string]any)
}

// undeclaredFields answers the JSON path of each field of t that the
// schema s does not declare.
func undeclaredFields(t reflect.Type, s map[string]any, path string) []string {
	for {
		switch t.Kind() {
		case reflect.Pointer:
			t = t.Elem()
			continue
		case reflect.Slice:
			s, _ = s["items"].(map[string]any)
			t = t.Elem()
			continue
		case reflect.Map:
			s, _ = s["additionalProperties"].(map[string]any)
			t = t.Elem()
			continue
		}
		break
	}
	if t.Kind() != reflect.Struct || t == reflect.TypeFor[time.Time]() || s == nil || s["x-kubernetes-preserve-unknown-fields"] == true {
		return nil
	}
	properties, _ := s["properties"].(map[string]any)
	var missing []string
	for field := range t.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch {
		case name == "-":
			continue
		case name == "" && field.Anonymous:
			missing = append(missing, undeclaredFields(field.Type, s, path)...)
			continue
		case name == "":
			name = field.Name
		}
		sub, ok := properties[name].(map[string]any)
		if !ok {
			missing = append(missing, path+"."+name)
			continue
		}
		missing = append(missing, undeclaredFields(field.Type, sub, path+"."+name)...)
	}
	return missing
}

// Every field that MachineStatus and MachineSpec can hold has a
// property in the CRD's schema, so the API server keeps what the
// operator writes.
func TestTheSchemaDeclaresEveryField(t *testing.T) {
	cases := []struct {
		property string
		of       reflect.Type
	}{
		{"status", reflect.TypeFor[MachineStatus]()},
		{"spec", reflect.TypeFor[MachineSpec]()},
	}
	for _, c := range cases {
		t.Run(c.property, func(t *testing.T) {
			if missing := undeclaredFields(c.of, readMachineSchema(t, c.property), c.property); len(missing) > 0 {
				t.Errorf("the schema does not declare %s, so the API server drops them", strings.Join(missing, ", "))
			}
		})
	}
}
