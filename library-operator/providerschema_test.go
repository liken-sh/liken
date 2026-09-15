package main

// What these tests read: the MetadataProvider CRD as the cluster applies it,
// against the operator that reconciles it. The two hold one vocabulary and
// one set of blocks, so a spec the API server admits is a spec this operator
// can serve.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The schema of the one version this CRD serves.
func providerSchema(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("deploy/metadataproviders-crd.yaml")
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

// One nested field of a schema, by the path of its keys.
func schemaField(t *testing.T, from map[string]any, keys ...string) any {
	t.Helper()
	var held any = from
	for _, key := range keys {
		object, ok := held.(map[string]any)
		if !ok {
			t.Fatalf("%s is no object", strings.Join(keys, "."))
		}
		held = object[key]
	}
	return held
}

// Every block the operator holds a row for, which the CRD names one by one.
var providerBlocks = []string{providerBlockTMDb, providerBlockOMDb,
	providerBlockFanart, providerBlockTVmaze, providerBlockPeerTube}

// The field names one block's schema requires, none for a block that requires
// nothing.
func requiredNames(held any) []string {
	names := []string{}
	values, _ := held.([]any)
	for _, value := range values {
		names = append(names, value.(string))
	}
	return names
}

// The enum the CRD admits is the vocabulary the operator holds, so a person
// cannot name a fact no container runs, and a fact the operator serves is a
// fact a spec can narrow to.
func TestTheFactsEnumIsTheOperatorsVocabulary(t *testing.T) {
	schema := providerSchema(t)
	enum := schemaField(t, schema, "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "facts", "items", "enum").([]any)

	names := []string{}
	for _, fact := range enum {
		names = append(names, fact.(string))
	}
	if !slices.Equal(names, factVocabulary) {
		t.Errorf("the enum is %v, want %v", names, factVocabulary)
	}
}

// The CRD holds one block per row of the operator's table, and a block whose
// provider takes a key requires the Secret that holds it.
func TestTheCRDHoldsOneBlockPerProvider(t *testing.T) {
	schema := providerSchema(t)
	blocks := schemaField(t, schema, "schema", "openAPIV3Schema", "properties",
		"spec", "properties").(map[string]any)

	for _, block := range providerBlocks {
		t.Run(block, func(t *testing.T) {
			if _, held := blocks[block]; !held {
				t.Fatalf("the spec holds no %s block", block)
			}
			if _, held := providerFacts[block]; !held {
				t.Errorf("the operator's table holds no row for %s", block)
			}
			required := requiredNames(schemaField(t, blocks, block, "required"))
			wantsSecret := providerOfBlock("one", block).secretRef() != nil
			if wantsSecret != slices.Contains(required, "secretRef") {
				t.Errorf("%s requires %v, and the operator reads a Secret: %v",
					block, required, wantsSecret)
			}
		})
	}
}

// The peertube block names one instance by its address and holds no Secret.
func TestThePeerTubeBlockNamesAnEndpoint(t *testing.T) {
	schema := providerSchema(t)
	block := schemaField(t, schema, "schema", "openAPIV3Schema", "properties",
		"spec", "properties", providerBlockPeerTube).(map[string]any)

	if got := requiredNames(block["required"]); !slices.Equal(got, []string{"endpoint"}) {
		t.Errorf("peertube requires %v, want the endpoint alone", got)
	}
	endpoint := schemaField(t, block, "properties", "endpoint").(map[string]any)
	if endpoint["type"] != "string" {
		t.Errorf("the endpoint is %v, want a string", endpoint["type"])
	}
	if endpoint["pattern"] != "^https://" {
		t.Errorf("the endpoint admits %v, want an https address", endpoint["pattern"])
	}
}

// The API server admits one block and refuses none, two, or more, which is
// what makes an account one account with one provider.
func TestTheSpecAdmitsExactlyOneBlock(t *testing.T) {
	schema := providerSchema(t)
	rules := schemaField(t, schema, "schema", "openAPIV3Schema", "properties",
		"spec", "x-kubernetes-validations").([]any)

	if len(rules) != 1 {
		t.Fatalf("the spec holds %d rules, want the one that admits one block", len(rules))
	}
	rule := rules[0].(map[string]any)["rule"].(string)
	if !strings.Contains(rule, "exists_one") {
		t.Errorf("the rule is %q, want one that admits exactly one block", rule)
	}
	for _, block := range providerBlocks {
		if !strings.Contains(rule, "has(self."+block+")") {
			t.Errorf("the rule is %q, and it does not read the %s block", rule, block)
		}
	}
}

// The PROVIDER column reads the block off the status, because no printer
// column can read which block a spec holds.
func TestThePrinterColumnsShowTheProvider(t *testing.T) {
	schema := providerSchema(t)
	columns := schemaField(t, schema, "additionalPrinterColumns").([]any)

	paths := map[string]string{}
	for _, column := range columns {
		one := column.(map[string]any)
		paths[one["name"].(string)] = one["jsonPath"].(string)
	}
	if paths["Provider"] != ".status.provider" {
		t.Errorf("the PROVIDER column reads %q, want .status.provider", paths["Provider"])
	}
}
