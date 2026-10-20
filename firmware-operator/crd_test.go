package main

// These tests run objects through the CRDs' own schemas, with the
// validators and the pruning the API server uses, so a schema edit that
// refuses a valid object, accepts an invalid one, or prunes a field the
// operator writes fails here. The structural schema and the
// x-kubernetes-validations rules are two passes in the API server, and
// a case runs both.

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
	celschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

// The definitions this module ships.
const (
	firmwareCRD = "deploy/firmware-crd.yaml"
	catalogCRD  = "deploy/firmwarecatalog-crd.yaml"
)

func loadCRD(t *testing.T, path string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(raw, crd); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return crd
}

func TestCRDIdentity(t *testing.T) {
	crd := loadCRD(t, firmwareCRD)
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
	for _, column := range loadCRD(t, firmwareCRD).Spec.Versions[0].AdditionalPrinterColumns {
		names = append(names, column.Name)
	}
	want := []string{"Valid", "Loaded", "Running", "Failed", "Conflict", "Machines", "Behind", "Version", "Approved", "Pending", "Age"}
	if !slices.Equal(names, want) {
		t.Errorf("printer columns %v, want %v", names, want)
	}
}

// validateObject runs an object through both passes the API server
// runs: the structural schema and the CEL rules.
func validateObject(t *testing.T, path string, object map[string]any) field.ErrorList {
	t.Helper()
	validator, _, err := validation.NewSchemaValidator(internalSchema(t, path))
	if err != nil {
		t.Fatalf("building the schema validator: %v", err)
	}
	errs := validation.ValidateCustomResource(field.NewPath(""), object, validator)
	structural := structuralSchema(t, path)
	if rules := celschema.NewValidator(structural, true, celconfig.PerCallLimit); rules != nil {
		celErrs, _ := rules.Validate(t.Context(), field.NewPath(""), structural, object, nil, celconfig.RuntimeCELCostBudget)
		errs = append(errs, celErrs...)
	}
	return errs
}

// internalSchema is a CRD's schema as the API server's own type.
func internalSchema(t *testing.T, path string) *apiextensions.JSONSchemaProps {
	t.Helper()
	scheme := runtime.NewScheme()
	install.Install(scheme)
	internal := &apiextensions.JSONSchemaProps{}
	if err := scheme.Convert(loadCRD(t, path).Spec.Versions[0].Schema.OpenAPIV3Schema, internal, nil); err != nil {
		t.Fatalf("converting the schema: %v", err)
	}
	return internal
}

func structuralSchema(t *testing.T, path string) *structuralschema.Structural {
	t.Helper()
	structural, err := structuralschema.NewStructural(internalSchema(t, path))
	if err != nil {
		t.Fatal(err)
	}
	return structural
}

// prunedFrom answers the fields the API server would drop from an
// object, because a CRD's schema does not name them.
func prunedFrom(t *testing.T, path string, object map[string]any) []string {
	t.Helper()
	return pruning.PruneWithOptions(object, structuralSchema(t, path), true, structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})
}

// object is a Firmware with the spec given, as kubectl sends it.
func object(spec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": firmwareAPIVersion, "kind": firmwareKind,
		"metadata": map[string]any{"name": "qhy5l-ii"},
		"spec":     spec,
	}
}

// The component and the cabinet of a dbx release that a person
// approves.
const (
	dbxComponent = "com.microsoft.dbx.x64-kek2023.firmware"
	dbxCabinet   = "1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5"
)

func TestCRDValidatesExamples(t *testing.T) {
	upload := map[string]any{"image": "ghcr.io/liken-sh/indi-qhy:test", "command": []any{"fxload", "-D", "$(LIKEN_DEVICE)"}}
	update := map[string]any{"guids": []any{biosGUID}}
	cabinet := map[string]any{"url": "https://fwupd.org/downloads/dbx.cab", "sha256": dbxCabinet}
	approval := map[string]any{"component": dbxComponent, "version": "20260707", "cabinet": cabinet}
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
		{"an update", map[string]any{"update": update}, false},
		{"both", map[string]any{"selector": "true", "upload": upload, "update": update}, true},
		{"neither", map[string]any{}, true},
		{"an update with a selector", map[string]any{"selector": "true", "update": update}, true},
		{"an update with machines", map[string]any{"update": update, "machines": map[string]any{"matchLabels": map[string]any{"a": "b"}}}, true},
		{"an update with no GUIDs", map[string]any{"update": map[string]any{"guids": []any{}}}, true},
		{"an update of a component", map[string]any{"update": map[string]any{"component": "com.microsoft.dbx.x64.firmware"}}, false},
		{"an update of a component and GUIDs", map[string]any{"update": map[string]any{"component": "com.microsoft.dbx.x64.firmware", "guids": []any{biosGUID}}}, true},
		{"an update of nothing", map[string]any{"update": map[string]any{}}, true},
		{"an approval", map[string]any{"update": approval}, false},
		{"an approval on some machines", map[string]any{"update": approval, "machines": map[string]any{"matchLabels": map[string]any{"canary": "true"}}}, false},
		{"a version with no cabinet", map[string]any{"update": map[string]any{"component": dbxComponent, "version": "20260707"}}, true},
		{"a cabinet with no version", map[string]any{"update": map[string]any{"component": dbxComponent, "cabinet": cabinet}}, true},
		{"an approval of GUIDs", map[string]any{"update": map[string]any{"guids": []any{biosGUID}, "version": "20260707", "cabinet": cabinet}}, true},
		{"a cabinet with a short SHA-256", map[string]any{"update": map[string]any{"component": dbxComponent, "version": "20260707",
			"cabinet": map[string]any{"url": "https://fwupd.org/a.cab", "sha256": "1bf2dd35"}}}, true},
		{"a cabinet with no URL", map[string]any{"update": map[string]any{"component": dbxComponent, "version": "20260707",
			"cabinet": map[string]any{"sha256": dbxCabinet}}}, true},
		{"an upload with no command", map[string]any{"selector": "true", "upload": map[string]any{"image": "loader"}}, true},
		{"an upload with an empty command", map[string]any{"selector": "true", "upload": map[string]any{"image": "loader", "command": []any{}}}, true},
		{"an unknown operator", map[string]any{"selector": "true", "upload": upload,
			"machines": map[string]any{"matchExpressions": []any{map[string]any{"key": "a", "operator": "Near"}}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validateObject(t, firmwareCRD, object(c.spec))
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

	assertTheSchemaKeeps(t, firmwareCRD, f)
}

// assertTheSchemaKeeps checks that a CRD's schema takes an object the
// operator writes, and prunes none of its fields.
func assertTheSchemaKeeps(t *testing.T, path string, object any) {
	t.Helper()
	var written map[string]any
	if err := json.Unmarshal(mustJSON(object), &written); err != nil {
		t.Fatal(err)
	}
	if errs := validateObject(t, path, written); len(errs) > 0 {
		t.Errorf("the schema refuses the operator's object: %v", errs)
	}
	if pruned := prunedFrom(t, path, written); len(pruned) > 0 {
		t.Errorf("the API server prunes %v from the operator's object", pruned)
	}
}
