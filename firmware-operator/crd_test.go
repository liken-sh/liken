package main

// These tests run objects through the CRD's own schema, with the
// validator and the pruning the API server uses, so a schema edit that
// refuses a valid Firmware, accepts an invalid one, or prunes a field
// the operator writes fails here.

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

func loadCRD(t *testing.T) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile("deploy/firmware-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(raw, crd); err != nil {
		t.Fatalf("decoding deploy/firmware-crd.yaml: %v", err)
	}
	return crd
}

func TestCRDIdentity(t *testing.T) {
	crd := loadCRD(t)
	cases := []struct {
		name, got, want string
	}{
		{"scope", string(crd.Spec.Scope), string(apiextensionsv1.ClusterScoped)},
		{"group", crd.Spec.Group, "firmware.liken.sh"},
		{"kind", crd.Spec.Names.Kind, firmwareKind},
		{"plural", crd.Spec.Names.Plural, "firmwares"},
		{"version", crd.Spec.Versions[0].Name, "v1alpha1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

func TestCRDPrinterColumns(t *testing.T) {
	var names []string
	for _, column := range loadCRD(t).Spec.Versions[0].AdditionalPrinterColumns {
		names = append(names, column.Name)
	}
	want := []string{"Valid", "Loaded", "Running", "Failed", "Conflict", "Age"}
	if !slices.Equal(names, want) {
		t.Errorf("printer columns %v, want %v", names, want)
	}
}

// schemaValidator builds the validator the API server itself would run
// a Firmware through, from the CRD's own schema.
func schemaValidator(t *testing.T) validation.SchemaValidator {
	t.Helper()
	validator, _, err := validation.NewSchemaValidator(internalSchema(t))
	if err != nil {
		t.Fatalf("building the schema validator: %v", err)
	}
	return validator
}

// internalSchema is the CRD's schema as the API server's own type.
func internalSchema(t *testing.T) *apiextensions.JSONSchemaProps {
	t.Helper()
	scheme := runtime.NewScheme()
	install.Install(scheme)
	internal := &apiextensions.JSONSchemaProps{}
	if err := scheme.Convert(loadCRD(t).Spec.Versions[0].Schema.OpenAPIV3Schema, internal, nil); err != nil {
		t.Fatalf("converting the schema: %v", err)
	}
	return internal
}

// object is a Firmware with the spec given, as kubectl sends it.
func object(spec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": firmwareAPIVersion, "kind": firmwareKind,
		"metadata": map[string]any{"name": "qhy5l-ii"},
		"spec":     spec,
	}
}

func TestCRDValidatesExamples(t *testing.T) {
	validator := schemaValidator(t)
	upload := map[string]any{"image": "ghcr.io/liken-sh/indi-qhy:test", "command": []any{"fxload", "-D", "$(LIKEN_DEVICE)"}}
	cases := []struct {
		name    string
		spec    map[string]any
		wantErr bool
	}{
		{"a rule", map[string]any{"selector": "true", "upload": upload}, false},
		{"a rule on some machines", map[string]any{"selector": "true", "upload": upload,
			"machines": map[string]any{"matchExpressions": []any{map[string]any{"key": "observatory", "operator": "Exists"}}}}, false},
		{"no upload", map[string]any{"selector": "true"}, true},
		{"no selector", map[string]any{"upload": upload}, true},
		{"an upload with no command", map[string]any{"selector": "true", "upload": map[string]any{"image": "loader"}}, true},
		{"an upload with an empty command", map[string]any{"selector": "true", "upload": map[string]any{"image": "loader", "command": []any{}}}, true},
		{"an unknown operator", map[string]any{"selector": "true", "upload": upload,
			"machines": map[string]any{"matchExpressions": []any{map[string]any{"key": "a", "operator": "Near"}}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validation.ValidateCustomResource(field.NewPath(""), object(c.spec), validator)
			if got := len(errs) > 0; got != c.wantErr {
				t.Errorf("got errors %v, want an error: %v", errs, c.wantErr)
			}
		})
	}
}

// The status the operator composes is one the CRD's schema takes, and
// the API server prunes none of its fields.
func TestTheOperatorsStatusValidates(t *testing.T) {
	f := qhy5lii()
	f.APIVersion, f.Kind = firmwareAPIVersion, firmwareKind
	f.Metadata.Generation = 2
	a := appearance{machine: "liken-1", address: "1-4.1.2", deviceNumber: 12}
	f.Status, _, _ = composeStatus(&f, plan{
		valid:   validity(nil),
		results: []attempt{{appearance: a, device: "usb-1-4-1-2", generation: 2, result: resultLoaded, message: "loaded"}},
		conflicts: []attempt{{appearance: appearance{machine: "liken-1", address: "1-3", deviceNumber: 4}, device: "usb-1-3",
			generation: 2, result: resultConflict, message: "another rule"}},
	}, time.Date(2026, 10, 19, 22, 1, 9, 0, time.UTC))

	var written map[string]any
	if err := json.Unmarshal(mustJSON(f), &written); err != nil {
		t.Fatal(err)
	}
	if errs := validation.ValidateCustomResource(field.NewPath(""), written, schemaValidator(t)); len(errs) > 0 {
		t.Errorf("the schema refuses the operator's status: %v", errs)
	}
	structural, err := structuralschema.NewStructural(internalSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	pruned := pruning.PruneWithOptions(written, structural, true, structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})
	if len(pruned) > 0 {
		t.Errorf("the API server prunes %v from the operator's status", pruned)
	}
}
