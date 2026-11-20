// apiref writes a manual's HTTP route reference page, as Markdown for
// Hugo, from an OpenAPI document. A manual's docs Makefile runs it as
// `go tool apiref`, which the docs module's go.mod declares as a tool:
//
//	go tool apiref [-title <title>] [-weight <n>] [-postamble <file>] <openapi.json> <out.md> [preamble.md]
//
// Do not edit the output by hand. Regenerate it when the OpenAPI
// document or this program changes. The page shares its reference
// section with other pages, so -title and -weight set its title and
// its place in the section's order.
//
// The page starts with the document's own summary, then has one
// section for each operation, headed with the method and the path.
// Each section lists the parameters, the responses, and the fields of
// each response. After the operations come the component schemas, as
// the same field tables that crdref writes for a CRD, and the
// credentials that the document declares. The page is plain Markdown
// with no Swagger UI and no JavaScript, and every section is a heading
// that the manual's link check can resolve.
//
// A route's heading carries the method as an attribute:
//
//	## `GET` `/v1/widget/widgets/{name}` {data-method=GET}
//
// The theme draws the method as a badge from the attribute. Goldmark
// removes the attribute before it computes the heading's id, and
// backticks add nothing to an id, so the id is the one of the plain
// heading "GET /v1/widget/widgets/{name}", and Hugo's table of contents
// lists the route. A theme that draws no badge shows the method and
// the path.
//
// The preamble and the postamble are hand-written files that the
// program copies unchanged to the top and the bottom of the page. They
// hold what an OpenAPI document has no place for, such as what the API
// is for and a link to the manual page that explains it.
// plans/completed/02-openapi-as-a-page.md in this component is the
// design.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// usage is the -h help text and also ends the error for a wrong number
// of arguments, so the two cannot differ.
const usage = "usage: apiref [-title <title>] [-weight <n>] " +
	"[-postamble <file>] <openapi.json> <out.md> [preamble.md]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "apiref: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("apiref", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), usage)
		flags.PrintDefaults()
	}
	postamble := flags.String("postamble", "", "append this file after the generated sections")
	title := flags.String("title", "", "the front matter title, in place of the document's own")
	weight := flags.Int("weight", defaultWeight, "the front matter weight, which orders the page in its section")
	if err := flags.Parse(args); err != nil {
		// flags.Usage has printed the help for -h, which is not an error.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	args = flags.Args()
	if len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("expected 2 or 3 arguments, got %d\n%s", len(args), usage)
	}
	openAPIJSON, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	opts := Options{Title: *title, Weight: *weight}
	if len(args) == 3 {
		if opts.Preamble, err = os.ReadFile(args[2]); err != nil {
			return err
		}
	}
	if *postamble != "" {
		if opts.Postamble, err = os.ReadFile(*postamble); err != nil {
			return err
		}
	}
	page, err := Generate(openAPIJSON, args[0], opts)
	if err != nil {
		return err
	}
	return os.WriteFile(args[1], page, 0o644)
}
