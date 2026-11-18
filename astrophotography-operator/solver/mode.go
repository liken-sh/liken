package solver

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"
)

// Options are the flags of the solver mode. The operator's Deployment
// sets --indexes to the directory of the Solver's spec.indexFiles. The
// other flags keep their defaults, which are the paths in the
// astrometry image and the pod's volumes.
type Options struct {
	// Indexes is the directory that contains the index files.
	Indexes string
	// Work is a writable directory for the engine's configuration file
	// and the files of each job.
	Work string
	// Engine is the path of astrometry-engine.
	Engine string
	// CPULimit is the CPU time, in seconds, that the engine spends on
	// one field before it gives up and reports no solution.
	CPULimit int
	// Timeout is the wall-clock limit for one run of the engine.
	Timeout time.Duration
}

// ParseOptions reads the flags of the solver mode.
func ParseOptions(args []string, output io.Writer) (Options, error) {
	var o Options
	flags := flag.NewFlagSet("solver", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&o.Indexes, "indexes", "/indexes", "the directory of the index files")
	flags.StringVar(&o.Work, "work", "/work", "a writable directory for the engine's files")
	flags.StringVar(&o.Engine, "engine", "/usr/bin/astrometry-engine", "the path of astrometry-engine")
	flags.IntVar(&o.CPULimit, "cpu-limit", 60, "the CPU seconds the engine spends on one field")
	// The default is twice the default CPU limit, so the engine's own
	// CPU limit stops a field first, even on a slower node.
	flags.DurationVar(&o.Timeout, "timeout", 2*time.Minute, "the wall time of one run of the engine")
	if err := flags.Parse(args); err != nil {
		return Options{}, err
	}
	if flags.NArg() > 0 {
		return Options{}, fmt.Errorf("the solver mode takes flags only, and was given the arguments %q", flags.Args())
	}
	return o, nil
}

// Start watches the index directory, reads the index files, writes the
// engine's configuration, and returns the server. It logs each index
// series it finds. A goroutine keeps the engine's configuration current
// as index files change, until ctx ends. When the directory has no
// index file, Start still returns a server, which reports no indexes,
// so the Solver's status shows NoIndexFiles and the pod does not
// restart in a loop.
func Start(ctx context.Context, o Options, logs io.Writer) (*Server, error) {
	config := filepath.Join(o.Work, "engine.cfg")
	set := newIndexSet(config, o.CPULimit, logs)
	w := &indexWatch{dir: o.Indexes, set: set, logs: logs}
	watch, inside, err := w.open()
	if err != nil {
		return nil, err
	}
	engine := Engine{Program: o.Engine, Config: config, Work: o.Work, Timeout: o.Timeout}
	s := newServer(engine.Solve, set, logs)
	go func() {
		defer close(s.done)
		if err := w.run(ctx, watch, inside); err != nil {
			s.failed <- err
		}
	}()
	return s, nil
}
