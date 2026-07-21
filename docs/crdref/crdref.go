// The walker: from a CRD manifest to a Markdown reference page.
//
// The CRD schemas are the authority on liken's API. Every field
// already carries a description in the schema itself, because the
// schema is written to be read. This program arranges those
// descriptions into a page, so the website's reference can never
// drift from what the API server actually enforces. When a field
// changes, the page changes with it on the next build.
package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Generate renders one CRD manifest as a Markdown page with Hugo
// front matter. The source path appears in a comment so a reader of
// the page knows where the words come from.
//
// The walk uses yaml.v3 nodes rather than decoded maps, because
// nodes preserve the document's field order. The CRDs declare their
// fields in a deliberate teaching order, and the page keeps it.
func Generate(crdYAML []byte, source string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(crdYAML, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s holds no YAML document", source)
	}
	root := doc.Content[0]
	if kind := scalar(mapGet(root, "kind")); kind != "CustomResourceDefinition" {
		return nil, fmt.Errorf("%s is a %s, not a CustomResourceDefinition", source, kind)
	}

	spec := mapGet(root, "spec")
	kind := scalar(mapGet(mapGet(spec, "names"), "kind"))
	if kind == "" {
		return nil, fmt.Errorf("%s names no kind", source)
	}

	// liken serves one version of each CRD, so the first entry is
	// the schema. A second served version would mean choosing which
	// one the manual documents, and that day this lookup grows.
	versions := mapGet(spec, "versions")
	if versions == nil || len(versions.Content) == 0 {
		return nil, fmt.Errorf("%s declares no versions", source)
	}
	schema := mapGet(mapGet(versions.Content[0], "schema"), "openAPIV3Schema")
	if schema == nil {
		return nil, fmt.Errorf("%s carries no openAPIV3Schema", source)
	}

	var b strings.Builder
	// The weight puts the generated pages ahead of the hand-written
	// reference pages in the section listing; ties fall back to the
	// title, which is the kind. toc asks the page template for an
	// "On this page" table of contents, which these long pages need
	// and short pages would not.
	fmt.Fprintf(&b, "---\ntitle: %s\nweight: 10\ntoc: true\n---\n\n", kind)
	fmt.Fprintf(&b, "<!-- Generated from %s by docs/crdref. Do not edit. -->\n\n", displayPath(source))
	if d := foldText(scalar(mapGet(schema, "description"))); d != "" {
		b.WriteString(d + "\n\n")
	}
	forEachField(mapGet(schema, "properties"), func(name string, field *yaml.Node) {
		// The object grammar (apiVersion, kind, metadata) belongs to
		// Kubernetes, not to this API, so the page starts at spec.
		if name == "apiVersion" || name == "kind" || name == "metadata" {
			return
		}
		emitSection(&b, name, field, 1, "")
	})
	return []byte(strings.TrimRight(b.String(), "\n") + "\n"), nil
}

// emitSection writes one object's heading, its description, a table
// of its direct fields, and then, depth first and in declared order,
// a section for each field that is itself an object. Every heading
// carries the object's full dotted path, so every path is searchable
// text. The heading level follows the depth, capped at four, so the
// nesting reads as nesting; the cap keeps the deepest paths from
// vanishing into fine print.
//
// intro stands in when the node has no description of its own: an
// array's description lives on the array field, and the section that
// describes one element should still carry it.
func emitSection(b *strings.Builder, path string, node *yaml.Node, depth int, intro string) {
	heading := strings.Repeat("#", min(depth+1, 4))
	fmt.Fprintf(b, "%s %s\n\n", heading, path)
	if d := foldText(scalar(mapGet(node, "description"))); d != "" {
		intro = d
	}
	if intro != "" {
		b.WriteString(intro + "\n\n")
	}

	props := mapGet(node, "properties")
	if props == nil || len(props.Content) == 0 {
		return
	}

	required := map[string]bool{}
	if req := mapGet(node, "required"); req != nil {
		for _, r := range req.Content {
			required[r.Value] = true
		}
	}

	b.WriteString("| Field | Type | Required | Description |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	forEachField(props, func(name string, field *yaml.Node) {
		yesno := "no"
		if required[name] {
			yesno = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", name, typeCell(field, path+"."+name), yesno, cellText(field))
	})
	b.WriteString("\n")

	forEachField(props, func(name string, field *yaml.Node) {
		childPath, child := childSection(field, path+"."+name)
		if child == nil {
			return
		}
		emitSection(b, childPath, child, depth+1, foldText(scalar(mapGet(field, "description"))))
	})
}

