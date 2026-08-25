// crdref generates the manual's CRD reference pages.
//
// The docs Makefile runs it once per CRD:
//
//	go run ./crdref [-kind <Kind>] [-title <title>] [-weight <n>] [-postamble <file>] <crd.yaml> <out.md> [preamble.md]
//
// The output lands in the Hugo content tree and is gitignored: the
// schemas are the source of truth, and the pages are build products,
// regenerated whenever a schema or this program changes.
//
// Other liken repositories run this program with
// `go run github.com/liken-sh/liken/docs/crdref`. They ship several
// CRDs in one manifest file, so -kind names the one a page
// documents, and -title and -weight name and order the page. Their
// reference pages carry hand-written sections under the field
// tables, which -postamble supplies. Every flag defaults to what
// this repository's own pages already have, so this repository's
// Makefile rules pass none of them.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// usage is both the -h help text and the error a wrong argument
// count returns, so one line stays true for both.
const usage = "usage: crdref [-kind <Kind>] [-title <title>] [-weight <n>] " +
	"[-postamble <file>] <crd.yaml> <out.md> [preamble.md]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "crdref: %v\n", err)
		os.Exit(1)
	}
}

// The flags come before the positional arguments, and the
// positional form is unchanged, so the Makefile rules that predate
// the flags run as they are.
func run(args []string) error {
	flags := flag.NewFlagSet("crdref", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), usage)
		flags.PrintDefaults()
	}
	kind := flags.String("kind", "", "render the CustomResourceDefinition with this spec.names.kind")
	postamble := flags.String("postamble", "", "append this file after the generated tables")
	title := flags.String("title", "", "the front matter title, in place of the kind")
	weight := flags.Int("weight", defaultWeight, "the front matter weight, which orders the page in its section")
	if err := flags.Parse(args); err != nil {
		// flags.Usage wrote the help text already, so -h is done and
		// is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	args = flags.Args()
	if len(args) < 2 || len(args) > 3 {
		return errors.New(usage)
	}
	crd, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	opts := Options{Kind: *kind, Title: *title, Weight: *weight}
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
	page, err := Generate(crd, args[0], opts)
	if err != nil {
		return err
	}
	return os.WriteFile(args[1], page, 0o644)
}
