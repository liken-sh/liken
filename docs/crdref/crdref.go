// The walker: from a CRD manifest to a Markdown reference page.
//
// The CRD schemas are the authority on liken's API. Every field
// already has a description in the schema itself, because the
// schema is written to be read. This program arranges those
// descriptions into a page, so the website's reference can never
// drift from what the API server actually enforces. When a field
// changes, the page changes with it on the next build.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/liken-sh/liken/docs/linkcheck"
)

// Options are the per-page choices the command line makes. Kind
// names which CRD in the file to render, and matters only when the
// file holds more than one. Preamble opens the page and Postamble
// closes it, both hand-written files that land verbatim, because
// the schema cannot hold that prose. Title and Weight set the two
// front matter values a repository orders its pages with. The zero
// values keep today's pages: an empty Title is the kind, and a zero
// Weight is defaultWeight.
type Options struct {
	Kind      string
	Title     string
	Weight    int
	Preamble  []byte
	Postamble []byte
}

// defaultWeight puts a generated page ahead of the hand-written
// reference pages in its section listing. A repository with several
// generated pages passes -weight to order them against each other.
const defaultWeight = 10

// Generate renders one CRD manifest as a Markdown page with Hugo
// front matter. The source path appears in a comment so a reader of
// the page knows where the words come from. The preamble is optional
// hand-written prose that opens the page: the schema's descriptions
// document the fields, but only a person can say how the page relates
// to the guides, so that paragraph is in a file beside this
// program and lands here verbatim.
//
// The postamble follows the generated tables the way the preamble
// precedes them, for hand-written sections that belong under the
// field reference.
//
// The walk uses yaml.v3 nodes rather than decoded maps, because
// nodes preserve the document's field order. The CRDs declare their
// fields in a deliberate teaching order, and the page keeps it.
func Generate(crdYAML []byte, source string, opts Options) ([]byte, error) {
	root, err := selectCRD(crdYAML, source, opts.Kind)
	if err != nil {
		return nil, err
	}

	spec := mapGet(root, "spec")
	kind := scalar(mapGet(mapGet(spec, "names"), "kind"))
	if kind == "" {
		return nil, fmt.Errorf("%s names no kind", source)
	}

	// liken serves one version of each CRD, so the first entry is
	// the schema. A second served version would mean choosing which
	// one the manual documents, and this lookup would grow to make
	// that choice.
	versions := mapGet(spec, "versions")
	if versions == nil || len(versions.Content) == 0 {
		return nil, fmt.Errorf("%s declares no versions", source)
	}
	schema := mapGet(mapGet(versions.Content[0], "schema"), "openAPIV3Schema")
	if schema == nil {
		return nil, fmt.Errorf("%s has no openAPIV3Schema", source)
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
	// The default weight puts the generated pages ahead of the
	// hand-written reference pages in the section listing, and a
	// repository with several generated pages orders them with its
	// own weights. Ties fall back to the title, which is the kind
	// unless Title replaces it. toc asks the page template for an
	// "On this page" table of contents, which these long pages need
	// and short pages would not.
	fmt.Fprintf(&b, "---\ntitle: %s\nweight: %d\ntoc: true\n---\n\n", title, weight)
	fmt.Fprintf(&b, "<!-- Generated from %s by docs/crdref. Do not edit. -->\n\n", displayPath(source))
	if p := strings.Trim(string(opts.Preamble), "\n"); p != "" {
		b.WriteString(p + "\n\n")
	}
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
	if p := strings.Trim(string(opts.Postamble), "\n"); p != "" {
		b.WriteString(p + "\n\n")
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n"), nil
}

// selectCRD picks one CustomResourceDefinition out of a YAML
// stream, because a repository may ship all of its CRDs in one
// file. It matches on spec.names.kind and skips every document that
// is not a CRD. An empty want takes the only CRD in the file and
// refuses a file that holds several, so a page can never quietly
// change which resource it documents. Every refusal names the kinds
// the file holds, because that list is what the caller needs to
// write the flag.
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
		return nil, fmt.Errorf("%s holds no YAML document", source)
	case len(kinds) == 0 && len(others) == 0:
		return nil, fmt.Errorf("%s holds no CustomResourceDefinition", source)
	case len(kinds) == 0:
		return nil, fmt.Errorf("%s holds no CustomResourceDefinition, only %s",
			source, strings.Join(others, ", "))
	case want == "" && len(kinds) > 1:
		return nil, fmt.Errorf("%s holds %d CustomResourceDefinitions (%s), so -kind must name one",
			source, len(kinds), strings.Join(kinds, ", "))
	case len(found) == 0:
		return nil, fmt.Errorf("%s holds no CustomResourceDefinition for kind %s, only %s",
			source, want, strings.Join(kinds, ", "))
	}
	return found[0], nil
}

// emitSection writes one object's heading, its description, a table
// of its direct fields, and then, depth first and in declared order,
// a section for each field that is itself an object. Every heading
// has the object's full dotted path, so every path is searchable
// text. The heading level follows the depth, capped at four, so the
// nesting reads as nesting. The cap stops the deepest paths from
// becoming too small to read.
//
// intro supplies the text when the node has no description of its
// own: an array's description is on the array field, and the section
// that describes one element should still have it.
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
	// The link and the heading must agree on the heading's id, and
	// linkcheck owns the one implementation of Hugo's id algorithm,
	// so the manual's link check and these links cannot drift apart.
	return fmt.Sprintf("[%s](#%s)", escaped, linkcheck.Anchor(target))
}

// rowAnchor gives one field's table row its own id: the section's
// anchor, two hyphens, then the field's, as in spec--features. A
// field with no section of its own is still a row, so this is the id
// that lets a link land on exactly one field. The two hyphens keep
// row ids apart from heading ids, because a heading id here comes
// from a dotted path and never contains a hyphen.
func rowAnchor(path, name string) string {
	return linkcheck.Anchor(path) + "--" + linkcheck.Anchor(name)
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
// machine-checkable facts the schema also holds (the enum, the
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

// displayPath strips the ../ prefixes a Makefile invocation adds,
// so the generated comment names the file by its path in the
// repository, which is the name a reader can find.
func displayPath(p string) string {
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	return p
}
