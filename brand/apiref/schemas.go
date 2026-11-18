// This file renders the component schemas of an OpenAPI document as
// field tables. The columns, the dotted headings, and the row ids are
// the same as in the tables that crdref writes for a CRD, so a reader
// and a link find a field the same way on both kinds of page.

package main

import (
	"fmt"
	"strings"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// componentPrefix starts a $ref to a schema of the same document.
// OpenAPI allows a $ref to any URI, but the operators' documents refer
// only to their own schemas, so the page links only these. Any other
// $ref renders as "object" with no link.
const componentPrefix = "#/components/schemas/"

// emitSchemas writes the Schemas section, which the Type cells of the
// route tables link to, in the order the document declares the
// schemas.
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

// emitSchemaSection writes a heading, the description, and a table of
// fields for one object, then a section for each field that is an
// inline object. The heading level is depth, capped at 4 as in crdref,
// so a deeply nested path does not render in tiny type.
func emitSchemaSection(b *strings.Builder, document *node, path string, schema *node, depth int) {
	fmt.Fprintf(b, "%s %s\n\n", strings.Repeat("#", min(depth, 4)), path)
	if d := foldText(schema.member("description").value()); d != "" {
		b.WriteString(d + "\n\n")
	}

	properties := schema.member("properties")
	if properties == nil || len(properties.members) == 0 {
		// A scalar, an array, or an object with no declared properties
		// has no fields to tabulate, so describe its type in a sentence.
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
		// A field that is only a $ref has no description, so use the
		// description of the component it references.
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

// rowAnchor returns the HTML id of a field's table row, such as
// problem--instance, so a link can point to one field. The two hyphens
// keep a row id apart from a heading id, because the id of a dotted
// path has no hyphen unless a name in the path contains one.
func rowAnchor(path, name string) string {
	return linkcheck.Anchor(path) + "--" + linkcheck.Anchor(name)
}

// typeCell renders a schema's type for the Type column. When the cell
// has a link, the brackets of [] and map[string] are escaped, so
// Markdown does not read them as part of the link.
func typeCell(schema *node, path string) string {
	wrapper, leaf, leafPath := unwrap(schema, path)
	cell := leafCell(leaf, leafPath)
	if strings.Contains(cell, "](") {
		wrapper = strings.NewReplacer("[", `\[`, "]", `\]`).Replace(wrapper)
	}
	return wrapper + cell
}

// unwrap removes the array and map layers from a schema. It returns
// their notation, such as map[string][], the innermost schema, and
// that schema's path. The operators' schemas set additionalProperties
// to a schema only on maps, so isMap treats every such object as a map.
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

// leafCell renders the innermost type of a field: a link to a
// referenced component, a link to the section of an inline object,
// or the type word followed by its format.
func leafCell(schema *node, path string) string {
	if name := refName(schema); name != "" {
		return fmt.Sprintf("[%s](#%s)", name, linkcheck.Anchor(name))
	}
	// Tables outside the Schemas section pass an empty path, because
	// an inline object there has no section to link to.
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

// typeWord returns the type member. In OpenAPI 3.1, type can be a
// list such as ["string", "null"], which typeWord joins as
// "string or null".
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

// refName returns the schema name of a $ref that starts with
// componentPrefix, and "" for any other schema.
func refName(schema *node) string {
	pointer := schema.member("$ref").value()
	if !strings.HasPrefix(pointer, componentPrefix) {
		return ""
	}
	return strings.TrimPrefix(pointer, componentPrefix)
}

// resolve follows a chain of $refs inside the document and returns
// the value at the end, so a parameter or response declared under
// components renders from its declaration. It returns a $ref that it
// cannot follow unchanged. The limit of 10 hops ends a cycle of
// references.
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

// pointerTarget returns the node at a JSON pointer such as
// #/components/schemas/Problem, or nil when a step is missing. RFC 6901
// escapes "/" in a name as ~1 and "~" as ~0.
func pointerTarget(document *node, pointer string) *node {
	unescape := strings.NewReplacer("~1", "/", "~0", "~")
	for _, step := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		document = document.member(unescape.Replace(step))
	}
	return document
}

// childSection returns the path and the schema of the section that a
// field gets: the field itself, an array's items with a path ending in
// [], or a map's values with a path ending in .*. It returns "" and nil
// for a field with no inline object, and for a $ref, because the
// referenced schema has its own section.
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

func hasProperties(schema *node) bool {
	properties := schema.member("properties")
	return properties != nil && len(properties.members) > 0
}

func isMap(schema *node) bool {
	values := schema.member("additionalProperties")
	return values != nil && values.kind == objectNode
}

// cellText renders a Description cell on one line, with its pipes
// escaped, so it does not break the Markdown table.
func cellText(schema *node) string {
	text := strings.TrimSpace(foldText(schema.member("description").value()) + " " + facts(schema))
	return strings.ReplaceAll(text, "|", `\|`)
}

// facts renders the enum, default, and pattern of a schema as
// sentences.
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

func yesNo(required bool) string {
	if required {
		return "yes"
	}
	return "no"
}

// foldText joins the lines of s into one line, because a Markdown
// table cell cannot contain a line break.
func foldText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
