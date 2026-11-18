package observatory

// examples/simulators.yaml is a complete set of simulated equipment
// that a reader applies unchanged. These tests check it against the
// CRDs and the Go types, and check that every resource it refers to is
// in the file.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apimachinery/pkg/util/jsonmergepatch"
	"sigs.k8s.io/yaml"
)

const example = "simulators.yaml"

// documents returns the objects in one file of examples/, in order.
func documents(t *testing.T, name string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "examples", name))
	if err != nil {
		t.Fatalf("reading the example file: %v", err)
	}
	var objects []map[string]any
	for _, document := range strings.Split(string(raw), "\n---\n") {
		object := map[string]any{}
		if err := yaml.UnmarshalStrict([]byte(document), &object); err != nil {
			t.Fatalf("parsing a document of the example: %v", err)
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
			t.Errorf("the example has no %s, want one of every kind", kind.Name)
		}
	}
}

func TestTheAPIServerAcceptsTheExample(t *testing.T) {
	for _, object := range documents(t, example) {
		kind := kindNamed(object["kind"].(string))
		t.Run(kind.Name+"/"+objectName(object), func(t *testing.T) {
			if errs := validate(t, kind, object, nil); len(errs) > 0 {
				t.Errorf("the CRD schema rejects the example: %v", errs.ToAggregate())
			}
		})
	}
}

// The operator's decoder drops a field that the Go type lacks without
// an error, so this test decodes with DisallowUnknownFields.
func TestTheGoTypesReadTheExample(t *testing.T) {
	for _, object := range documents(t, example) {
		kind := object["kind"].(string)
		t.Run(kind+"/"+objectName(object), func(t *testing.T) {
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatalf("encoding %s as JSON: %v", kind, err)
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			typed := reflect.New(reflect.TypeOf(kindTypes[kind])).Interface()
			if err := decoder.Decode(typed); err != nil {
				t.Errorf("decoding into the Go type: %v", err)
			}
		})
	}
}

// references maps each spec field that holds another resource's name
// to the kind of that resource.
var references = map[string]string{
	"observatory":  "Observatory",
	"telescope":    "Telescope",
	"opticalTube":  "OpticalTube",
	"opticalTrain": "OpticalTrain",
	"horizon":      "Horizon",
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
		if device, held := spec["device"].(map[string]any); held {
			named["device"] = device["kind"].(string) + "/" + device["name"].(string)
		}
		for field, target := range named {
			if !exists[target] {
				t.Errorf("%s %s: spec.%s names %s, which the example lacks",
					object["kind"], objectName(object), field, target)
			}
		}
	}
}

// A second `kubectl apply` of the unchanged file must change nothing.
// For a custom resource, kubectl computes a three-way JSON merge patch
// from the last applied file, the file, and the live object, which has
// the CRD's defaults. If the patch is not {}, kubectl reports the
// resource as configured on every apply.
func TestApplyingTheExampleAgainChangesNothing(t *testing.T) {
	for _, object := range documents(t, example) {
		kind := kindNamed(object["kind"].(string))
		t.Run(kind.Name+"/"+objectName(object), func(t *testing.T) {
			file, err := json.Marshal(object)
			if err != nil {
				t.Fatalf("encoding the example file: %v", err)
			}
			patch, err := jsonmergepatch.CreateThreeWayJSONMergePatch(file, file, live(t, kind, file))
			if err != nil {
				t.Fatalf("computing the merge patch: %v", err)
			}
			if string(patch) != "{}" {
				t.Errorf("a second apply patches %s", patch)
			}
		})
	}
}

// live returns the object that the API server would store for a file:
// the file with the CRD's defaults applied.
func live(t *testing.T, kind Kind, file []byte) []byte {
	t.Helper()
	structural, err := structuralschema.NewStructural(internalSchema(t, kind))
	if err != nil {
		t.Fatalf("building the structural schema of the CRD: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(file, &object); err != nil {
		t.Fatalf("decoding the example file: %v", err)
	}
	defaulting.Default(object, structural)
	stored, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encoding the stored object: %v", err)
	}
	return stored
}
