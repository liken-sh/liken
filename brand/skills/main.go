// skills turns a site's guides into Agent Skills, one SKILL.md per
// guide.
//
// Each component's docs module declares it as a tool, and the docs
// Makefile runs it as
//
//	go tool skills -base <baseURL> <guides dir> <out dir>
//
// The output is committed in the component's skills/ directory, so a
// checkout has the skills, and the component's check fails when a
// guide changed and its skill was not generated again.
// plans/completed/01-guides-as-skills.md in this component is the
// design.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// usage is the -h help text, and the error of a wrong command line
// ends with it.
const usage = "usage: skills -base <baseURL> <guides dir> <out dir>"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "skills: %v\n", err)
		os.Exit(1)
	}
}

// run parses the flags and writes the skills. -base is required,
// because every link in a skill becomes a full URL, and an agent that
// reads the skill from disk has no site to resolve a path against.
func run(args []string) error {
	flags := flag.NewFlagSet("skills", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), usage)
		flags.PrintDefaults()
	}
	base := flags.String("base", "", "the site's base URL, which every link in the output resolves against")
	if err := flags.Parse(args); err != nil {
		// flags.Usage wrote the help text already, so -h is done and
		// is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	args = flags.Args()
	if *base == "" {
		return fmt.Errorf("-base is required\n%s", usage)
	}
	if len(args) != 2 {
		return fmt.Errorf("expected 2 arguments, got %d\n%s", len(args), usage)
	}
	return Emit(args[0], args[1], *base)
}
