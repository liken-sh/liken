package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// testdata/sample-crd.yaml has each shape that Generate handles:
// required fields, enums, defaults, patterns, arrays of scalars and of
// objects, maps, and a pipe that must be escaped.
func TestGenerateMatchesGolden(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatalf("reading testdata/sample-crd.yaml: %v", err)
	}
	want, err := os.ReadFile("testdata/sample.md")
	if err != nil {
		t.Fatalf("reading testdata/sample.md: %v", err)
	}
	got, err := Generate(crd, "testdata/sample-crd.yaml", Options{})
	if err != nil {
		t.Fatalf("generating the page: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("the generated page differs from testdata/sample.md:\n%s", string(got))
	}
}

// The preamble appears unchanged after the kind heading and before
// the schema's description.
func TestGenerateInsertsThePreamble(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatalf("reading testdata/sample-crd.yaml: %v", err)
	}
	preamble, err := os.ReadFile("testdata/sample-preamble.md")
	if err != nil {
		t.Fatalf("reading testdata/sample-preamble.md: %v", err)
	}
	want, err := os.ReadFile("testdata/sample-with-preamble.md")
	if err != nil {
		t.Fatalf("reading testdata/sample-with-preamble.md: %v", err)
	}
	got, err := Generate(crd, "testdata/sample-crd.yaml", Options{Preamble: preamble})
	if err != nil {
		t.Fatalf("generating the page: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("the generated page differs from testdata/sample-with-preamble.md:\n%s", string(got))
	}
}

// The postamble appears unchanged after the last generated table,
// separated from it by one blank line.
func TestGenerateAppendsThePostamble(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatalf("reading testdata/sample-crd.yaml: %v", err)
	}
	postamble, err := os.ReadFile("testdata/sample-postamble.md")
	if err != nil {
		t.Fatalf("reading testdata/sample-postamble.md: %v", err)
	}
	want, err := os.ReadFile("testdata/sample-with-postamble.md")
	if err != nil {
		t.Fatalf("reading testdata/sample-with-postamble.md: %v", err)
	}
	got, err := Generate(crd, "testdata/sample-crd.yaml", Options{Postamble: postamble})
	if err != nil {
		t.Fatalf("generating the page: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("the generated page differs from testdata/sample-with-postamble.md:\n%s", string(got))
	}
}

func TestGenerateSelectsTheNamedKind(t *testing.T) {
	crd, err := os.ReadFile("testdata/multi-crd.yaml")
	if err != nil {
		t.Fatalf("reading testdata/multi-crd.yaml: %v", err)
	}
	for _, tc := range []struct {
		kind string
		row  string
	}{
		{"Widget", "`width`"},
		{"Gadget", "`purpose`"},
		{"Sprocket", "`teeth`"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			got, err := Generate(crd, "testdata/multi-crd.yaml", Options{Kind: tc.kind})
			if err != nil {
				t.Fatalf("generating the page: %v", err)
			}
			if !strings.Contains(string(got), "title: "+tc.kind+"\n") {
				t.Errorf("the page title is not %s, want it in:\n%s", tc.kind, string(got))
			}
			if !strings.Contains(string(got), tc.row) {
				t.Errorf("the page has no row %s for the %s field:\n%s", tc.row, tc.kind, string(got))
			}
		})
	}
}

func TestGenerateRefusesAStreamWithNoKindNamed(t *testing.T) {
	crd, err := os.ReadFile("testdata/multi-crd.yaml")
	if err != nil {
		t.Fatalf("reading testdata/multi-crd.yaml: %v", err)
	}
	_, err = Generate(crd, "testdata/multi-crd.yaml", Options{})
	if err == nil {
		t.Fatal("Generate returned no error for a file of several CustomResourceDefinitions with no Kind, want one")
	}
	for _, kind := range []string{"Widget", "Gadget", "Sprocket"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("the error does not name %s: %v", kind, err)
		}
	}
}

func TestGenerateRefusesAKindTheStreamDoesNotContain(t *testing.T) {
	crd, err := os.ReadFile("testdata/multi-crd.yaml")
	if err != nil {
		t.Fatalf("reading testdata/multi-crd.yaml: %v", err)
	}
	_, err = Generate(crd, "testdata/multi-crd.yaml", Options{Kind: "Doohickey"})
	if err == nil {
		t.Fatal("Generate returned no error for a Kind that the file does not contain, want one")
	}
	for _, kind := range []string{"Widget", "Gadget", "Sprocket"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("the error does not name %s: %v", kind, err)
		}
	}
}

