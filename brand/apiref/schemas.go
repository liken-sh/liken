// The schemas: a JSON Schema rendered as the field tables the CRD
// reference pages use.
//
// A reader moves between the pages of a manual, so the two
// generators write the same kind of table. crdref renders a CRD's
// schema and this file renders an OpenAPI component schema, with the
// same four columns, the same dotted heading for a nested object,
// and the same id on each row.
package main

import (
	"fmt"
	"strings"

	"github.com/liken-sh/brand/linkcheck"
)

// componentPrefix is the pointer every $ref in these documents
// writes. OpenAPI allows a reference to any URI. The operators
// reference their own components and nothing else, so a $ref that
// leaves the document renders as its type word and links to
// nothing.
const componentPrefix = "#/components/schemas/"

// emitSchemas writes the section that every response and parameter
// table links into. The schemas come in the document's order, which
// is the order the program that wrote the document declared them.
func emitSchemas(b *strings.Builder, document *node) {
	schemas := document.member("components").member("schemas")
	if schemas == nil || len(schemas.members) == 0 {
		return
	}
	b.WriteString("## Schemas\n\n")
	schemas.each(func(name string, schema *node) {
		emitSchemaSection(b, document, name, schema, 3)
	})
}

// emitSchemaSection writes one object's heading, its description, a
// table of its direct fields, and then a section for each field that
// is an object of its own. The heading carries the schema's name, and
// under it the whole dotted path of each nested object, so every path
// on the page is searchable text. The heading level follows the
// depth, capped at four the way crdref caps it, because below four
// the headings would be too small to read.
func emitSchemaSection(b *strings.Builder, document *node, path string, schema *node, depth int) {
	fmt.Fprintf(b, "%s %s\n\n", strings.Repeat("#", min(depth, 4)), path)
	if d := foldText(schema.member("description").value()); d != "" {
		b.WriteString(d + "\n\n")
	}

	properties := schema.member("properties")
	if properties == nil || len(properties.members) == 0 {
		// A schema with no fields is a scalar, an array, or a free
		// object. It has no table, so the type and the values it
		// takes are a sentence.
		fmt.Fprintf(b, "%s\n\n", strings.TrimSpace(
			fmt.Sprintf("The type is %s. %s", typeCell(schema, path), facts(schema))))
		return
	}

	required := map[string]bool{}
	for _, name := range schema.member("required").elements() {
		required[name.value()] = true
	}

	b.WriteString("| Field | Type | Required | Description |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	properties.each(func(name string, field *node) {
		// A field written as a reference alone has no description of
		// its own, so its cell uses the description of the component it
		// names.
		text := cellText(field)
		if text == "" {
			text = cellText(resolve(document, field))
		}
		fmt.Fprintf(b, "| <span id=%q></span>`%s` | %s | %s | %s |\n",
			rowAnchor(path, name), name, typeCell(field, path+"."+name), yesNo(required[name]), text)
	})
	b.WriteString("\n")

	properties.each(func(name string, field *node) {
		childPath, child := childSection(field, path+"."+name)
		if child == nil {
			return
		}
		emitSchemaSection(b, document, childPath, child, depth+1)
	})
}

// rowAnchor gives one field's table row its own id: the section's
// anchor, two hyphens, then the field's, as in problem--instance. A
// link then lands on exactly one field. The two hyphens keep a row
// id apart from a heading id, because a heading id here comes from a
// dotted path and holds no hyphen.
func rowAnchor(path, name string) string {
	return linkcheck.Anchor(path) + "--" + linkcheck.Anchor(name)
}

// typeCell renders a schema's type for a table column. A named
// component becomes a link to its own section, and an object
// declared inline becomes a link to the section this page writes for
// it, so a reader lands on the definition instead of scanning for
// it. The brackets of an array or a map are escaped where the cell
// also holds a link, because they would otherwise read as part of
// the link's own syntax.
func typeCell(schema *node, path string) string {
	wrapper, leaf, leafPath := unwrap(schema, path)
	cell := leafCell(leaf, leafPath)
	if strings.Contains(cell, "](") {
		wrapper = strings.NewReplacer("[", `\[`, "]", `\]`).Replace(wrapper)
	}
	return wrapper + cell
}

// unwrap peels the arrays and maps off a schema and returns the text
// that stands in front of the element's own type, as in []
// or map[string][]. The schemas use additionalProperties exactly
// when an object is a map, so its presence is the map test.
func unwrap(schema *node, path string) (wrapper string, leaf *node, leafPath string) {
	var b strings.Builder
	for {
		switch {
		case refName(schema) != "":
			return b.String(), schema, path
		case typeWord(schema) == "array" && schema.member("items") != nil:
			b.WriteString("[]")
			schema, path = schema.member("items"), path+"[]"
		case isMap(schema):
			b.WriteString("map[string]")
			schema, path = schema.member("additionalProperties"), path+".*"
		default:
			return b.String(), schema, path
		}
	}
}

