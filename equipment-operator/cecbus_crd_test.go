package main

// The CECBus definition, run through the same validators the API
// server runs. receiver_test.go holds the helpers.

import (
	"os"
	"testing"

	"sigs.k8s.io/yaml"
)

func cecBus(spec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "equipment.liken.sh/v1alpha1",
		"kind":       "CECBus",
		"metadata":   map[string]any{"name": "den"},
		"spec":       spec,
	}
}

func TestTheCECBusDefinitionValidatesExamples(t *testing.T) {
	adapter := func(fields ...string) map[string]any {
		entry := map[string]any{}
		for index := 0; index+1 < len(fields); index += 2 {
			entry[fields[index]] = fields[index+1]
		}
		return entry
	}
	cases := []struct {
		name    string
		bus     map[string]any
		wantErr bool
	}{
		{"a bus in Listen with no display", cecBus(map[string]any{"mode": "Listen", "adapters": []any{adapter("machine", "node-1")}}), false},
		{"a bus in Control with a display", cecBus(map[string]any{"mode": "Control", "adapters": []any{adapter("machine", "node-1", "display", "acm-0001-receiver")}}), false},
		{"a bus in Control with an adapter that names no display", cecBus(map[string]any{"mode": "Control", "adapters": []any{
			adapter("machine", "node-1", "display", "acm-0001-receiver"), adapter("machine", "node-2")}}), true},
		{"a mode that is not Listen or Control", cecBus(map[string]any{"mode": "Scan", "adapters": []any{adapter("machine", "node-1")}}), true},
		{"no mode", cecBus(map[string]any{"adapters": []any{adapter("machine", "node-1")}}), true},
		{"no adapter", cecBus(map[string]any{"mode": "Listen", "adapters": []any{}}), true},
		{"an adapter with no machine", cecBus(map[string]any{"mode": "Listen", "adapters": []any{adapter("display", "acm-0001-receiver")}}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validateObject(t, cecBusesCRD, c.bus)
			if got := len(errs) > 0; got != c.wantErr {
				t.Errorf("got errors %v, want an error: %v", errs, c.wantErr)
			}
		})
	}
}

func TestTheCECBusDefinitionIdentity(t *testing.T) {
	crd := loadCRDFrom(t, cecBusesCRD)
	version := crd.Spec.Versions[0]
	cases := []struct{ name, got, want string }{
		{"scope", string(crd.Spec.Scope), "Cluster"},
		{"group", crd.Spec.Group, "equipment.liken.sh"},
		{"kind", crd.Spec.Names.Kind, "CECBus"},
		{"plural", crd.Spec.Names.Plural, "cecbuses"},
		{"version", version.Name, "v1alpha1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustMatch(t, c.got, c.want)
		})
	}
	if version.Subresources == nil || version.Subresources.Status == nil {
		t.Error("the CECBus definition has no status subresource")
	}
}

// Server-side apply merges the node workloads' entries only when the
// list is a map keyed by machine. Without the key, each node's apply
// would replace the whole list and remove the other nodes' entries.
func TestTheAdapterEntriesAreAMapKeyedByMachine(t *testing.T) {
	status := loadCRDFrom(t, cecBusesCRD).Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"]
	adapters := status.Properties["adapters"]

	if adapters.XListType == nil || *adapters.XListType != "map" || len(adapters.XListMapKeys) != 1 || adapters.XListMapKeys[0] != "machine" {
		t.Errorf("status.adapters is list type %v keyed by %v, want a map keyed by machine", adapters.XListType, adapters.XListMapKeys)
	}
}

func TestTheExampleCECBusValidates(t *testing.T) {
	raw, err := os.ReadFile("testdata/cecbus.yaml")
	mustSucceed(t, err)
	example := map[string]any{}
	mustSucceed(t, yaml.UnmarshalStrict(raw, &example))

	if errs := validateObject(t, cecBusesCRD, example); len(errs) > 0 {
		t.Errorf("testdata/cecbus.yaml: %v", errs)
	}
}

// A status as the writers write it passes the schema, so no apply is
// refused for its shape.
func TestAWrittenStatusValidates(t *testing.T) {
	bus := cecBus(map[string]any{"mode": "Control", "adapters": []any{map[string]any{"machine": "node-1", "display": "acm-0001-receiver"}}})
	bus["status"] = map[string]any{
		"adapters": []any{map[string]any{
			"machine": "node-1", "mode": "Control", "state": "Scanned", "driver": "pulse8-cec",
			"physicalAddress": "1.3.0.0", "logicalAddress": int64(4), "osdName": "node-1", "reportedAt": "2026-09-26T12:00:00Z",
			"devices": []any{map[string]any{"logicalAddress": int64(0), "physicalAddress": "0.0.0.0", "type": "TV", "osdName": "TV", "vendor": "00e091", "cecVersion": "1.4", "power": "Standby"}},
		}},
		"devices": []any{map[string]any{"logicalAddress": int64(0), "physicalAddress": "0.0.0.0", "type": "TV"}},
		"conditions": []any{map[string]any{
			"type": "Joined", "status": "True", "observedGeneration": int64(1), "reason": "Joined",
			"message": "every adapter holds a logical address", "lastTransitionTime": "2026-09-26T12:00:00Z",
		}},
	}

	if errs := validateObject(t, cecBusesCRD, bus); len(errs) > 0 {
		t.Errorf("a written status: %v", errs)
	}
}
