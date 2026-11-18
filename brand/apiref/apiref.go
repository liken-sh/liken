// apiref turns an OpenAPI 3.1 document into a Markdown reference page.
//
// An operator generates its OpenAPI document from the router it
// serves, so a page generated from that document lists the routes the
// operator serves, with the descriptions written in its code. The page
// is plain Markdown, so it needs no JavaScript and the site's link
// check can read every link and heading on it.

package main

import (
	"fmt"
	"strings"
)

// Options are the per-page settings from the command line. Preamble
// and Postamble are hand-written Markdown that Generate copies onto
// the page, the preamble under the title and the postamble at the end.
// Title and Weight set the front matter.
// An empty Title uses the document's info.title, and a zero Weight uses
// defaultWeight.
type Options struct {
	Title     string
	Weight    int
	Preamble  []byte
	Postamble []byte
}

// defaultWeight is the front matter weight when the caller sets none.
// Hugo lists the pages of a section in ascending weight.
const defaultWeight = 10

// Generate renders one OpenAPI document as a Markdown page with Hugo
// front matter. An HTML comment on the page names source, so a reader
// can find the file the page was generated from.
//
// After the title, the page has the preamble, the document's summary
// and description, one section for each operation, the schemas, the
// security schemes, and the postamble.
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
	// toc: true makes the page template add an "On this page" list of
	// the headings, because these pages are long.
	fmt.Fprintf(&b, "---\ntitle: %s\nweight: %d\ntoc: true\n---\n\n", title, weight)
	fmt.Fprintf(&b, "<!-- Generated from %s by apiref. Do not edit. -->\n\n", displayPath(source))
	// The page template does not print the title as a heading.
	fmt.Fprintf(&b, "# %s\n\n", title)
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

// emitInfo writes the title, the API version, the OpenAPI version, and
// the document's summary and description. The API version tells the
// reader which build the page describes, because a release and a
// development build serve different versions.
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

// displayPath removes the leading ../ segments that a docs Makefile
// puts in the path of a document outside its docs directory.
func displayPath(p string) string {
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	return p
}
