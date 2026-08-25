package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The golden test is the contract: one small CRD that exercises every
// shape the walker supports (required fields, enums, defaults,
// patterns, arrays of scalars and of objects, maps, and a pipe that
// must be escaped), and the exact page it must produce.
func TestGenerateMatchesGolden(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/sample.md")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Generate(crd, "testdata/sample-crd.yaml", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("generated page does not match testdata/sample.md:\n%s", string(got))
	}
}

// A preamble is hand-written prose the schema cannot hold, such as a
// link into the guides. It lands verbatim between the generated-from
// comment and the schema's own description, so the page opens with
// the words a person wrote for it.
func TestGenerateInsertsThePreamble(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	preamble, err := os.ReadFile("testdata/sample-preamble.md")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/sample-with-preamble.md")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Generate(crd, "testdata/sample-crd.yaml", Options{Preamble: preamble})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("generated page does not match testdata/sample-with-preamble.md:\n%s", string(got))
	}
}

// A postamble is the other half of the preamble:
// hand-written sections that belong under the field tables, such as
// the messages a resource answers on a bus. It lands verbatim after
// the last generated table, with one blank line before it.
func TestGenerateAppendsThePostamble(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	postamble, err := os.ReadFile("testdata/sample-postamble.md")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/sample-with-postamble.md")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Generate(crd, "testdata/sample-crd.yaml", Options{Postamble: postamble})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("generated page does not match testdata/sample-with-postamble.md:\n%s", string(got))
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
		got, err := Generate(crd, tc.path, Options{})
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if !strings.Contains(string(got), tc.heading) {
			t.Errorf("%s: generated page is missing %q", tc.path, tc.heading)
		}
	}
}

// An operator repository ships every CRD it
// serves in one manifest file, so the page for one resource names the
// kind it documents and the walker skips the rest of the stream, and
// the documents in it that are not CRDs.
func TestGenerateSelectsTheNamedKind(t *testing.T) {
	crd, err := os.ReadFile("testdata/multi-crd.yaml")
	if err != nil {
		t.Fatal(err)
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
				t.Fatal(err)
			}
			if !strings.Contains(string(got), "title: "+tc.kind+"\n") {
				t.Errorf("page is not titled %s:\n%s", tc.kind, string(got))
			}
			if !strings.Contains(string(got), tc.row) {
				t.Errorf("page is missing %s, the %s field:\n%s", tc.row, tc.kind, string(got))
			}
		})
	}
}

func TestGenerateRefusesAStreamWithNoKindNamed(t *testing.T) {
	crd, err := os.ReadFile("testdata/multi-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Generate(crd, "testdata/multi-crd.yaml", Options{})
	if err == nil {
		t.Fatal("a file of several CRDs must be refused when no kind names one")
	}
	for _, kind := range []string{"Widget", "Gadget", "Sprocket"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("error does not name %s: %v", kind, err)
		}
	}
}

func TestGenerateRefusesAKindTheStreamDoesNotHold(t *testing.T) {
	crd, err := os.ReadFile("testdata/multi-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Generate(crd, "testdata/multi-crd.yaml", Options{Kind: "Doohickey"})
	if err == nil {
		t.Fatal("a kind the file does not hold must be refused")
	}
	for _, kind := range []string{"Widget", "Gadget", "Sprocket"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("error does not name %s: %v", kind, err)
		}
	}
}

// A stream with one CRD in it needs no kind,
// which keeps the single-CRD call the manual's own pages make.
func TestGenerateTakesTheOnlyKindWithoutBeingTold(t *testing.T) {
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
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "title: Widget\n") {
		t.Errorf("page is not titled Widget:\n%s", string(got))
	}
}

// The front matter title and weight order a
// repository's generated pages against each other, so both are
// settable, and both keep the value this repository's pages already
// carry when nothing sets them.
func TestGenerateTitlesAndWeightsThePage(t *testing.T) {
	crd, err := os.ReadFile("testdata/sample-crd.yaml")
	if err != nil {
		t.Fatal(err)
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
				t.Fatal(err)
			}
			if !strings.Contains(string(got), tc.want) {
				t.Errorf("front matter is missing %q:\n%s", tc.want, string(got))
			}
		})
	}
}

func TestGenerateRefusesANonCRD(t *testing.T) {
	_, err := Generate([]byte("apiVersion: v1\nkind: ConfigMap\n"), "x.yaml", Options{})
	if err == nil {
		t.Error("a non-CRD document must be refused")
	}
}

func TestGenerateRefusesAnEmptyFile(t *testing.T) {
	_, err := Generate(nil, "x.yaml", Options{})
	if err == nil {
		t.Error("a file with no YAML document must be refused")
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

// The Makefile rules in this repository pass
// three positional arguments and no flag, and they must keep working
// exactly as they are. The flags all come before the positional
// arguments.
func TestRunTakesTheFlagsBeforeThePositionalArguments(t *testing.T) {
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
				t.Fatal(err)
			}
			page, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(page), tc.want) {
				t.Errorf("page is missing %q:\n%s", tc.want, string(page))
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
			t.Errorf("run(%q) must be refused", args)
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
