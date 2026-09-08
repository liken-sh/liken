package main

// These tests run the CRD through the same code the API server runs:
// the structural schema validator for the shape, and the CEL
// validator for the rules the schema states. An example that the
// cluster would refuse fails here instead.

import (
	"os"
	"slices"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	celschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

func loadCRD(t *testing.T) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile("deploy/receivers-crd.yaml")
	if err != nil {
		t.Fatalf("reading deploy/receivers-crd.yaml: %v", err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(raw, crd); err != nil {
		t.Fatalf("decoding deploy/receivers-crd.yaml: %v", err)
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
		{"group", crd.Spec.Group, "equipment.liken.sh"},
		{"kind", crd.Spec.Names.Kind, "Receiver"},
		{"listKind", crd.Spec.Names.ListKind, "ReceiverList"},
		{"plural", crd.Spec.Names.Plural, "receivers"},
		{"singular", crd.Spec.Names.Singular, "receiver"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustMatch(t, c.got, c.want)
		})
	}
}

func TestCRDHasOneServedStorageVersion(t *testing.T) {
	crd := loadCRD(t)

	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("got %d versions, want 1", len(crd.Spec.Versions))
	}
	v := crd.Spec.Versions[0]
	mustMatch(t, v.Name, "v1alpha1")
	if !v.Served {
		t.Error("version v1alpha1 is not served")
	}
	if !v.Storage {
		t.Error("version v1alpha1 is not the storage version")
	}
}

// The status subresource is what keeps this operator's writes off a
// spec a person declared.
func TestCRDHasAStatusSubresource(t *testing.T) {
	crd := loadCRD(t)

	subresources := crd.Spec.Versions[0].Subresources
	if subresources == nil || subresources.Status == nil {
		t.Fatalf("subresources = %+v, want a status subresource", subresources)
	}
}

func TestCRDSchema(t *testing.T) {
	crd := loadCRD(t)
	schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	spec := schema.Properties["spec"]

	t.Run("a Receiver carries a spec", func(t *testing.T) {
		if !slices.Contains(schema.Required, "spec") {
			t.Errorf("required = %v, want it to contain spec", schema.Required)
		}
	})

	t.Run("denon requires an address", func(t *testing.T) {
		denon := spec.Properties["denon"]
		if !slices.Contains(denon.Required, "address") {
			t.Errorf("spec.properties.denon.required = %v, want it to contain address", denon.Required)
		}
	})

	t.Run("an input requires all three of its fields", func(t *testing.T) {
		input := spec.Properties["inputs"].Items.Schema
		for _, name := range []string{"name", "machine", "monitor"} {
			if !slices.Contains(input.Required, name) {
				t.Errorf("spec.properties.inputs.items.required = %v, want it to contain %s", input.Required, name)
			}
		}
	})

	t.Run("the volume block states a ceiling and a step", func(t *testing.T) {
		volume := spec.Properties["volume"]
		for _, name := range []string{"max", "step"} {
			if _, held := volume.Properties[name]; !held {
				t.Errorf("spec.properties.volume has no property %s", name)
			}
		}
	})

	t.Run("a session requires all three of its fields", func(t *testing.T) {
		session := spec.Properties["session"]
		for _, name := range []string{"player", "input", "volumeTopic"} {
			if !slices.Contains(session.Required, name) {
				t.Errorf("spec.properties.session.required = %v, want it to contain %s", session.Required, name)
			}
		}
	})

	t.Run("the status holds what the operator reads back", func(t *testing.T) {
		status := schema.Properties["status"]
		for _, name := range []string{"power", "input", "volume", "volumeMax", "mute", "soundMode", "service", "conditions"} {
			if _, held := status.Properties[name]; !held {
				t.Errorf("status has no property %s", name)
			}
		}
	})
}

