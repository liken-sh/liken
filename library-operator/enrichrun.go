package main

// PROSE: this file is the enricher Job's one regular container: it writes the
// runs row and waits for the standing pod to echo it, which is what proves the
// rows the init containers left have reached the catalog.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// PROSE: the container that closes an enricher Job: the enricher's own
// environment, and the bus the echo arrives on.
type enrichRun struct {
	*enricher
	statusTopic string
	bus         *Bus
	echo        *echoWaiter
	echoTimeout time.Duration
}

// PROSE: the role's whole program. A Job that never hears its echo fails, so
// its rows stay on its claim and the retry carries them.
func runEnrich() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	run, err := newEnrichRun(os.Stdout)
	if err != nil {
		stop()
		os.Exit(1)
	}
	if err := run.runJob(stopped); err != nil {
		run.logf("the enrich job failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// PROSE: says why a container with no broker refuses to start, before it
// writes anything.
func newEnrichRun(log io.Writer) (*enrichRun, error) {
	address, err := echoBusAddress(log)
	if err != nil {
		return nil, err
	}
	namespace := os.Getenv(libraryNamespaceVariable)
	name := os.Getenv(libraryNameVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}

	run := &enrichRun{
		enricher:    newEnricher(log),
		statusTopic: libraryStatusTopic(base, namespace, name),
		echoTimeout: echoTimeout(os.Getenv(echoTimeoutVariable)),
	}
	run.echo = newEchoWaiter(run.statusTopic, workerEnrich, run.job)
	run.bus = newBus(address, "enrich-"+namespace+"-"+name, nil, nil, run.echo.note)
	return run, nil
}

// PROSE: says why the counts this container expects are the ones its own agent
// holds now: an enricher Job writes to the volume and changes no item or file
// row, so the numbers stand where the last scan left them.
func (r *enrichRun) runJob(ctx context.Context) error {
	counts, err := r.catalog.countsOf(ctx, r.library)
	if err != nil {
		return fmt.Errorf("counting the catalog of %s: %w", r.library, err)
	}

	run := libraryRun{
		Worker:   workerEnrich,
		Job:      r.job,
		Started:  r.startedAt(ctx),
		Finished: time.Now().UTC(),
	}
	if err := r.catalog.UpsertRun(ctx, r.library, run); err != nil {
		return fmt.Errorf("writing the finished run of %s: %w", r.library, err)
	}

	r.echo.expect(counts.items, counts.files)
	return r.echo.wait(ctx, r.bus, r.echoTimeout)
}

// PROSE: says why the start time comes off the row the probe container wrote,
// and why a row that names another Job takes this container's own start.
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
