// The routes: one section for each operation the document declares.
//
// A section is one method on one path, because that is the unit the
// document describes and the unit a caller writes: the parameters, the
// answers, and the fields of a GET are not the ones of an OPTIONS on
// the same path. The heading is the method and the path together, so
// the text a reader searches for is the text they would type into
// curl.
package main

import (
	"fmt"
	"strings"
)

// methods are the operations a path item may declare, in the order
// OpenAPI lists them. A path item also holds members that are not
// operations, such as parameters and summary, so a section is
// written only for a member in this set.
var methods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// emitPaths writes a section for every operation, with the paths in
// the document's order and the methods in each path item's order.
func emitPaths(b *strings.Builder, document *node, paths *node) {
	paths.each(func(path string, item *node) {
		item.each(func(name string, operation *node) {
			if !methods[name] {
				return
			}
			emitOperation(b, document, strings.ToUpper(name), path, item, operation)
		})
	})
}

// emitOperation writes one section. The heading holds the method and
// the path in the code face, and an attribute with the method, which
// the theme draws the badge from. Goldmark strips the attribute
// before it computes the heading's id, and the code marks render as
// nothing, so the id is the one a plain "GET /v1/thing" heading gets
// and the page's table of contents still lists the route.
func emitOperation(b *strings.Builder, document *node, method, path string, item, operation *node) {
	fmt.Fprintf(b, "## `%s` `%s` {data-method=%s}\n\n", method, path, method)
	if summary := foldText(operation.member("summary").value()); summary != "" {
		b.WriteString(summary + "\n\n")
	}
	if description := strings.TrimSpace(operation.member("description").value()); description != "" {
		b.WriteString(description + "\n\n")
	}
	// A route that states its own security stands apart from the
	// document's requirement, so its section says what it takes.
	if phrase := requirementPhrase(operation.member("security")); phrase != "" {
		fmt.Fprintf(b, "This route requires %s.\n\n", phrase)
	}
	emitParameters(b, document, item, operation)
	emitRequestBody(b, document, operation.member("requestBody"))
	responses := operation.member("responses")
	emitResponses(b, document, responses)
	emitResponseHeaders(b, document, responses)
	emitResponseLinks(b, document, responses)
}

// emitParameters writes the parameters of one operation: the ones
// the path item declares for every method, then the ones the
// operation declares for itself. OpenAPI says an operation's
// parameter replaces the path item's parameter of the same name and
// place, which is what the replacement below does.
func emitParameters(b *strings.Builder, document *node, item, operation *node) {
	var merged []*node
	add := func(parameter *node) {
		parameter = resolve(document, parameter)
		for i, existing := range merged {
			if existing.member("name").value() == parameter.member("name").value() &&
				existing.member("in").value() == parameter.member("in").value() {
				merged[i] = parameter
				return
			}
		}
		merged = append(merged, parameter)
	}
	for _, parameter := range item.member("parameters").elements() {
		add(parameter)
	}
	for _, parameter := range operation.member("parameters").elements() {
		add(parameter)
	}
	if len(merged) == 0 {
		return
	}

	b.WriteString("**Parameters**\n\n")
	b.WriteString("| Parameter | In | Required | Type | Description |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, parameter := range merged {
		schema := parameter.member("schema")
		// A parameter carries its own description, and the facts the
		// schema holds, the enum and the default, belong in the same
		// cell.
		text := strings.TrimSpace(cellText(parameter) + " " + cellText(schema))
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n",
			parameter.member("name").value(),
			parameter.member("in").value(),
			yesNo(parameter.member("required").value() == "true"),
			typeCell(schema, ""),
			text)
	}
	b.WriteString("\n")
}

