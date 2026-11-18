package astrophotography

// The tests of this package read the CRDs in deploy/ through the code
// that the API server runs: the conversion to the internal schema, the
// check of the whole definition, the structural validator, and the CEL
// validator. A CRD or a resource that the cluster would refuse fails
// here first.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

func crdPath(kind Kind) string {
	return filepath.Join("..", "deploy", kind.Plural+"-crd.yaml")
}

func loadCRDFile(t *testing.T, path string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(raw, crd); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return crd
}

func loadCRD(t *testing.T, kind Kind) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	return loadCRDFile(t, crdPath(kind))
}

// schemaOf returns the schema of the only version that a CRD serves.
func schemaOf(t *testing.T, kind Kind) *apiextensionsv1.JSONSchemaProps {
	t.Helper()
	return loadCRD(t, kind).Spec.Versions[0].Schema.OpenAPIV3Schema
}

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	install.Install(scheme)
	return scheme
}

// internalSchema converts a CRD's schema to the internal type that the
// validators take, the same conversion the API server does.
func internalSchema(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) *apiextensions.JSONSchemaProps {
	t.Helper()
	internal := &apiextensions.JSONSchemaProps{}
	if err := newScheme().Convert(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, internal, nil); err != nil {
		t.Fatalf("converting the schema of %s: %v", crd.Name, err)
	}
	return internal
}

// deploy/ has one CRD for each kind and no others, so a CRD with no Go
// type fails here.
func TestDeployHasOneCRDForEachKind(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "deploy", "*-crd.yaml"))
	if err != nil {
		t.Fatalf("listing the CRD files: %v", err)
	}
	var want []string
	for _, kind := range Kinds {
		want = append(want, crdPath(kind))
	}
	slices.Sort(files)
	slices.Sort(want)
	if !slices.Equal(files, want) {
		t.Errorf("CRD files = %v, want %v", files, want)
	}
}

// Both kinds are in the category astro with observatory-operator's
// kinds, so `kubectl get astro` lists the resources of both groups. No
// kind has a short name.
func TestEachCRDDeclaresItsKind(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			crd := loadCRD(t, kind)
			names := crd.Spec.Names
			cases := []struct{ field, got, want string }{
				{"metadata.name", crd.Name, kind.Plural + "." + Group},
				{"group", crd.Spec.Group, Group},
				{"scope", string(crd.Spec.Scope), string(apiextensionsv1.NamespaceScoped)},
				{"kind", names.Kind, kind.Name},
				{"listKind", names.ListKind, kind.Name + "List"},
				{"plural", names.Plural, kind.Plural},
				{"singular", names.Singular, strings.ToLower(kind.Name)},
				{"categories", strings.Join(names.Categories, ","), "astro"},
				{"shortNames", strings.Join(names.ShortNames, ","), ""},
			}
			for _, c := range cases {
				if c.got != c.want {
					t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
				}
			}
		})
	}
}

// Each CRD serves and stores one version with a status subresource, so
// the operator's status writes never change a spec a person wrote.
func TestEachCRDServesOneVersionWithAStatusSubresource(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			versions := loadCRD(t, kind).Spec.Versions
			if len(versions) != 1 {
				t.Fatalf("%d versions, want 1", len(versions))
			}
			v := versions[0]
			if v.Name != Version || !v.Served || !v.Storage {
				t.Errorf("version %s served=%v storage=%v, want %s served and stored",
					v.Name, v.Served, v.Storage, Version)
			}
			if v.Subresources == nil || v.Subresources.Status == nil {
				t.Error("the CRD has no status subresource")
			}
		})
	}
}

// The API server checks the whole definition before it serves it, and
// that check compiles each CEL rule and estimates its cost. A rule whose
// cost is too high makes the cluster refuse the whole CRD.
func TestTheAPIServerWouldAcceptEachCRD(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			internal := &apiextensions.CustomResourceDefinition{}
			if err := newScheme().Convert(loadCRD(t, kind), internal, nil); err != nil {
				t.Fatalf("converting the %s CRD to the internal type: %v", kind.Name, err)
			}
			// The API server fills the stored versions in when it creates
			// the definition, and a manifest sets none.
			internal.Status.StoredVersions = []string{Version}
			if errs := crdvalidation.ValidateCustomResourceDefinition(t.Context(), internal); len(errs) > 0 {
				t.Errorf("the API server would refuse the definition: %v", errs)
			}
		})
	}
}

