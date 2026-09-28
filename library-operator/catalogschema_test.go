package main

// What these tests read: the Catalog CRD as the cluster applies it,
// against the operator that reconciles it. The two hold one vocabulary,
// so a Catalog the API server admits is a Catalog this operator stands.

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// The schema of the one version this CRD serves.
func catalogSchema(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("deploy/catalogs-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	versions := document["spec"].(map[string]any)["versions"].([]any)
	return versions[0].(map[string]any)
}

// Both stores take a copy count, one is the default, and one is the
// floor: a Catalog that asks for none is refused at apply rather than
// standing a namespace with no catalog.
func TestTheSchemaTakesACopyCountForBothStores(t *testing.T) {
	for _, block := range []string{"storage", "progress"} {
		t.Run(block, func(t *testing.T) {
			replicas := schemaField(t, catalogSchema(t), "schema", "openAPIV3Schema", "properties",
				"spec", "properties", block, "properties", "replicas").(map[string]any)

			if replicas["type"] != "integer" {
				t.Errorf("type = %v, want an integer", replicas["type"])
			}
			if replicas["minimum"] != 1 {
				t.Errorf("minimum = %v, want 1", replicas["minimum"])
			}
			if replicas["default"] != 1 {
				t.Errorf("default = %v, want 1", replicas["default"])
			}
		})
	}
}

// The count the API server defaults is the count the operator stands,
// and a Catalog that names one of its own stands that many.
func TestTheOperatorStandsTheCopyCountTheSchemaAdmits(t *testing.T) {
	defaulted := &NamespaceCatalog{}
	if err := json.Unmarshal([]byte(`{"spec":{"storage":{"replicas":1},"progress":{"replicas":1}}}`),
		defaulted); err != nil {
		t.Fatal(err)
	}
	asked := &NamespaceCatalog{}
	if err := json.Unmarshal([]byte(`{"spec":{"storage":{"replicas":3},"progress":{"replicas":2}}}`),
		asked); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name             string
		catalog          *NamespaceCatalog
		catalogs, stores int
	}{
		{name: "a Catalog written before the field existed", catalog: &NamespaceCatalog{}, catalogs: 1, stores: 1},
		{name: "the count the API server defaults", catalog: defaulted, catalogs: 1, stores: 1},
		{name: "the count a Catalog asks for", catalog: asked, catalogs: 3, stores: 2},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := catalogReplicaCount(one.catalog); got != one.catalogs {
				t.Errorf("catalog copies = %d, want %d", got, one.catalogs)
			}
			if got := progressReplicaCount(one.catalog); got != one.stores {
				t.Errorf("progress copies = %d, want %d", got, one.stores)
			}
		})
	}
}

// The counts the status reports reach a person through kubectl, beside
// the size each copy was given.
func TestTheCatalogPrintsTheCopiesItStands(t *testing.T) {
	columns := catalogSchema(t)["additionalPrinterColumns"].([]any)

	paths := []string{}
	for _, column := range columns {
		paths = append(paths, column.(map[string]any)["jsonPath"].(string))
	}

	want := ".status.replicas.catalog.wanted"
	if !slices.Contains(paths, want) {
		t.Errorf("the printer columns read %v, want %q among them", paths, want)
	}
}
