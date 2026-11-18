// coverage turns the coverage data from a repository's tests into one
// self-contained HTML report.
//
// Each component's Makefile runs it from the component's docs module,
// which declares it as a tool:
//
//	go tool coverage -title <name> -out coverage.html [-root <dir>] [-label <name>] <input>...
//
// An input is a Go coverage profile or a Cobertura report, so a
// component with Go and Rust code gets one page for both. The manual's
// site and the release channel publish the page, so it is one file
// with its stylesheet inline.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// usage is the -h help text and the error for a wrong command line.
const usage = "usage: coverage [-title <name>] [-label <name>] [-root <dir>] " +
	"-out <file> <input> [<input>...]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
		os.Exit(1)
	}
}

// labels collects the repeated -label flag, which names the inputs on
// the page in order. A Cobertura report does not record its language,
// so without a label the page shows the input's file name.
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
		// flags.Usage has printed the help text, so -h succeeds.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	inputs := flags.Args()
	if len(inputs) == 0 || *out == "" {
		return fmt.Errorf("coverage needs -out and at least one input\n%s", usage)
	}
	if len(names) > len(inputs) {
		return fmt.Errorf("the command line has %d -label flags and %d inputs, and each -label names one input", len(names), len(inputs))
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
