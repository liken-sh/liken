package observatory

// examples/simulators.yaml is the stack that a reader copies and that
// plan 07's tests apply as it is. These tests hold it to the CRDs, to
// the Go types, and to itself: every resource it names exists in it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const example = "simulators.yaml"

// documents answers the objects in one file of examples/, in order.
func documents(t *testing.T, name string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	var objects []map[string]any
	for _, document := range strings.Split(string(raw), "\n---\n") {
		object := map[string]any{}
		if err := yaml.UnmarshalStrict([]byte(document), &object); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, object)
	}
	return objects
}

func kindNamed(name string) Kind {
	i := slices.IndexFunc(Kinds, func(k Kind) bool { return k.Name == name })
	return Kinds[i]
}

func objectName(object map[string]any) string {
	return object["metadata"].(map[string]any)["name"].(string)
}

func TestTheExampleHasEveryKind(t *testing.T) {
	var kinds []string
	for _, object := range documents(t, example) {
		kinds = append(kinds, object["kind"].(string))
	}
	for _, kind := range Kinds {
		if !slices.Contains(kinds, kind.Name) {
			t.Errorf("the example has no %s", kind.Name)
		}
	}
}

func TestTheAPIServerAcceptsTheExample(t *testing.T) {
	for _, object := range documents(t, example) {
		kind := kindNamed(object["kind"].(string))
		t.Run(kind.Name+"/"+objectName(object), func(t *testing.T) {
			if errs := validate(t, kind, object, nil); len(errs) > 0 {
				t.Error(errs.ToAggregate())
			}
		})
	}
}

// The operator decodes each object into its Go type. A field that the
// type lacks would be dropped with no error, so the decode here
// refuses unknown fields.
func TestTheGoTypesReadTheExample(t *testing.T) {
	for _, object := range documents(t, example) {
		kind := object["kind"].(string)
		t.Run(kind+"/"+objectName(object), func(t *testing.T) {
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			typed := reflect.New(reflect.TypeOf(kindTypes[kind])).Interface()
			if err := decoder.Decode(typed); err != nil {
				t.Error(err)
			}
		})
	}
}

// references are the spec fields that name another resource, and the
// kind each one names.
var references = map[string]string{
	"observatory":  "Observatory",
	"telescope":    "Telescope",
	"opticalTube":  "OpticalTube",
	"opticalTrain": "OpticalTrain",
}

func TestEveryNameInTheExampleExists(t *testing.T) {
	objects := documents(t, example)
	exists := map[string]bool{}
	for _, object := range objects {
		exists[object["kind"].(string)+"/"+objectName(object)] = true
	}
	for _, object := range objects {
		spec := object["spec"].(map[string]any)
		named := map[string]string{}
		for field, kind := range references {
			if name, held := spec[field].(string); held {
				named[field] = kind + "/" + name
			}
		}
		if power, held := spec["power"].(map[string]any); held {
			named["power.switch"] = "Switch/" + power["switch"].(string)
		}
		for field, target := range named {
			if !exists[target] {
				t.Errorf("%s %s: spec.%s names %s, which the example lacks",
					object["kind"], objectName(object), field, target)
			}
		}
	}
}
