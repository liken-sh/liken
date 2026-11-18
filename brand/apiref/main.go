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
