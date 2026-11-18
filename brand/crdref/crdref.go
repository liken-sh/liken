// crdref renders a CustomResourceDefinition manifest as a Markdown
// reference page for the website.
//
// The page is built from the descriptions in the CRD's own schema, so
// it documents the same fields that the API server validates.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// Options holds the settings of one page. Kind is needed only when
// the manifest contains more than one CRD. Generate copies Preamble and
// Postamble, hand-written Markdown, unchanged before and after the
// generated tables. An empty Title means the kind, and a zero Weight
// means defaultWeight.
type Options struct {
	Kind      string
	Title     string
	Weight    int
	Preamble  []byte
	Postamble []byte
}

// defaultWeight is the front matter weight of a page that sets none.
// A section with several generated pages orders them with -weight.
const defaultWeight = 10

// Generate renders one CRD from crdYAML as a Markdown page with Hugo
// front matter. source is the path of the manifest, which the page
// names in an HTML comment.
//
// Generate reads yaml.v3 nodes instead of decoded maps, because a Go
// map loses the order of the fields, and the page lists the fields in
// the order the CRD declares them.
func Generate(crdYAML []byte, source string, opts Options) ([]byte, error) {
	root, err := selectCRD(crdYAML, source, opts.Kind)
	if err != nil {
		return nil, err
	}

	spec := mapGet(root, "spec")
	kind := scalar(mapGet(mapGet(spec, "names"), "kind"))
	if kind == "" {
		return nil, fmt.Errorf("%s has a CustomResourceDefinition with no spec.names.kind", source)
	}

	// The page documents the schema of the first version only.
	versions := mapGet(spec, "versions")
	if versions == nil || len(versions.Content) == 0 {
		return nil, fmt.Errorf("%s has a CustomResourceDefinition with no spec.versions", source)
	}
	schema := mapGet(mapGet(versions.Content[0], "schema"), "openAPIV3Schema")
	if schema == nil {
		return nil, fmt.Errorf("%s has no openAPIV3Schema in its first version", source)
	}

	title := opts.Title
	if title == "" {
		title = kind
	}
	weight := opts.Weight
	if weight == 0 {
		weight = defaultWeight
	}

	var b strings.Builder
	// toc: true makes the page template write an "On this page" list
	// of the headings.
	fmt.Fprintf(&b, "---\ntitle: %s\nweight: %d\ntoc: true\n---\n\n", title, weight)
	fmt.Fprintf(&b, "<!-- Generated from %s by crdref. Do not edit. -->\n\n", displayPath(source))
	// The page template writes no heading, so Generate writes one, with
	// the kind in the code face.
	fmt.Fprintf(&b, "# `%s`\n\n", kind)
	if p := strings.Trim(string(opts.Preamble), "\n"); p != "" {
		b.WriteString(p + "\n\n")
	}
	if d := foldText(scalar(mapGet(schema, "description"))); d != "" {
		b.WriteString(d + "\n\n")
	}
	forEachField(mapGet(schema, "properties"), func(name string, field *yaml.Node) {
		// Kubernetes defines apiVersion, kind, and metadata for every
		// resource, so the page documents only the fields of this API.
		if name == "apiVersion" || name == "kind" || name == "metadata" {
			return
		}
		emitSection(&b, name, field, 1, "")
	})
	if p := strings.Trim(string(opts.Postamble), "\n"); p != "" {
		b.WriteString(p + "\n\n")
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n"), nil
}

// selectCRD returns the CustomResourceDefinition whose
// spec.names.kind equals want, from a YAML stream that can contain
// several CRDs and other objects. When want is empty and the stream
// contains more than one CRD, selectCRD fails instead of choosing one, so
// a new CRD in a manifest cannot change which kind a page documents.
// Its errors list the kinds in the stream, for the -kind flag.
func selectCRD(crdYAML []byte, source, want string) (*yaml.Node, error) {
	var documents int
	var kinds, others []string
	var found []*yaml.Node

	decoder := yaml.NewDecoder(bytes.NewReader(crdYAML))
	for {
		var doc yaml.Node
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(doc.Content) == 0 {
			continue
		}
		documents++
		root := doc.Content[0]
		objectKind := scalar(mapGet(root, "kind"))
		if objectKind != "CustomResourceDefinition" {
			if objectKind != "" {
				others = append(others, objectKind)
			}
			continue
		}
		kind := scalar(mapGet(mapGet(mapGet(root, "spec"), "names"), "kind"))
		kinds = append(kinds, kind)
		if want == "" || want == kind {
			found = append(found, root)
		}
	}

	switch {
	case documents == 0:
		return nil, fmt.Errorf("%s has no YAML document", source)
	case len(kinds) == 0 && len(others) == 0:
		return nil, fmt.Errorf("%s has no CustomResourceDefinition", source)
	case len(kinds) == 0:
		return nil, fmt.Errorf("%s has no CustomResourceDefinition, only %s",
			source, strings.Join(others, ", "))
	case want == "" && len(kinds) > 1:
		return nil, fmt.Errorf("%s has %d CustomResourceDefinitions (%s), so -kind must name one",
			source, len(kinds), strings.Join(kinds, ", "))
	case len(found) == 0:
		return nil, fmt.Errorf("%s has no CustomResourceDefinition of kind %s, only %s",
			source, want, strings.Join(kinds, ", "))
	}
	return found[0], nil
}

// emitSection writes a heading with the object's dotted path, its
// description, and a table of its fields, then a section for each
// field that is an object, depth first. The heading level is depth+1,
// capped at 4, so a deeply nested path does not render in tiny type.
//
// intro is the description to use when node has none, because the
// description of an array or a map is on the field, not on its items
// or values.
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
		fmt.Fprintf(b, "| <span id=%q></span>`%s` | %s | %s | %s |\n",
			rowAnchor(path, name), name, typeCell(field, path+"."+name), yesno, cellText(field))
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

// childSection returns the path and the schema of the section that a
// field gets: the field itself, an array's items with a path ending in
// [], or a map's values with a path ending in .*. It returns "" and nil
// for a field with no object schema.
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

// typeCell renders a field's type, linked to the field's own section
// when it has one. The brackets of a type such as []object are escaped
// in a link, so Markdown does not read them as part of the link.
func typeCell(field *yaml.Node, childPath string) string {
	t := fieldType(field)
	target, child := childSection(field, childPath)
	if child == nil {
		return t
	}
	escaped := strings.NewReplacer("[", `\[`, "]", `\]`).Replace(t)
	// linkcheck.Anchor computes the heading id the way Hugo does, and
	// the manual's link check uses the same function.
	return fmt.Sprintf("[%s](#%s)", escaped, linkcheck.Anchor(target))
}

// rowAnchor returns the HTML id of a field's table row, such as
// spec--features, so a link can point to a field that has no section
// of its own. The two hyphens keep a row id apart from a heading id,
// because the id of a dotted path has no hyphen unless a field name
// contains one.
func rowAnchor(path, name string) string {
	return linkcheck.Anchor(path) + "--" + linkcheck.Anchor(name)
}

// fieldType renders a schema's type in Go notation, such as []string
// or map[string]object. The CRDs set additionalProperties to a schema
// only on maps, so fieldType treats every such object as a map.
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

// cellText renders a field's Description cell: the description, then
// the enum, default, and pattern. It is one line with its pipes
// escaped, so it does not break the Markdown table.
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

func hasProperties(node *yaml.Node) bool {
	props := mapGet(node, "properties")
	return props != nil && len(props.Content) > 0
}

// mapGet returns the value of key in a mapping node, or nil for a nil
// node, any other kind of node, or a missing key, so calls can be
// chained without nil checks.
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

func scalar(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

// foldText joins the lines of a description into one line, because a
// line break ends a Markdown table row. A YAML folded block is already
// one line, but a literal block keeps its line breaks.
func foldText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// displayPath removes the leading ../ segments that a Makefile adds
// to the manifest path. A docs Makefile runs from the component's
// docs/ directory, so the page names the manifest by its path in the
// component, such as deploy/crds.yaml.
func displayPath(p string) string {
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	return p
}
