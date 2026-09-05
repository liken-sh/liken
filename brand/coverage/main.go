// coverage turns the coverage data a repository's tests produce into
// one self-contained HTML report.
//
// The liken repositories run it as `go tool coverage`, pinned as a
// tool dependency of the docs module:
//
//	go tool coverage -title <name> -out coverage.html [-root <dir>] [-label <name>] <input>...
//
// An input is a Go coverage profile or a Cobertura report. A
// repository with a Go operator and a Rust program hands it both at
// once, and the two come out in one page with one look. The page
// goes onto the repository's documentation site and into its
// releases, so it is one file that carries its own stylesheet.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// usage is both the -h help text and the error a wrong command line
// returns, so one line stays true for both.
const usage = "usage: coverage [-title <name>] [-label <name>] [-root <dir>] " +
	"-out <file> <input> [<input>...]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
		os.Exit(1)
	}
}

// labels collects the repeated -label flag. A Cobertura report says
// nothing about the language that wrote it, so a repository names
// its inputs itself. The names pair with the inputs in order.
type labels []string

func (l *labels) String() string { return strings.Join(*l, ", ") }

func (l *labels) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func run(args []string) error {
	flags := flag.NewFlagSet("coverage", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), usage)
		flags.PrintDefaults()
	}
	var names labels
	flags.Var(&names, "label", "name the inputs on the page, one -label for each, in order")
	out := flags.String("out", "", "write the report to this file")
	root := flags.String("root", ".", "the source tree the inputs describe")
	title := flags.String("title", "Test coverage", "the report's heading")
	if err := flags.Parse(args); err != nil {
		// flags.Usage wrote the help text already, so -h is done and
		// is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	inputs := flags.Args()
	if len(inputs) == 0 || *out == "" {
		return errors.New(usage)
	}
	if len(names) > len(inputs) {
		return fmt.Errorf("%d labels name %d inputs", len(names), len(inputs))
	}

	sources := make([]*Source, 0, len(inputs))
	for i, input := range inputs {
		source, err := Read(input, *root)
		if err != nil {
			return err
		}
		if i < len(names) {
			source.Label = names[i]
		}
		sources = append(sources, source)
	}

	page, err := Render(*title, sources)
	if err != nil {
		return err
	}
	return os.WriteFile(*out, page, 0o644)
}