// leafCell renders the innermost type: a link to a named component,
// a link to the section for an object declared inline, or the type
// word with the format beside it.
func leafCell(schema *node, path string) string {
	if name := refName(schema); name != "" {
		return fmt.Sprintf("[%s](#%s)", name, linkcheck.Anchor(name))
	}
	// An empty path is a table outside the schemas section, where an
	// object declared inline has no section to link to.
	if hasProperties(schema) && path != "" {
		return fmt.Sprintf("[object](#%s)", linkcheck.Anchor(path))
	}
	word := typeWord(schema)
	if word == "" {
		word = "object"
	}
	if format := schema.member("format").value(); format != "" {
		return word + " (" + format + ")"
	}
	return word
}

// typeWord renders the type member. OpenAPI 3.1 is JSON Schema
// 2020-12, where type may be a list, which is how a document states
// that a field may also be null.
func typeWord(schema *node) string {
	member := schema.member("type")
	if member == nil {
		return ""
	}
	if words := member.elements(); len(words) > 0 {
		var parts []string
		for _, word := range words {
			parts = append(parts, word.value())
		}
		return strings.Join(parts, " or ")
	}
	return member.value()
}

// refName is the component's name for a $ref into this document's
// own components, and "" for anything else, including a reference
// this page cannot link.
func refName(schema *node) string {
	pointer := schema.member("$ref").value()
	if !strings.HasPrefix(pointer, componentPrefix) {
		return ""
	}
	return strings.TrimPrefix(pointer, componentPrefix)
}

// resolve follows a chain of references to the value that holds the
// fields, so a parameter or a response written as a component is
// rendered from the component itself. A reference this document does
// not hold resolves to the reference, which renders as an empty row
// rather than as nothing at all. The hop limit stops a document
// whose references form a loop.
func resolve(document *node, value *node) *node {
	for hops := 0; hops < 10; hops++ {
		pointer := value.member("$ref").value()
		if !strings.HasPrefix(pointer, "#/") {
			return value
		}
		target := pointerTarget(document, pointer)
		if target == nil {
			return value
		}
		value = target
	}
	return value
}

// pointerTarget walks one local JSON pointer, as in
// #/components/schemas/Problem. RFC 6901 writes a slash in a name as
// ~1 and a tilde as ~0.
func pointerTarget(document *node, pointer string) *node {
	unescape := strings.NewReplacer("~1", "/", "~0", "~")
	for _, step := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		document = document.member(unescape.Replace(step))
	}
	return document
}

// childSection finds the object a field's own section describes: the
// field itself, one element of an array, or one value of a map. The
// path suffix says which: [] for an element, .* for a value under
// any key. A field that names a component has no section of its own,
// because the component has one.
func childSection(field *node, childPath string) (string, *node) {
	if refName(field) != "" {
		return "", nil
	}
	items, values := field.member("items"), field.member("additionalProperties")
	switch {
	case hasProperties(field):
		return childPath, field
	case hasProperties(items) && refName(items) == "":
		return childPath + "[]", items
	case hasProperties(values) && refName(values) == "":
		return childPath + ".*", values
	}
	return "", nil
}

// hasProperties reports whether a schema is an object with declared
// fields: the shape that earns a section of its own.
func hasProperties(schema *node) bool {
	properties := schema.member("properties")
	return properties != nil && len(properties.members) > 0
}

// isMap reports whether a schema is an object used as a map.
func isMap(schema *node) bool {
	values := schema.member("additionalProperties")
	return values != nil && values.kind == objectNode
}

// cellText renders one schema's table cell: the description, then
// the machine-checkable facts beside it, folded onto one line and
// with the pipes escaped so the Markdown table survives.
func cellText(schema *node) string {
	text := strings.TrimSpace(foldText(schema.member("description").value()) + " " + facts(schema))
	return strings.ReplaceAll(text, "|", `\|`)
}

// facts renders what a schema states about its own values: the
// values it accepts, the value it takes when a caller states none,
// and the expression a string must match.
func facts(schema *node) string {
	var parts []string
	if values := schema.member("enum").elements(); len(values) > 0 {
		names := make([]string, len(values))
		for i, value := range values {
			names[i] = "`" + value.value() + "`"
		}
		parts = append(parts, "One of: "+strings.Join(names, ", ")+".")
	}
	if value := schema.member("default").value(); value != "" {
		parts = append(parts, "Default: `"+value+"`.")
	}
	if pattern := schema.member("pattern").value(); pattern != "" {
		parts = append(parts, "Pattern: `"+pattern+"`.")
	}
	return strings.Join(parts, " ")
}

// yesNo renders a required column.
func yesNo(required bool) string {
	if required {
		return "yes"
	}
	return "no"
}

// foldText collapses text onto one line, because a Markdown table
// cell cannot hold a line break.
func foldText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