// childSection finds the object a field's own section would
// describe: the field itself, one element of it, or one value of it.
// The path suffix says which: [] for an array's element, .* for a
// map's value under any key. A field with no object beneath it
// returns nil, and gets no section.
func childSection(field *yaml.Node, childPath string) (string, *yaml.Node) {
	items := mapGet(field, "items")
	values := mapGet(field, "additionalProperties")
	switch {
	case hasProperties(field):
		return childPath, field
	case hasProperties(items):
		return childPath + "[]", items
	case hasProperties(values):
		return childPath + ".*", values
	}
	return "", nil
}

// typeCell renders a field's type for the table. When the field has
// its own section further down the page, the type becomes a link to
// it, so a reader lands on the definition instead of scanning for
// it. The brackets in a type like []object are escaped, because they
// would otherwise read as part of the link's own syntax.
func typeCell(field *yaml.Node, childPath string) string {
	t := fieldType(field)
	target, child := childSection(field, childPath)
	if child == nil {
		return t
	}
	escaped := strings.NewReplacer("[", `\[`, "]", `\]`).Replace(t)
	return fmt.Sprintf("[%s](#%s)", escaped, anchorFor(target))
}

// anchorFor turns a section's path into the id Hugo gives its
// heading: lowercased, with everything but letters and digits
// dropped. The link and the heading must agree on this, and the
// heading's side is Goldmark's GitHub-style autoHeadingID.
func anchorFor(path string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(path) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fieldType renders a schema's type the way a Go reader expects:
// []string for an array of strings, map[string]object for an object
// used as a map. The CRDs use additionalProperties exactly when a
// field is a map, so its presence is the map test.
func fieldType(node *yaml.Node) string {
	if node == nil {
		return "object"
	}
	switch scalar(mapGet(node, "type")) {
	case "array":
		return "[]" + fieldType(mapGet(node, "items"))
	case "object", "":
		if values := mapGet(node, "additionalProperties"); values != nil && values.Kind == yaml.MappingNode {
			return "map[string]" + fieldType(values)
		}
		return "object"
	default:
		return scalar(mapGet(node, "type"))
	}
}

// cellText renders one field's table cell: the description, then the
// machine-checkable facts the schema also carries (the enum, the
// default, the pattern), folded onto one line and with pipes escaped
// so the Markdown table survives.
func cellText(node *yaml.Node) string {
	var parts []string
	if d := foldText(scalar(mapGet(node, "description"))); d != "" {
		parts = append(parts, d)
	}
	if enum := mapGet(node, "enum"); enum != nil && enum.Kind == yaml.SequenceNode {
		values := make([]string, len(enum.Content))
		for i, v := range enum.Content {
			values[i] = "`" + v.Value + "`"
		}
		parts = append(parts, "One of: "+strings.Join(values, ", ")+".")
	}
	if def := mapGet(node, "default"); def != nil && def.Kind == yaml.ScalarNode {
		parts = append(parts, "Default: `"+def.Value+"`.")
	}
	if p := scalar(mapGet(node, "pattern")); p != "" {
		parts = append(parts, "Pattern: `"+p+"`.")
	}
	return strings.ReplaceAll(strings.Join(parts, " "), "|", `\|`)
}

// hasProperties reports whether a schema node is an object with
// declared fields: the shape that earns its own section.
func hasProperties(node *yaml.Node) bool {
	props := mapGet(node, "properties")
	return props != nil && len(props.Content) > 0
}

// mapGet finds one key's value in a mapping node. A nil node or a
// missing key returns nil, so lookups chain without checks.
func mapGet(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// forEachField visits a mapping's pairs in document order.
func forEachField(node *yaml.Node, visit func(name string, value *yaml.Node)) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		visit(node.Content[i].Value, node.Content[i+1])
	}
}

// scalar returns a scalar node's value, or "" for anything else.
func scalar(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

// foldText collapses a description onto one line. The schemas write
// descriptions as folded blocks, and YAML already joins those lines,
// but plain multi-line strings keep their breaks, and a Markdown
// table cell cannot hold one.
func foldText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// displayPath strips the ../ prefixes a Makefile invocation carries,
// so the generated comment names the file by its path in the
// repository, which is the name a reader can find.
func displayPath(p string) string {
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	return p
}