// A manifest with one CRD and other objects needs no Kind.
func TestGenerateSelectsTheOnlyKindWhenNoKindIsNamed(t *testing.T) {
	doc := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: not-a-crd
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  names:
    kind: Widget
  versions:
    - name: v1alpha1
      schema:
        openAPIV3Schema:
          type: object
          description: A Widget describes one widget.
`
	got, err := Generate([]byte(doc), "x.yaml", Options{})
	if err != nil {
		t.Fatalf("generating the page: %v", err)
	}
	if !strings.Contains(string(got), "title: Widget\n") {
		t.Errorf("the page title is not Widget:\n%s", string(got))
	}
}

func TestGenerateTitlesAndWeightsThePage(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatalf("reading testdata/sample-crd.yaml: %v", err)
	}
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"defaults", Options{}, "title: Widget\nweight: 10\n"},
		{"title", Options{Title: "Widgets"}, "title: Widgets\nweight: 10\n"},
		{"weight", Options{Weight: 15}, "title: Widget\nweight: 15\n"},
		{"both", Options{Title: "Widgets", Weight: 20}, "title: Widgets\nweight: 20\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Generate(crd, "testdata/sample-crd.yaml", tc.opts)
			if err != nil {
				t.Fatalf("generating the page: %v", err)
			}
			if !strings.Contains(string(got), tc.want) {
				t.Errorf("the front matter has no %q:\n%s", tc.want, string(got))
			}
		})
	}
}

// A Title changes only the front matter, not the heading.
func TestGenerateWritesTheKindAsTheFirstHeading(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatalf("reading testdata/sample-crd.yaml: %v", err)
	}
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"defaults", Options{}},
		{"title", Options{Title: "Widgets"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Generate(crd, "testdata/sample-crd.yaml", tc.opts)
			if err != nil {
				t.Fatalf("generating the page: %v", err)
			}
			if !strings.Contains(string(got), "-->\n\n# `Widget`\n\n") {
				t.Errorf("the page does not open with the kind as its heading:\n%s", string(got))
			}
		})
	}
}

func TestGenerateRefusesANonCRD(t *testing.T) {
	_, err := Generate([]byte("apiVersion: v1\nkind: ConfigMap\n"), "x.yaml", Options{})
	if err == nil {
		t.Error("Generate returned no error for a document that is not a CustomResourceDefinition, want one")
	}
}

func TestGenerateRefusesAnEmptyFile(t *testing.T) {
	_, err := Generate(nil, "x.yaml", Options{})
	if err == nil {
		t.Error("Generate returned no error for input with no YAML document, want one")
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
	_, err := Generate([]byte(doc), "x.yaml", Options{})
	if err == nil {
		t.Error("Generate returned no error for a CustomResourceDefinition with no schema, want one")
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
				t.Errorf("the result is %q, want %q", got, tc.want)
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
				t.Errorf("the result is %q, want %q", got, tc.want)
			}
		})
	}
}

func parseSchema(t *testing.T, fragment string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fragment), &doc); err != nil {
		t.Fatalf("parsing the schema fragment: %v", err)
	}
	return doc.Content[0]
}

// The docs Makefiles pass the positional arguments with and without
// flags, and the flags come first.
func TestRunAcceptsFlagsBeforeThePositionalArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"positional only",
			[]string{"testdata/sample-crd.yaml", "", "testdata/sample-preamble.md"},
			"title: Widget\nweight: 10\n",
		},
		{
			"kind, title, and weight",
			[]string{"-kind", "Gadget", "-title", "Gadgets", "-weight", "15", "testdata/multi-crd.yaml", ""},
			"title: Gadgets\nweight: 15\n",
		},
		{
			"postamble",
			[]string{"-postamble", "testdata/sample-postamble.md", "testdata/sample-crd.yaml", ""},
			"## On the bus",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "page.md")
			args := append([]string(nil), tc.args...)
			for i, a := range args {
				if a == "" {
					args[i] = out
				}
			}
			if err := run(args); err != nil {
				t.Fatalf("running crdref: %v", err)
			}
			page, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("reading the generated page: %v", err)
			}
			if !strings.Contains(string(page), tc.want) {
				t.Errorf("the page has no %q:\n%s", tc.want, string(page))
			}
		})
	}
}

func TestRunRefusesTheWrongArgumentCount(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"testdata/sample-crd.yaml"},
		{"a", "b", "c", "d"},
	} {
		if err := run(args); err == nil {
			t.Errorf("run(%q) returned no error, want one", args)
		}
	}
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
