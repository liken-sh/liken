// skills turns a site's guides into Agent Skills, one SKILL.md per
// guide.
//
// The docs Makefile of each liken repository runs it as
//
//	go tool skills -base <baseURL> <guides dir> <out dir>
//
// pinned as a tool dependency the way crdref is. The output is
// committed in each repository, so a checkout carries its skills
// and CI fails when they are stale. plans/01-guides-as-skills.md in
// this repository is the design.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// usage is both the -h help text and the error a wrong argument
// count returns, so one line stays true for both.
const usage = "usage: skills -base <baseURL> <guides dir> <out dir>"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "skills: %v\n", err)
		os.Exit(1)
	}
}

// run parses the flags and emits the skills. -base is required,
// because every link in a skill becomes a full URL, and there is no
// site to take the base from when the skill is read from disk.
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
	if len(args) != 2 || *base == "" {
		return errors.New(usage)
	}
	return Emit(args[0], args[1], *base)
}
