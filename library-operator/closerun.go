package main

// closerun.go is the close container of a library Job. It starts with the
// phases and does no work on titles. It writes the run's start first, so the
// operator reads a run in flight from the moment the Job starts. It then waits
// for the mark of every phase the Job includes, derives the Library's set
// rows, writes the one finished runs row, and waits for a catalog pod to
// confirm it. Every container of the Job writes through the one agent, so that
// confirmation covers every row the Job wrote.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// The role the close container runs, and its container name.
const closeMode = "close"

// The close container: the phase environment every container reads, and how
// long it waits to be confirmed.
type closeRun struct {
	*enricher
	handoffTimeout time.Duration
}

// The role's whole program. A Job no catalog pod confirms fails, so its rows
// stay on its claim and the retry carries them.
func runClose() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	run, err := newCloseRun(os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "library.liken.sh: %v\n", err)
		stop()
		os.Exit(1)
	}
	if err := run.runJob(stopped); err != nil {
		run.logf("the library job failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// The container reads everything it needs from its environment, as every
// phase container does.
func newCloseRun(log io.Writer) (*closeRun, error) {
	work, err := newEnricher(log)
	if err != nil {
		return nil, err
	}
	return &closeRun{
		enricher:       work,
		handoffTimeout: handoffTimeout(os.Getenv(handoffTimeoutVariable)),
	}, nil
}

// The start, the wait for every phase, the sets, and the hand-off. A phase
// that failed does not fail the Job: its failure goes into the runs row, and
// its gaps stay open for the next Job.
func (r *closeRun) runJob(ctx context.Context) error {
	run := libraryRun{Worker: workerEnrich, Job: r.job, Started: time.Now().UTC()}
	if _, _, err := r.catalog.UpsertRun(ctx, r.library, run); err != nil {
		return fmt.Errorf("writing the run of %s: %w", r.library, err)
	}
	r.sweepOldTallies(ctx, run.Started)

	failures, err := r.awaitPhases(ctx)
	if err != nil {
		return err
	}
	if r.kind == libraryKindMovies {
		if err := deriveLibrarySets(ctx, r.catalog, r.library); err != nil {
			r.logf("could not derive the sets of %s: %v", r.library, err)
			failures = append(failures, "the sets: "+err.Error())
		}
	}
	run.Finished = time.Now().UTC()
	run.Failure = strings.Join(failures, "; ")
	return handOff(ctx, r.catalog, r.library, run, r.log, r.handoffTimeout)
}

// Waits until every phase of the Job has ended, and returns the failures the
// failed phases wrote. A container with no phases volume waits for nothing.
func (r *closeRun) awaitPhases(ctx context.Context) ([]string, error) {
	if r.board == nil {
		return nil, nil
	}
	wake, stop, err := r.wakes(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer stop()
	for !r.needsEnded(ctx) {
		if err := r.awaitWake(ctx, wake); err != nil {
			return nil, err
		}
	}
	_, failures := r.board.allEnded(r.needs)
	return failures, nil
}
