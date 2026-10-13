package solver

// One run of astrometry-engine for each request. The engine writes no
// file for a field it does not solve, so a resident engine that reads
// paths on its standard input gives no sign that a job ended without a
// solution. A process for each job ends the job with its exit, whether
// or not it solved, and plan 02 measured the cost of a start at 0.06 s
// with the lab's 13 index files.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/fits"
)

// Engine runs astrometry-engine on one .axy at a time.
type Engine struct {
	// Program is the path of astrometry-engine.
	Program string
	// Config is the path of the engine's configuration (EngineConfig).
	Config string
	// Work is the directory that holds a directory for each job, an
	// emptyDir in the pod.
	Work string
	// Timeout bounds one run in wall time. The CPU limit of the
	// configuration bounds the search, and this bounds an engine that
	// hangs for any other reason.
	Timeout time.Duration
}

// ErrBadStarList is the error of an .axy that the solver refuses
// before it runs the engine.
var ErrBadStarList = errors.New("the star list is not one the solver runs")

// outputs are the keywords of an .axy that name a file the engine
// writes. solve-field writes each one relative to its -D directory,
// and the worker passes -D ., so the engine writes each one in the
// job's directory. The solver refuses any other path, so the engine
// never writes outside that directory.
var outputs = []string{"ANSOLVED", "ANSOLVIN", "ANMATCH", "ANRDLS", "ANWCS", "ANCORR"}

// Solve writes the .axy into a new directory, runs the engine there,
// and answers the solution in the WCS file the .axy names, or no
// solution when the engine wrote none. It deletes the directory before
// it answers.
func (e Engine) Solve(ctx context.Context, axy []byte) (SolveAnswer, error) {
	header, err := fits.ReadHeader(bytes.NewReader(axy))
	if err != nil {
		return SolveAnswer{}, fmt.Errorf("%w: %v", ErrBadStarList, err)
	}
	wcs, err := header.String("ANWCS")
	if err != nil {
		return SolveAnswer{}, fmt.Errorf("%w: it names no WCS file", ErrBadStarList)
	}
	for _, key := range outputs {
		if path, held := header.Get(key); held && !filepath.IsLocal(path) {
			return SolveAnswer{}, fmt.Errorf("%w: %s = %q is outside the job's directory", ErrBadStarList, key, path)
		}
	}
	dir, err := os.MkdirTemp(e.Work, "job-")
	if err != nil {
		return SolveAnswer{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "job.axy"), axy, 0o600); err != nil {
		return SolveAnswer{}, err
	}
	run, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	cmd := exec.CommandContext(run, e.Program, "-c", e.Config, "job.axy")
	cmd.Dir = dir
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Run(); err != nil {
		return SolveAnswer{}, fmt.Errorf("astrometry-engine: %w: %s", err, tail(log.Bytes()))
	}
	solution, err := os.ReadFile(filepath.Join(dir, wcs))
	if errors.Is(err, fs.ErrNotExist) {
		return SolveAnswer{}, nil
	}
	if err != nil {
		return SolveAnswer{}, err
	}
	h, err := fits.ReadHeader(bytes.NewReader(solution))
	if err != nil {
		return SolveAnswer{}, fmt.Errorf("the WCS file: %w", err)
	}
	return answerOf(h)
}

// tail answers the last part of the engine's log, which holds the
// reason it failed.
func tail(log []byte) string {
	const keep = 512
	if len(log) > keep {
		log = log[len(log)-keep:]
	}
	return string(bytes.TrimSpace(log))
}
