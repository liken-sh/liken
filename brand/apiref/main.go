// apiref generates a manual's route reference page.
//
// The docs Makefile runs it once per API:
//
//	go run ./apiref [-title <title>] [-weight <n>] [-postamble <file>] <openapi.json> <out.md> [preamble.md]
//
// The output lands in the Hugo content tree beside the hand-written
// page that tells the API's story. The document is the source of
// truth, and the page is a build product, regenerated whenever the
// document or this program changes.
//
// The liken repositories run this program with `go tool apiref`,
// pinned as a tool dependency of each docs module, the way they pin
// crdref and Hugo. Their reference sections already hold
// hand-written pages, so -title and -weight name and order this one.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// usage is both the -h help text and the error a wrong argument
// count returns, so one line stays true for both.
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
	postamble := flags.String("postamble", "", "append this file after the generated tables")
	title := flags.String("title", "", "the front matter title, in place of the document's own")
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
