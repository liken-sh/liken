package solver

// The solver starts one astrometry-engine process for each request,
// because the exit of the process is the only sign that a job ended
// without a solution. The engine writes no file for a field it does not
// solve, so an engine that keeps running and reads jobs from its
// standard input reports nothing for that job.

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

// Engine runs astrometry-engine on one .axy file at a time.
type Engine struct {
	// Program is the path of astrometry-engine.
	Program string
	// Config is the path of the engine's configuration (EngineConfig).
	Config string
	// Work is the directory in which each job gets its own directory. In
	// the pod it is an emptyDir.
	Work string
	// Timeout limits the wall-clock time of one run. The CPU limit in
	// the engine's configuration ends a search that takes too long, and
	// this timeout ends an engine that hangs for any other reason.
	Timeout time.Duration
}

// ErrBadStarList means that Solve refused the .axy without running the
// engine: it is not FITS, names no WCS file, or names an output file
// outside the job's directory.
var ErrBadStarList = errors.New("the solver does not accept the star list")

// outputs are the .axy header keywords that name a file the engine
// writes. The worker runs solve-field with -D ., so each name is
// relative, and the engine runs in the job's directory. Solve refuses a
// name that is not local, so the engine cannot write anywhere else.
var outputs = []string{"ANSOLVED", "ANSOLVIN", "ANMATCH", "ANRDLS", "ANWCS", "ANCORR"}

// Solve writes the .axy into a new directory, runs the engine there,
// and returns the solution from the WCS file that the .axy names. It
// returns an empty SolveResponse when the engine wrote no WCS file. It
// deletes the directory before it returns.
func (e Engine) Solve(ctx context.Context, axy []byte) (SolveResponse, error) {
	header, err := fits.ReadHeader(bytes.NewReader(axy))
	if err != nil {
		return SolveResponse{}, fmt.Errorf("%w: %v", ErrBadStarList, err)
	}
	wcs, err := header.String("ANWCS")
	if err != nil {
		return SolveResponse{}, fmt.Errorf("%w: the header has no ANWCS keyword with a file name", ErrBadStarList)
	}
	for _, key := range outputs {
		if path, present := header.Get(key); present && !filepath.IsLocal(path) {
			return SolveResponse{}, fmt.Errorf("%w: %s = %q is not a path inside the job's directory", ErrBadStarList, key, path)
		}
	}
	dir, err := os.MkdirTemp(e.Work, "job-")
	if err != nil {
		return SolveResponse{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "job.axy"), axy, 0o600); err != nil {
		return SolveResponse{}, err
	}
	run, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	cmd := exec.CommandContext(run, e.Program, "-c", e.Config, "job.axy")
	cmd.Dir = dir
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Run(); err != nil {
		return SolveResponse{}, fmt.Errorf("running astrometry-engine: %w: %s", err, tail(log.Bytes()))
	}
	solution, err := os.ReadFile(filepath.Join(dir, wcs))
	if errors.Is(err, fs.ErrNotExist) {
		return SolveResponse{}, nil
	}
	if err != nil {
		return SolveResponse{}, err
	}
	h, err := fits.ReadHeader(bytes.NewReader(solution))
	if err != nil {
		return SolveResponse{}, fmt.Errorf("reading the WCS file: %w", err)
	}
	return solutionOf(h)
}

// tail returns the end of the engine's log, where the reason for a
// failure is.
func tail(log []byte) string {
	const keep = 512
	if len(log) > keep {
		log = log[len(log)-keep:]
	}
	return string(bytes.TrimSpace(log))
}
