// The route sections of the page. An operation is one method on one
// path, and each operation gets its own section, because a GET and an
// OPTIONS on the same path have different parameters and responses.
// The heading is the method and the path, which is what a reader types
// into curl and searches for.

package main

import (
	"fmt"
	"strings"
)

// methods are the HTTP methods that OpenAPI allows in a path item. A
// path item also has members that are not operations, such as
// parameters and summary, and emitPaths skips them.
var methods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

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

// emitOperation writes one section. The heading sets a data-method
// attribute, which the theme's stylesheet uses to style the method as
// a badge. Goldmark removes the attribute block and the backticks
// before it computes the heading's id, so the id is the same as for a
// plain "GET /v1/thing" heading.
func emitOperation(b *strings.Builder, document *node, method, path string, item, operation *node) {
	fmt.Fprintf(b, "## `%s` `%s` {data-method=%s}\n\n", method, path, method)
	if summary := foldText(operation.member("summary").value()); summary != "" {
		b.WriteString(summary + "\n\n")
	}
	if description := strings.TrimSpace(operation.member("description").value()); description != "" {
		b.WriteString(description + "\n\n")
	}
	// The Security section gives the document-wide requirement. An
	// operation's own security field overrides it, so the section of
	// that operation states the override.
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

// emitParameters writes the parameters that the path item declares for
// every method, then those that the operation declares. As OpenAPI
// specifies, an operation's parameter replaces a path item parameter
// with the same name and location.
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
		// The schema adds its own description, enum, default, and
		// pattern to the parameter's description.
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

// emitRequestBody writes the body's description as a paragraph and
// not as a table cell, because it describes the whole body and not one
// media type.
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

// emitResponses writes one table row for each media type of each
// response. When no response of the operation declares content, the
// table has no media type and schema columns, because they would say
// "none" on every row.
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

// emitResponseHeaders writes a table with no type column, because
// every header value is text and the header's description states its
// format.
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

// emitResponseLinks writes the OpenAPI links of each response. A link
// names an operation that the caller can call next, by operationId or
// by operationRef.
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
