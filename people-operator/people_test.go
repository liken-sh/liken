package main

import (
	"os"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

func loadCRD(t *testing.T) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile("deploy/people-crd.yaml")
	if err != nil {
		t.Fatalf("reading deploy/people-crd.yaml: %v", err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(raw, crd); err != nil {
		t.Fatalf("decoding deploy/people-crd.yaml: %v", err)
	}
	return crd
}

func TestCRDIdentity(t *testing.T) {
	crd := loadCRD(t)

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"scope", string(crd.Spec.Scope), string(apiextensionsv1.ClusterScoped)},
		{"group", crd.Spec.Group, "people.liken.sh"},
		{"kind", crd.Spec.Names.Kind, "Person"},
		{"listKind", crd.Spec.Names.ListKind, "PersonList"},
		{"plural", crd.Spec.Names.Plural, "people"},
		{"singular", crd.Spec.Names.Singular, "person"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

func TestCRDHasOneServedStorageVersion(t *testing.T) {
	crd := loadCRD(t)

	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("got %d versions, want 1", len(crd.Spec.Versions))
	}
	v := crd.Spec.Versions[0]
	if v.Name != "v1alpha1" {
		t.Errorf("got version %q, want v1alpha1", v.Name)
	}
	if !v.Served {
		t.Error("version v1alpha1 is not served")
	}
	if !v.Storage {
		t.Error("version v1alpha1 is not the storage version")
	}
}

func TestCRDSchema(t *testing.T) {
	crd := loadCRD(t)
	schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	spec := schema.Properties["spec"]

	t.Run("displayName is required", func(t *testing.T) {
		if !contains(spec.Required, "displayName") {
			t.Errorf("spec.required = %v, want it to contain displayName", spec.Required)
		}
	})

	t.Run("uid has a minimum of 1000", func(t *testing.T) {
		uid := spec.Properties["uid"]
		if uid.Minimum == nil || *uid.Minimum != 1000 {
			t.Errorf("spec.properties.uid.minimum = %v, want 1000", uid.Minimum)
		}
	})

	t.Run("identity requires issuer and subject", func(t *testing.T) {
		identity := spec.Properties["identity"]
		if !contains(identity.Required, "issuer") {
			t.Errorf("spec.properties.identity.required = %v, want it to contain issuer", identity.Required)
		}
		if !contains(identity.Required, "subject") {
			t.Errorf("spec.properties.identity.required = %v, want it to contain subject", identity.Required)
		}
	})
}

func TestCRDPrinterColumns(t *testing.T) {
	crd := loadCRD(t)
	columns := crd.Spec.Versions[0].AdditionalPrinterColumns

	want := []string{"Name", "UID", "Age"}
	for _, name := range want {
		t.Run(name, func(t *testing.T) {
			for _, col := range columns {
				if col.Name == name {
					return
				}
			}
			t.Errorf("no printer column named %q", name)
		})
	}
}

// schemaValidator builds the validator the API server itself would
// run a Person through, from the CRD's own schema. The validation
// package works against the internal JSONSchemaProps type, so the
// scheme converts the CRD's v1 schema into it.
func schemaValidator(t *testing.T) validation.SchemaValidator {
	t.Helper()
	crd := loadCRD(t)

	scheme := runtime.NewScheme()
	install.Install(scheme)

	internal := &apiextensions.JSONSchemaProps{}
	if err := scheme.Convert(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, internal, nil); err != nil {
		t.Fatalf("converting the schema: %v", err)
	}

	validator, _, err := validation.NewSchemaValidator(internal)
	if err != nil {
		t.Fatalf("building the schema validator: %v", err)
	}
	return validator
}

func TestCRDValidatesExamples(t *testing.T) {
	validator := schemaValidator(t)

	cases := []struct {
		name    string
		person  map[string]any
		wantErr bool
	}{
		{
			name: "a person with a display name",
			person: map[string]any{
				"apiVersion": "people.liken.sh/v1alpha1",
				"kind":       "Person",
				"metadata":   map[string]any{"name": "chris"},
				"spec":       map[string]any{"displayName": "Chris"},
			},
			wantErr: false,
		},
		{
			name: "a person with a stated uid and an identity",
			person: map[string]any{
				"apiVersion": "people.liken.sh/v1alpha1",
				"kind":       "Person",
				"metadata":   map[string]any{"name": "sam"},
				"spec": map[string]any{
					"displayName": "Sam",
					"uid":         int64(2000),
					"identity": map[string]any{
						"issuer":  "https://idp.example/",
						"subject": "sam",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "no displayName",
			person: map[string]any{
				"apiVersion": "people.liken.sh/v1alpha1",
				"kind":       "Person",
				"metadata":   map[string]any{"name": "chris"},
				"spec":       map[string]any{},
			},
			wantErr: true,
		},
		{
			name: "a uid under the reserved range",
			person: map[string]any{
				"apiVersion": "people.liken.sh/v1alpha1",
				"kind":       "Person",
				"metadata":   map[string]any{"name": "chris"},
				"spec":       map[string]any{"displayName": "Chris", "uid": int64(5)},
			},
			wantErr: true,
		},
		{
			name: "an identity with no subject",
			person: map[string]any{
				"apiVersion": "people.liken.sh/v1alpha1",
				"kind":       "Person",
				"metadata":   map[string]any{"name": "chris"},
				"spec": map[string]any{
					"displayName": "Chris",
					"identity":    map[string]any{"issuer": "https://idp.example/"},
				},
			},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validation.ValidateCustomResource(field.NewPath(""), c.person, validator)
			if got := len(errs) > 0; got != c.wantErr {
				t.Errorf("got errors %v, want an error: %v", errs, c.wantErr)
			}
		})
	}
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
