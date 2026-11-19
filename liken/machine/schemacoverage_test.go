package machine

// The API server drops every field that the CRD's structural schema
// does not declare. When the schema lacks a status field that the
// operator writes, the operator reads back a status that differs from
// what it wrote, and it writes again on every pass. The write changes
// nothing, so no watch event shows the loop. This test walks the Go
// types and requires a schema property for each of their fields.

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

// readMachineSchema returns the CRD schema of the spec or the status
// property of a Machine, as plain maps.
func readMachineSchema(t *testing.T, property string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("manifests/machines-crd.yaml")
	if err != nil {
		t.Fatalf("reading the Machine CRD: %v", err)
	}
	var crd map[string]any
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parsing the Machine CRD: %v", err)
	}
	version := crd["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)
	root := version["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
	return root["properties"].(map[string]any)[property].(map[string]any)
}

// undeclaredFields returns the JSON path of each field of t that the
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
				t.Errorf("the schema does not declare %s, so the API server drops those fields", strings.Join(missing, ", "))
			}
		})
	}
}
