package main

// playschema_test.go reads the Play CRD as the cluster applies it. The API
// server prunes each field that the schema does not name, so a field of the
// Go type that the schema lacks would never reach the display.

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

// The schema of one item's presentation, in the one version the CRD serves.
func presentationSchema(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("deploy/plays-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	node := document["spec"].(map[string]any)["versions"].([]any)[0]
	for _, key := range []string{"schema", "openAPIV3Schema", "properties", "spec", "properties", "items",
		"items", "properties", "presentation"} {
		node = node.(map[string]any)[key]
		if node == nil {
			t.Fatalf("the schema has no %s", key)
		}
	}
	return node.(map[string]any)
}

// missingFields lists each key of a JSON value that the schema does not name,
// by its path. A list is checked through its items' schema.
func missingFields(schema map[string]any, value any, path string) []string {
	var missing []string
	switch value := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for key, inner := range value {
			field, held := properties[key].(map[string]any)
			if !held {
				missing = append(missing, path+"."+key)
				continue
			}
			missing = append(missing, missingFields(field, inner, path+"."+key)...)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, inner := range value {
			missing = append(missing, missingFields(items, inner, path+"[]")...)
		}
	}
	slices.Sort(missing)
	return missing
}

// Every field of a video entry survives the API server, so the display gets
// the frame and the active area that the library wrote.
func TestThePlaySchemaKeepsEveryFieldOfAVideoStream(t *testing.T) {
	presentation := Presentation{Video: []PlayVideo{
		{Stream: 0, Width: 1920, Height: 1080, ActiveArea: &PlayActiveArea{Left: 0, Top: 131, Width: 1920, Height: 817}},
	}}
	written, err := json.Marshal(presentation)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(written, &value); err != nil {
		t.Fatal(err)
	}

	if missing := missingFields(presentationSchema(t), value, "presentation"); len(missing) > 0 {
		t.Errorf("the schema prunes %v", missing)
	}
}
