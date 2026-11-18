// crdref generates a manual's reference page for one CRD from its
// schema. Each component's docs module declares it as a tool, and the
// docs Makefile runs it once for each CRD:
//
//	go tool crdref [-kind <Kind>] [-title <title>] [-weight <n>] [-postamble <file>] <crd.yaml> <out.md> [preamble.md]
//
// The page goes into the Hugo content tree and is committed. The
// Makefile writes it again when the schema or this program changes.
// A manifest file can contain several CRDs, so -kind selects one.
// -title sets the page title, which is the kind by default. -weight
// orders the page in its section. -postamble appends a hand-written
// file after the field tables.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// usage is the -h help text and the error for a wrong number of
// arguments.
const usage = "usage: crdref [-kind <Kind>] [-title <title>] [-weight <n>] " +
	"[-postamble <file>] <crd.yaml> <out.md> [preamble.md]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "crdref: %v\n", err)
		os.Exit(1)
	}
}

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
		// flags.Usage has printed the help text, so -h succeeds.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	args = flags.Args()
	if len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("expected 2 or 3 arguments, got %d\n%s", len(args), usage)
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