func TestCRDPrinterColumns(t *testing.T) {
	crd := loadCRD(t)
	columns := crd.Spec.Versions[0].AdditionalPrinterColumns

	paths := make(map[string]string, len(columns))
	for _, column := range columns {
		paths[column.Name] = column.JSONPath
	}

	cases := []struct{ name, jsonPath string }{
		{"Power", ".status.power"},
		{"Input", ".status.input"},
		{"Volume", ".status.volume"},
		{"Reachable", `.status.conditions[?(@.type=="Reachable")].status`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustMatch(t, paths[c.name], c.jsonPath)
		})
	}
}

// The API server runs the whole definition through this before it
// serves the resource, and that pass is where a CEL rule is compiled
// and its cost estimated. A rule the estimator refuses is a CRD the
// cluster refuses whole, and no amount of validating objects against
// the schema would find it.
func TestTheAPIServerWouldAcceptTheCRD(t *testing.T) {
	scheme := runtime.NewScheme()
	install.Install(scheme)

	internal := &apiextensions.CustomResourceDefinition{}
	mustSucceed(t, scheme.Convert(loadCRD(t), internal, nil))
	// The API server fills the stored versions in when it creates the
	// definition; a manifest states none.
	internal.Status.StoredVersions = []string{internal.Spec.Versions[0].Name}

	if errs := crdvalidation.ValidateCustomResourceDefinition(t.Context(), internal); len(errs) > 0 {
		t.Errorf("the API server would refuse the definition: %v", errs)
	}
}

// internalSchema converts the CRD's v1 schema into the internal type
// both validators work against, which is the same conversion the API
// server does before it serves the resource.
func internalSchema(t *testing.T) *apiextensions.JSONSchemaProps {
	t.Helper()
	crd := loadCRD(t)

	scheme := runtime.NewScheme()
	install.Install(scheme)

	internal := &apiextensions.JSONSchemaProps{}
	if err := scheme.Convert(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, internal, nil); err != nil {
		t.Fatalf("converting the schema: %v", err)
	}
	return internal
}

// schemaValidator builds the validator the API server itself would
// run a Receiver through.
func schemaValidator(t *testing.T) validation.SchemaValidator {
	t.Helper()
	validator, _, err := validation.NewSchemaValidator(internalSchema(t))
	if err != nil {
		t.Fatalf("building the schema validator: %v", err)
	}
	return validator
}

// celValidator builds the validator that runs the schema's
// x-kubernetes-validations rules. It is a separate pass from the
// structural one, the way the API server runs it, so a rule is proved
// only by calling this.
func celValidator(t *testing.T) (*celschema.Validator, *structuralschema.Structural) {
	t.Helper()
	structural, err := structuralschema.NewStructural(internalSchema(t))
	if err != nil {
		t.Fatalf("building the structural schema: %v", err)
	}
	validator := celschema.NewValidator(structural, true, celconfig.PerCallLimit)
	if validator == nil {
		t.Fatal("the schema states no x-kubernetes-validations rule")
	}
	return validator, structural
}

// validateReceiver runs both passes, so a case states one verdict for
// the whole object the way the API server answers one.
func validateReceiver(t *testing.T, receiver map[string]any) field.ErrorList {
	t.Helper()
	errs := validation.ValidateCustomResource(field.NewPath(""), receiver, schemaValidator(t))
	validator, structural := celValidator(t)
	celErrs, _ := validator.Validate(
		t.Context(), field.NewPath(""), structural, receiver, nil, celconfig.RuntimeCELCostBudget)
	return append(errs, celErrs...)
}

// receiver builds an object the cluster accepts, so each case below
// states the one thing it changes.
func receiver(spec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "equipment.liken.sh/v1alpha1",
		"kind":       "Receiver",
		"metadata":   map[string]any{"name": "theater"},
		"spec":       spec,
	}
}

func denonSpec() map[string]any {
	return map[string]any{
		"denon":  map[string]any{"address": "receiver.example"},
		"volume": map[string]any{"max": 75.0},
	}
}

