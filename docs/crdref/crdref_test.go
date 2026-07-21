package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The golden test is the contract: one small CRD that exercises every
// shape the walker knows (required fields, enums, defaults, patterns,
// arrays of scalars and of objects, maps, and a pipe that must be
// escaped), and the exact page it must produce.
func TestGenerateMatchesGolden(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/sample.md")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Generate(crd, "testdata/sample-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("generated page does not match testdata/sample.md:\n%s", string(got))
	}
}

// The real CRDs must generate without error, and their well-known
// sections must appear. The golden test pins the format; this test
// pins the generator to the schemas it exists for.
func TestGenerateHandlesTheRealCRDs(t *testing.T) {
	for _, tc := range []struct {
		path    string
		heading string
	}{
		{"../../machine/manifests/machines-crd.yaml", "### spec.storage"},
		{"../../cluster/manifests/clusters-crd.yaml", "### spec.releases"},
	} {
		crd, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Generate(crd, tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if !strings.Contains(string(got), tc.heading) {
			t.Errorf("%s: generated page is missing %q", tc.path, tc.heading)
		}
	}
}

func TestGenerateRefusesANonCRD(t *testing.T) {
	_, err := Generate([]byte("apiVersion: v1\nkind: ConfigMap\n"), "x.yaml")
	if err == nil {
		t.Error("a non-CRD document must be refused")
	}
}

func TestGenerateRefusesAMissingSchema(t *testing.T) {
	doc := `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  names:
    kind: Widget
  versions:
    - name: v1alpha1
`
	_, err := Generate([]byte(doc), "x.yaml")
	if err == nil {
		t.Error("a CRD without a schema must be refused")
	}
}

func TestFieldType(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema string
		want   string
	}{
		{"scalar", "type: string", "string"},
		{"integer", "type: integer", "integer"},
		{"object", "type: object", "object"},
		{"array of scalars", "type: array\nitems:\n  type: string", "[]string"},
		{"array of objects", "type: array\nitems:\n  type: object", "[]object"},
		{"map of scalars", "type: object\nadditionalProperties:\n  type: string", "map[string]string"},
		{"map of arrays", "type: object\nadditionalProperties:\n  type: array\n  items:\n    type: string", "map[string][]string"},
		{"map of objects", "type: object\nadditionalProperties:\n  type: object", "map[string]object"},
		{"untyped with properties", "properties:\n  a:\n    type: string", "object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fieldType(parseSchema(t, tc.schema)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCellText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema string
		want   string
	}{
		{
			"folds whitespace",
			"type: string\ndescription: \"one\\ntwo   three\"",
			"one two three",
		},
		{
			"escapes pipes",
			"type: string\ndescription: \"a | b\"",
			`a \| b`,
		},
		{
			"appends enum",
			"type: string\ndescription: The mode.\nenum: [simple, fancy]",
			"The mode. One of: `simple`, `fancy`.",
		},
		{
			"appends default",
			"type: integer\ndescription: A count.\ndefault: 1",
			"A count. Default: `1`.",
		},
		{
			"appends pattern",
			"type: string\ndescription: A name.\npattern: ^[a-z]+$",
			"A name. Pattern: `^[a-z]+$`.",
		},
		{
			"no description",
			"type: string",
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellText(parseSchema(t, tc.schema)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The anchors must match the ids Hugo generates for the headings
// (lowercase, punctuation dropped), or every type link would point
// at nothing. These cases mirror ids verified against a built site.
func TestAnchorFor(t *testing.T) {
	for path, want := range map[string]string{
		"spec.network.interfaces[]": "specnetworkinterfaces",
		"spec.storage.biosBoot":     "specstoragebiosboot",
		"spec.features.*":           "specfeatures",
	} {
		if got := anchorFor(path); got != want {
			t.Errorf("anchorFor(%q) = %q, want %q", path, got, want)
		}
	}
}

// parseSchema turns an inline YAML fragment into the schema node the
// walker's helpers take, so each table row above stays one line of
// YAML instead of a fixture file.
func parseSchema(t *testing.T, fragment string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fragment), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func TestDisplayPath(t *testing.T) {
	for path, want := range map[string]string{
		"../machine/manifests/machines-crd.yaml": "machine/manifests/machines-crd.yaml",
		"testdata/sample-crd.yaml":               "testdata/sample-crd.yaml",
	} {
		if got := displayPath(path); got != want {
			t.Errorf("displayPath(%q) = %q, want %q", path, got, want)
		}
	}
}
