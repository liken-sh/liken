package solver

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Options are the flags of the solver mode. The operator's Deployment
// sets --indexes to the directory of the Solver's spec.indexFiles, and
// leaves the others at their defaults, which are the paths of the
// astrometry image and the pod's volumes.
type Options struct {
	// Indexes is the directory that holds the index files.
	Indexes string
	// Work is a writable directory for the engine's configuration and
	// each job's files.
	Work string
	// Engine is the path of astrometry-engine.
	Engine string
	// CPULimit is the CPU time, in seconds, that the engine spends on
	// one field before it answers no solution (plan 02).
	CPULimit int
	// Timeout bounds one run of the engine in wall time.
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
	// A field that cannot solve stopped at its CPU limit of 60 s after
	// 43 s of wall time on the laptop, so twice the CPU limit leaves
	// room for a slower node.
	flags.DurationVar(&o.Timeout, "timeout", 2*time.Minute, "the wall time of one run of the engine")
	if err := flags.Parse(args); err != nil {
		return Options{}, err
	}
	if flags.NArg() > 0 {
		return Options{}, fmt.Errorf("the solver mode takes no argument, and was given %q", flags.Args())
	}
	return o, nil
}

// Start reads the index files, writes the engine's configuration, and
// answers the server. It logs each series it found. A directory with
// no index file starts a server that reports none, so the Solver's
// status says NoIndexFiles instead of a pod that restarts.
func Start(o Options, logs io.Writer) (*Server, error) {
	files, err := ScanIndexes(o.Indexes)
	if err != nil {
		return nil, err
	}
	config := filepath.Join(o.Work, "engine.cfg")
	if err := os.WriteFile(config, []byte(EngineConfig(files, o.CPULimit)), 0o600); err != nil {
		return nil, err
	}
	series := Series(files)
	for _, s := range series {
		fmt.Fprintf(logs, "index series %d: %d files, quads of %g to %g arcminutes\n", s.ID, s.Files, s.ScaleArcmin.Low, s.ScaleArcmin.High)
	}
	if len(series) == 0 {
		fmt.Fprintf(logs, "no index-*.fits in %s, so every solve answers no solution\n", o.Indexes)
	}
	engine := Engine{Program: o.Engine, Config: config, Work: o.Work, Timeout: o.Timeout}
	return NewServer(engine, series, logs), nil
}
