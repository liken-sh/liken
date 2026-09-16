package main

// Enrichrun.go is the enricher Job's one regular container. It
// writes the runs row and waits for a catalog pod to confirm it, which is
// what proves the rows the init containers left have reached the catalog.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// The container that closes an enricher Job: the enricher's own
// environment, and how long it waits to be confirmed.
type enrichRun struct {
	*enricher
	handoffTimeout time.Duration
}

// The role's whole program. A Job no catalog pod confirms fails, so
// its rows stay on its claim and the retry carries them.
func runEnrich() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	run, err := newEnrichRun(os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "library.liken.sh: %v\n", err)
		stop()
		os.Exit(1)
	}
	if err := run.runJob(stopped); err != nil {
		run.logf("the enrich job failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// The container reads everything it needs from its environment, as
// every other enricher container does.
func newEnrichRun(log io.Writer) (*enrichRun, error) {
	work, err := newEnricher(log)
	if err != nil {
		return nil, err
	}
	return &enrichRun{
		enricher:       work,
		handoffTimeout: handoffTimeout(os.Getenv(handoffTimeoutVariable)),
	}, nil
}

// The container writes the finished run and hands off. An enricher
// Job writes to the volume and changes no item or file row, so the run row
// is the only write this container makes.
func (r *enrichRun) runJob(ctx context.Context) error {
	run := libraryRun{
		Worker:   workerEnrich,
		Job:      r.job,
		Started:  r.startedAt(ctx),
		Finished: time.Now().UTC(),
	}
	return handOff(ctx, r.catalog, r.library, run, r.log, r.handoffTimeout)
}

// The start time comes off the row the probe container wrote. A row that
// names another Job is a run that never finished, and this container takes
// its own start instead.
func (r *enrichRun) startedAt(ctx context.Context) time.Time {
	now := time.Now().UTC()
	runs, err := r.catalog.Runs(ctx)
	if err != nil {
		r.logf("could not read the run this job started: %v", err)
		return now
	}
	held, found := runOf(runs[r.library], workerEnrich)
	if !found || held.Job != r.job || held.Started.IsZero() {
		return now
	}
	return held.Started
}
