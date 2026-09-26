package main

// The Television definition, run through the same validators the API
// server runs. receiver_test.go holds the helpers.

import (
	"os"
	"testing"

	"sigs.k8s.io/yaml"
)

func televisionObject(spec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "equipment.liken.sh/v1alpha1",
		"kind":       "Television",
		"metadata":   map[string]any{"name": "den"},
		"spec":       spec,
	}
}

func TestTheTelevisionDefinitionValidatesExamples(t *testing.T) {
	onBus := map[string]any{"bus": "den"}
	cases := []struct {
		name    string
		tv      map[string]any
		wantErr bool
	}{
		{"a TV on a bus", televisionObject(map[string]any{"cec": onBus}), false},
		{"a TV to wake", televisionObject(map[string]any{"cec": onBus, "power": "On"}), false},
		{"a TV to put in standby", televisionObject(map[string]any{"cec": onBus, "power": "Standby"}), false},
		{"a power that is not On or Standby", televisionObject(map[string]any{"cec": onBus, "power": "ToOn"}), true},
		{"no protocol block", televisionObject(map[string]any{"power": "On"}), true},
		{"a cec block with no bus", televisionObject(map[string]any{"cec": map[string]any{}}), true},
		{"a cec block with an empty bus", televisionObject(map[string]any{"cec": map[string]any{"bus": ""}}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validateObject(t, televisionsCRD, c.tv)
			if got := len(errs) > 0; got != c.wantErr {
				t.Errorf("got errors %v, want an error: %v", errs, c.wantErr)
			}
		})
	}
}

func TestTheTelevisionDefinitionIdentity(t *testing.T) {
	crd := loadCRDFrom(t, televisionsCRD)
	version := crd.Spec.Versions[0]
	cases := []struct{ name, got, want string }{
		{"scope", string(crd.Spec.Scope), "Cluster"},
		{"group", crd.Spec.Group, "equipment.liken.sh"},
		{"kind", crd.Spec.Names.Kind, "Television"},
		{"plural", crd.Spec.Names.Plural, "televisions"},
		{"version", version.Name, "v1alpha1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustMatch(t, c.got, c.want)
		})
	}
	if version.Subresources == nil || version.Subresources.Status == nil {
		t.Error("the Television definition has no status subresource")
	}
}

// Two writers share the conditions: the Deployment owns Reachable and
// the node workload owns PowerApplied. Server-side apply keeps each
// writer's entry only when the list is a map keyed by type.
func TestTheTelevisionConditionsAreAMapKeyedByType(t *testing.T) {
	status := loadCRDFrom(t, televisionsCRD).Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"]
	conditions := status.Properties["conditions"]

	if conditions.XListType == nil || *conditions.XListType != "map" || len(conditions.XListMapKeys) != 1 || conditions.XListMapKeys[0] != "type" {
		t.Errorf("status.conditions is list type %v keyed by %v, want a map keyed by type", conditions.XListType, conditions.XListMapKeys)
	}
}

func TestTheExampleTelevisionValidates(t *testing.T) {
	raw, err := os.ReadFile("testdata/television.yaml")
	mustSucceed(t, err)
	example := map[string]any{}
	mustSucceed(t, yaml.UnmarshalStrict(raw, &example))

	if errs := validateObject(t, televisionsCRD, example); len(errs) > 0 {
		t.Errorf("testdata/television.yaml: %v", errs)
	}
}

// A status as both writers write it passes the schema, so no apply is
// refused for its shape.
func TestAWrittenTelevisionStatusValidates(t *testing.T) {
	tv := televisionObject(map[string]any{"cec": map[string]any{"bus": "den"}, "power": "On"})
	tv["status"] = map[string]any{
		"cec": map[string]any{
			"physicalAddress": "0.0.0.0", "logicalAddress": int64(0), "osdName": "TV", "vendor": "00e091", "cecVersion": "1.4",
		},
		"power":           "ToOn",
		"powerGeneration": int64(2),
		"displays": []any{map[string]any{
			"name": "acm-0001-receiver", "physicalAddress": "1.3.0.0", "via": map[string]any{"kind": "Receiver", "name": "den"},
		}},
		"conditions": []any{map[string]any{
			"type": "Reachable", "status": "True", "observedGeneration": int64(1), "reason": "Answers",
			"message": "the TV answers Give Device Power Status", "lastTransitionTime": "2026-09-26T12:00:00Z",
		}},
	}

	if errs := validateObject(t, televisionsCRD, tv); len(errs) > 0 {
		t.Errorf("a written status: %v", errs)
	}
}
