package main

import (
	"strings"
	"testing"
)

func TestTypeCell(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema string
		want   string
	}{
		{"scalar", `{"type":"string"}`, "string"},
		{"integer", `{"type":"integer"}`, "integer"},
		{"format", `{"type":"string","format":"binary"}`, "string (binary)"},
		{"null beside a type", `{"type":["string","null"]}`, "string or null"},
		{"no type", `{}`, "object"},
		{"array of scalars", `{"type":"array","items":{"type":"string"}}`, "[]string"},
		{"array with no items", `{"type":"array"}`, "array"},
		{"map of scalars", `{"type":"object","additionalProperties":{"type":"integer"}}`, "map[string]integer"},
		{"component", `{"$ref":"#/components/schemas/Problem"}`, "[Problem](#problem)"},
		{"array of components", `{"type":"array","items":{"$ref":"#/components/schemas/Problem"}}`, `\[\][Problem](#problem)`},
		{"map of components", `{"type":"object","additionalProperties":{"$ref":"#/components/schemas/Problem"}}`,
			`map\[string\][Problem](#problem)`},
		{"a reference this page cannot link", `{"$ref":"https://example/schema.json"}`, "object"},
		{"an object declared inline", `{"type":"object","properties":{"a":{"type":"string"}}}`, "[object](#specfield)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := typeCell(parseSchema(t, tc.schema), "spec.field"); got != tc.want {
				t.Errorf("the result is %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTypeCellLinksNoSectionWithoutAPath(t *testing.T) {
	schema := parseSchema(t, `{"type":"object","properties":{"a":{"type":"string"}}}`)
	if got := typeCell(schema, ""); got != "object" {
		t.Errorf("the result is %q, want %q", got, "object")
	}
}

func TestCellText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema string
		want   string
	}{
		{"folds the lines", `{"description":"one\ntwo   three"}`, "one two three"},
		{"escapes the pipes", `{"description":"a | b"}`, `a \| b`},
		{"appends the enum", `{"description":"The mode.","enum":["simple","fancy"]}`,
			"The mode. One of: `simple`, `fancy`."},
		{"appends the default", `{"description":"A count.","default":1}`, "A count. Default: `1`."},
		{"appends the pattern", `{"description":"A name.","pattern":"^[a-z]+$"}`, "A name. Pattern: `^[a-z]+$`."},
		{"facts with no description", `{"enum":["a"]}`, "One of: `a`."},
		{"nothing at all", `{"type":"string"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellText(parseSchema(t, tc.schema)); got != tc.want {
				t.Errorf("the result is %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRowAnchor(t *testing.T) {
	for _, tc := range []struct {
		path, name, want string
	}{
		{"Problem", "instance", "problem--instance"},
		{"Widget.faces[]", "edges", "widgetfaces--edges"},
	} {
		if got := rowAnchor(tc.path, tc.name); got != tc.want {
			t.Errorf("rowAnchor(%q, %q) = %q, want %q", tc.path, tc.name, got, tc.want)
		}
	}
}

func TestResolveStopsOnALoop(t *testing.T) {
	document := parseSchema(t, `{"components":{"schemas":{"A":{"$ref":"#/components/schemas/A"}}}}`)
	resolved := resolve(document, parseSchema(t, `{"$ref":"#/components/schemas/A"}`))
	if got := resolved.member("$ref").value(); got != "#/components/schemas/A" {
		t.Errorf("the result is %q, want the reference itself", got)
	}
}

func TestResolveKeepsAReferenceTheDocumentDoesNotDefine(t *testing.T) {
	document := parseSchema(t, `{"components":{"schemas":{}}}`)
	resolved := resolve(document, parseSchema(t, `{"$ref":"#/components/schemas/Missing"}`))
	if got := resolved.member("$ref").value(); got != "#/components/schemas/Missing" {
		t.Errorf("the result is %q, want the reference itself", got)
	}
}

func TestResolveReadsAnEscapedPointer(t *testing.T) {
	document := parseSchema(t, `{"components":{"schemas":{"a/b":{"description":"the one"}}}}`)
	resolved := resolve(document, parseSchema(t, `{"$ref":"#/components/schemas/a~1b"}`))
	if got := resolved.member("description").value(); got != "the one" {
		t.Errorf("the result is %q, want %q", got, "the one")
	}
}

func parseSchema(t *testing.T, fragment string) *node {
	t.Helper()
	parsed, err := parse([]byte(fragment))
	if err != nil {
		t.Fatalf("parsing the schema fragment: %v", err)
	}
	return parsed
}

// A line break inside a cell ends the Markdown table row early, so
// every line that starts a row must end it with "|".
func TestNoCellContainsALineBreak(t *testing.T) {
	page := generateDisplayPage(t)
	for _, line := range strings.Split(page, "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if !strings.HasSuffix(line, "|") {
			t.Errorf("the table row does not end with a pipe: %q", line)
		}
	}
}