// `kubectl explain` prints each field's description, and the
// descriptions give the units, so every field needs one.
func TestEveryFieldHasADescription(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			for _, path := range undescribed(schemaOf(t, kind), "") {
				t.Errorf("%s has no description", path)
			}
		})
	}
}

func undescribed(schema *apiextensionsv1.JSONSchemaProps, path string) []string {
	var missing []string
	if path != "" && strings.TrimSpace(schema.Description) == "" {
		missing = append(missing, path)
	}
	for name, property := range schema.Properties {
		missing = append(missing, undescribed(&property, path+"."+name)...)
	}
	if schema.Items != nil && schema.Items.Schema != nil && schema.Items.Schema.Type == "object" {
		missing = append(missing, undescribed(schema.Items.Schema, path+"[]")...)
	}
	return missing
}

// A printer column whose path names no field prints an empty column
// with no error, so each path must resolve in the schema. A column of
// type string prints any value, so only the other types must match.
func TestEachPrinterColumnPointsToAField(t *testing.T) {
	filter := regexp.MustCompile(`\[[^\]]*\]`)
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			crd := loadCRD(t, kind)
			schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
			columns := crd.Spec.Versions[0].AdditionalPrinterColumns
			if !slices.ContainsFunc(columns, func(c apiextensionsv1.CustomResourceColumnDefinition) bool {
				return c.Name == "Age" && c.Priority == 0
			}) {
				t.Error("the default view has no Age column")
			}
			for _, column := range columns {
				if column.JSONPath == ".metadata.creationTimestamp" {
					continue
				}
				path := filter.ReplaceAllString(column.JSONPath, "[]")
				if field := resolve(schema, strings.Split(strings.TrimPrefix(path, "."), ".")); field == nil {
					t.Errorf("column %s: %s names no field", column.Name, column.JSONPath)
				} else if column.Type != "string" && field.Type != column.Type {
					t.Errorf("column %s is %s, and %s is %s", column.Name, column.Type, column.JSONPath, field.Type)
				}
			}
		})
	}
}

// The API server prints only the first value that a column's path
// matches, so a column over a list, such as .status.indexes[*].id,
// shows one item and hides the rest.
func TestNoPrinterColumnSpansAList(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			for _, column := range loadCRD(t, kind).Spec.Versions[0].AdditionalPrinterColumns {
				if strings.Contains(column.JSONPath, "[*]") {
					t.Errorf("column %s: %s prints only the first item", column.Name, column.JSONPath)
				}
			}
		})
	}
}

// resolve walks a column's path through the schema. A step that ends
// in [] steps into the array's items.
func resolve(schema *apiextensionsv1.JSONSchemaProps, steps []string) *apiextensionsv1.JSONSchemaProps {
	for _, step := range steps {
		name, array := strings.CutSuffix(step, "[]")
		property, ok := schema.Properties[name]
		if !ok {
			return nil
		}
		schema = &property
		if array {
			if schema.Items == nil || schema.Items.Schema == nil {
				return nil
			}
			schema = schema.Items.Schema
		}
	}
	return schema
}

// `kubectl apply` sends a JSON merge patch to a custom resource, and a
// merge patch replaces a whole list when it differs from the live one.
// A default inside a list's items makes the live list differ from the
// file's list that omits the field, so every apply of an unchanged
// file reports the resource as configured. So the schema has no default
// inside a list.
func TestNoCRDHasADefaultInsideAList(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			for _, path := range listDefaults(schemaOf(t, kind), "", false) {
				t.Errorf("%s has a default inside a list", path)
			}
		})
	}
}

// listDefaults returns the path of each default under an array's
// items. A default outside every list is allowed.
func listDefaults(schema *apiextensionsv1.JSONSchemaProps, path string, inList bool) []string {
	var found []string
	if inList && schema.Default != nil {
		found = append(found, path)
	}
	for name, property := range schema.Properties {
		found = append(found, listDefaults(&property, path+"."+name, inList)...)
	}
	if schema.Items != nil && schema.Items.Schema != nil {
		found = append(found, listDefaults(schema.Items.Schema, path+"[]", true)...)
	}
	return found
}