func TestCRDValidatesExamples(t *testing.T) {
	cases := []struct {
		name     string
		receiver map[string]any
		wantErr  bool
	}{
		{
			name:     "a receiver on one protocol",
			receiver: receiver(denonSpec()),
		},
		{
			name: "a receiver with inputs and a session",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 75.0},
				"inputs": []any{
					map[string]any{"name": "MPLAY", "machine": "node-1", "monitor": "hdmi-a-1"},
					map[string]any{"name": "GAME", "machine": "node-2", "monitor": "hdmi-a-1"},
				},
				"session": map[string]any{
					"player":      "house/theater",
					"input":       "MPLAY",
					"volumeTopic": "liken/media/house/theater/volume",
				},
			}),
		},
		{
			name: "a receiver with a volume ceiling and step",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 75.0, "step": 0.5},
			}),
		},
		{
			name: "a volume block that states only a ceiling",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 75.0},
			}),
		},
		{
			name: "a volume ceiling of zero",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 0.0},
			}),
			wantErr: true,
		},
		{
			name: "a volume step of zero",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 75.0, "step": 0.0},
			}),
			wantErr: true,
		},
		{
			name: "a volume step below zero",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 75.0, "step": -1.0},
			}),
			wantErr: true,
		},
		{
			name:     "a denon with no volume ceiling",
			receiver: receiver(map[string]any{"denon": map[string]any{"address": "receiver.example"}}),
			wantErr:  true,
		},
		{
			name: "a denon whose volume block states only a step",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"step": 1.0},
			}),
			wantErr: true,
		},
		{
			name:     "no protocol block",
			receiver: receiver(map[string]any{}),
			wantErr:  true,
		},
		{
			name:     "a denon with no address",
			receiver: receiver(map[string]any{"denon": map[string]any{}, "volume": map[string]any{"max": 75.0}}),
			wantErr:  true,
		},
		{
			name: "two inputs under one name",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 75.0},
				"inputs": []any{
					map[string]any{"name": "MPLAY", "machine": "node-1", "monitor": "hdmi-a-1"},
					map[string]any{"name": "MPLAY", "machine": "node-2", "monitor": "hdmi-a-1"},
				},
			}),
			wantErr: true,
		},
		{
			name: "an input with no monitor",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 75.0},
				"inputs": []any{map[string]any{"name": "MPLAY", "machine": "node-1"}},
			}),
			wantErr: true,
		},
		{
			name: "a session whose player is not namespace/name",
			receiver: receiver(map[string]any{
				"denon":  map[string]any{"address": "receiver.example"},
				"volume": map[string]any{"max": 75.0},
				"session": map[string]any{
					"player":      "theater",
					"input":       "MPLAY",
					"volumeTopic": "liken/media/house/theater/volume",
				},
			}),
			wantErr: true,
		},
		{
			name: "a session with no volume topic",
			receiver: receiver(map[string]any{
				"denon":   map[string]any{"address": "receiver.example"},
				"volume":  map[string]any{"max": 75.0},
				"session": map[string]any{"player": "house/theater", "input": "MPLAY"},
			}),
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validateReceiver(t, c.receiver)
			if got := len(errs) > 0; got != c.wantErr {
				t.Errorf("got errors %v, want an error: %v", errs, c.wantErr)
			}
		})
	}
}

// A Receiver with no spec never reaches the one-of rule, because CEL
// runs only where the field it is written on exists. The root
// required list is what refuses it.
func TestCRDRefusesAReceiverWithNoSpec(t *testing.T) {
	errs := validateReceiver(t, map[string]any{
		"apiVersion": "equipment.liken.sh/v1alpha1",
		"kind":       "Receiver",
		"metadata":   map[string]any{"name": "theater"},
	})

	if len(errs) == 0 {
		t.Error("a Receiver with no spec passed validation")
	}
}

// The example is the one a reader copies, so the cluster has to
// accept it.
func TestTheExampleReceiverValidates(t *testing.T) {
	raw, err := os.ReadFile("testdata/receiver.yaml")
	mustSucceed(t, err)

	example := map[string]any{}
	mustSucceed(t, yaml.UnmarshalStrict(raw, &example))

	if errs := validateReceiver(t, example); len(errs) > 0 {
		t.Errorf("testdata/receiver.yaml: %v", errs)
	}
}