// emitRequestBody writes what an operation takes. The description is
// a paragraph rather than a cell, because it describes the body and
// not one of its forms.
func emitRequestBody(b *strings.Builder, document *node, body *node) {
	body = resolve(document, body)
	content := body.member("content")
	if content == nil {
		return
	}
	b.WriteString("**Request body**\n\n")
	if description := strings.TrimSpace(body.member("description").value()); description != "" {
		b.WriteString(description + "\n\n")
	}
	if body.member("required").value() == "true" {
		b.WriteString("The request body is required.\n\n")
	} else {
		b.WriteString("The request body is optional.\n\n")
	}
	b.WriteString("| Media type | Schema |\n")
	b.WriteString("| --- | --- |\n")
	content.each(func(mediaType string, entry *node) {
		fmt.Fprintf(b, "| `%s` | %s |\n", mediaType, typeCell(entry.member("schema"), ""))
	})
	b.WriteString("\n")
}

// emitResponses writes what an operation answers, one row per media
// type, in the document's order. A document whose responses carry no
// content, because every body is a live capture or a document the
// manual shows, gets the two columns that hold its facts.
func emitResponses(b *strings.Builder, document *node, responses *node) {
	if responses == nil || len(responses.members) == 0 {
		return
	}
	b.WriteString("**Answers**\n\n")
	if !anyResponseHasContent(document, responses) {
		b.WriteString("| Status | Description |\n")
		b.WriteString("| --- | --- |\n")
		responses.each(func(status string, response *node) {
			fmt.Fprintf(b, "| %s | %s |\n", status, cellText(resolve(document, response)))
		})
		b.WriteString("\n")
		return
	}

	b.WriteString("| Status | Media type | Schema | Description |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	responses.each(func(status string, response *node) {
		response = resolve(document, response)
		content := response.member("content")
		if content == nil || len(content.members) == 0 {
			fmt.Fprintf(b, "| %s | none | | %s |\n", status, cellText(response))
			return
		}
		content.each(func(mediaType string, entry *node) {
			fmt.Fprintf(b, "| %s | `%s` | %s | %s |\n",
				status, mediaType, typeCell(entry.member("schema"), ""), cellText(response))
		})
	})
	b.WriteString("\n")
}

func anyResponseHasContent(document *node, responses *node) bool {
	found := false
	responses.each(func(_ string, response *node) {
		if resolve(document, response).member("content") != nil {
			found = true
		}
	})
	return found
}

// emitResponseHeaders writes the fields the answers carry. The table
// has no type column: every header value is text, and the grammar a
// field follows is in the field's own description.
func emitResponseHeaders(b *strings.Builder, document *node, responses *node) {
	var rows []string
	responses.each(func(status string, response *node) {
		resolve(document, response).member("headers").each(func(name string, header *node) {
			header = resolve(document, header)
			rows = append(rows, fmt.Sprintf("| `%s` | %s | %s |\n", name, status, cellText(header)))
		})
	})
	if len(rows) == 0 {
		return
	}
	b.WriteString("**Headers**\n\n")
	b.WriteString("| Header | Status | Description |\n")
	b.WriteString("| --- | --- | --- |\n")
	b.WriteString(strings.Join(rows, ""))
	b.WriteString("\n")
}

// emitResponseLinks writes the link relations an answer declares:
// the other operation a caller reaches from this one, which OpenAPI
// names by operationId or by a reference to it.
func emitResponseLinks(b *strings.Builder, document *node, responses *node) {
	var rows []string
	responses.each(func(status string, response *node) {
		resolve(document, response).member("links").each(func(name string, link *node) {
			link = resolve(document, link)
			target := link.member("operationId").value()
			if target == "" {
				target = link.member("operationRef").value()
			}
			rows = append(rows, fmt.Sprintf("| `%s` | %s | `%s` | %s |\n", name, status, target, cellText(link)))
		})
	})
	if len(rows) == 0 {
		return
	}
	b.WriteString("**Link relations**\n\n")
	b.WriteString("| Link | Status | Operation | Description |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	b.WriteString(strings.Join(rows, ""))
	b.WriteString("\n")
}
