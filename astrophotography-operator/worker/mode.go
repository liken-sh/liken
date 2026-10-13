package worker

import (
	"flag"
	"fmt"
	"io"

	"github.com/liken-sh/liken/astrophotography-operator/outbox"
)

// ParseOptions reads the flags of the worker mode into an Extractor.
// The defaults are the paths of the astrometry image and the pod's
// volumes, so the operator's Deployment passes no flag.
func ParseOptions(args []string, output io.Writer) (Extractor, error) {
	var e Extractor
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&e.Outbox, "outbox", outbox.Root, "where the outbox claim is mounted")
	flags.StringVar(&e.Work, "work", "/work", "a writable directory for each extraction's files")
	flags.StringVar(&e.SourceExtractor, "source-extractor", "/usr/bin/source-extractor", "the path of source-extractor")
	flags.StringVar(&e.SolveField, "solve-field", "/usr/bin/solve-field", "the path of solve-field")
	if err := flags.Parse(args); err != nil {
		return Extractor{}, err
	}
	if flags.NArg() > 0 {
		return Extractor{}, fmt.Errorf("the worker mode takes no argument, and was given %q", flags.Args())
	}
	return e, nil
}
