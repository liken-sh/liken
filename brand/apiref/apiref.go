// The walker: from an OpenAPI 3.1 document to a Markdown reference
// page.
//
// Each operator's API describes itself. The document is generated
// from the router the program serves, so every served route appears
// in it, and a description in it is written to be
// read. This program arranges those descriptions into a page, so a
// manual's route reference can never drift from the routes the API
// serves. The page is Markdown and nothing else: a reader needs no
// JavaScript, and the site's own link check reaches every section.
package main

import (
	"fmt"
	"strings"
)

// Options are the per-page choices the command line makes. Preamble
// opens the page and Postamble closes it, both hand-written files
// that land verbatim, because the document cannot hold that prose.
// Title and Weight set the two front matter values a repository
// orders its pages with. The zero values are the document's own
// title and defaultWeight.
type Options struct {
	Title     string
	Weight    int
	Preamble  []byte
	Postamble []byte
}

// defaultWeight puts a generated page ahead of the hand-written
// reference pages in its section listing. A repository passes
// -weight to place the page against the pages it already has.
const defaultWeight = 10

// Generate renders one OpenAPI document as a Markdown page with Hugo
// front matter. The source path appears in a comment so a reader of
// the page knows where the words come from.
//
// The page opens with the document's own summary and description,
// then gives one section for each operation, then the schemas the
// operations name, then the credentials. The preamble is
// hand-written prose that opens the page: the document describes the
// routes, but only a person can say how the page relates to the
// narrative beside it, so that paragraph is a file in the repository
// and lands here verbatim. The postamble is the same for prose that
// belongs under the tables.
func Generate(openAPIJSON []byte, source string, opts Options) ([]byte, error) {
	document, err := parse(openAPIJSON)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if document.member("openapi").value() == "" {
		return nil, fmt.Errorf("%s declares no openapi version, so it is not an OpenAPI document", source)
	}
	paths := document.member("paths")
	if paths == nil || len(paths.members) == 0 {
		return nil, fmt.Errorf("%s declares no paths", source)
	}

	info := document.member("info")
	title := opts.Title
	if title == "" {
		title = info.member("title").value()
	}
	weight := opts.Weight
	if weight == 0 {
		weight = defaultWeight
	}

	var b strings.Builder
	// toc asks the page template for an "On this page" table of
	// contents, which lists every route and every schema on a page
	// this long.
	fmt.Fprintf(&b, "---\ntitle: %s\nweight: %d\ntoc: true\n---\n\n", title, weight)
	fmt.Fprintf(&b, "<!-- Generated from %s by apiref. Do not edit. -->\n\n", displayPath(source))
	if preamble := strings.Trim(string(opts.Preamble), "\n"); preamble != "" {
		b.WriteString(preamble + "\n\n")
	}
	emitInfo(&b, document.member("openapi").value(), info)
	emitPaths(&b, document, paths)
	emitSchemas(&b, document)
	emitSecurity(&b, document.member("components").member("securitySchemes"), document.member("security"))
	if postamble := strings.Trim(string(opts.Postamble), "\n"); postamble != "" {
		b.WriteString(postamble + "\n\n")
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n"), nil
}

// emitInfo writes what the document says about itself. The version
// is here because a released API and a development build serve
// different ones, and a reader of the page needs to know which
// document it was made from. The summary and the description land
// verbatim: they are prose already, written for this page's reader.
func emitInfo(b *strings.Builder, release string, info *node) {
	fmt.Fprintf(b, "`%s`, version `%s`, described in OpenAPI %s.\n\n",
		info.member("title").value(), info.member("version").value(), release)
	if summary := strings.TrimSpace(info.member("summary").value()); summary != "" {
		b.WriteString(summary + "\n\n")
	}
	if description := strings.TrimSpace(info.member("description").value()); description != "" {
		b.WriteString(description + "\n\n")
	}
}

// displayPath strips the ../ prefixes a Makefile invocation adds, so
// the generated comment names the file by its path in the
// repository, which is the name a reader can find.
func displayPath(p string) string {
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	return p
}
