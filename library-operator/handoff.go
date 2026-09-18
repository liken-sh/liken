package main

// The hand-off is the last thing a worker Job does. A Corrosion agent
// drops the broadcasts it has not sent when it receives SIGTERM, so a Job
// must not exit until a catalog pod holds what it wrote. The Job
// writes its finished runs row, writes it again with the actor and version
// that write answered with, and waits for a confirmations row that names
// it at that version. confirmations.go holds the proof the confirmer
// makes.
//
// The first write reaches the catalog pod by one gossip broadcast,
// and about one broadcast in forty from a walk that took a second misses:
// the fanout lands on a stalled peer, and the catalog's periodic sync does
// not know it needs anything from an actor it has just met. So the wait
// writes the row again every handoffNudge with a later finish time. A
// changed cell is a new version, a new version is a new broadcast with
// fresh peers, and a version the catalog pod sees names the gap it has to
// pull.

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// The environment every Job container carries: the Job's own name,
// which its runs row carries so a confirmation can name it, and how long
// it waits for that confirmation before it fails.
const (
	jobNameVariable        = "JOB_NAME"
	handoffTimeoutVariable = "HANDOFF_TIMEOUT"
)

// The wait a Job gives the confirmers when the environment names
// none. A confirmation arrives within the gossip latency in the normal
// case, and two minutes covers a catalog pod that is restarting.
const defaultHandoffTimeout = 2 * time.Minute

// How often the wait writes the run row again. Ten seconds is long
// enough for one broadcast to settle and short against the two-minute
// wait. A test shortens it.
var handoffNudge = 10 * time.Second

// How long the wait holds off before it opens the confirmations
// stream again. A test shortens it.
var handoffRetry = time.Second

// HandoffTimeout reads the wait out of the environment. An empty,
// unreadable, or negative value takes the default rather than failing the
// Job, because the wait is a bound and not a fact about the volume.
func handoffTimeout(raw string) time.Duration {
	if raw == "" {
		return defaultHandoffTimeout
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return defaultHandoffTimeout
	}
	return timeout
}

// The subscription that ends the wait. It reads one library,
// worker, Job, and run version, so a confirmation of another run never
// reaches it. The version is what tells this pod's run from the run of a
// pod of the same Job that timed out before it: both carry the Job's name,
// and each writes a version of its own.
const confirmationsQuery = `SELECT confirmer FROM confirmations ` +
	`WHERE library = ? AND worker = ? AND job = ? AND version = ?`

// The column the wait reads out of a confirmations row. The agent's
// matcher prepends the primary key to the projection, so a reader that
// counted cells would read the wrong one.
const confirmerColumn = "confirmer"

// HandOff closes one worker Job: it writes the finished run, writes
// it again naming the write that made it, and waits for a confirmation.
// The second write is one version past the first, and the row names the
// first, because that version covers every data row and the run row with
// them.
func handOff(ctx context.Context, catalog *Catalog, library string, run libraryRun,
	log io.Writer, timeout time.Duration) error {
	actor, version, err := catalog.UpsertRun(ctx, library, run)
	if err != nil {
		return fmt.Errorf("writing the finished run of %s: %w", library, err)
	}
	run.Actor, run.Version = actor, version
	if _, _, err := catalog.UpsertRun(ctx, library, run); err != nil {
		return fmt.Errorf("naming the write of the %s run of %s: %w", run.Worker, library, err)
	}
	wait := newHandoffWaiter(catalog, library, run)
	wait.log = log
	return wait.wait(ctx, timeout)
}

// HandoffWaiter is one Job's wait for a catalog pod to confirm its
// run. It holds the run it writes again while it waits, which is the row
// the confirmation answers.
type handoffWaiter struct {
	catalog *Catalog
	library string
	run     libraryRun
	// Where a failed repeat is reported, and nil to report nowhere.
	log io.Writer

	once      sync.Once
	confirmed chan struct{}
}

func newHandoffWaiter(catalog *Catalog, library string, run libraryRun) *handoffWaiter {
	return &handoffWaiter{
		catalog:   catalog,
		library:   library,
		run:       run,
		confirmed: make(chan struct{}),
	}
}

// Wait follows the confirmations of this run and ends on the first
// one. The wait is bounded by the timeout and by the context, so a Job
// that no pod confirms fails and Kubernetes retries it, rather than
// holding the claim for ever.
func (w *handoffWaiter) wait(ctx context.Context, timeout time.Duration) error {
	following, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.follow(following)
	}()
	defer func() {
		stop()
		<-done
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	nudges := time.NewTicker(handoffNudge)
	defer nudges.Stop()
	for {
		select {
		case <-w.confirmed:
			return nil
		case <-nudges.C:
			w.renew(ctx)
		case <-timer.C:
			return fmt.Errorf("no catalog confirmed the %s run of %s within %s",
				w.run.Worker, w.run.Job, timeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Follow holds the confirmations stream open and opens it again
// after a hold-off when it ends, because a stream that dropped would
// otherwise cost the Job its whole timeout.
func (w *handoffWaiter) follow(ctx context.Context) {
	for ctx.Err() == nil {
		_ = w.catalog.subscribe(ctx, confirmationsQuery,
			[]any{w.library, w.run.Worker, w.run.Job, w.run.Version},
			func() {},
			func(columns []string, cells []any) { w.note(columns, cells) })
		select {
		case <-ctx.Done():
			return
		case <-time.After(handoffRetry):
		}
	}
}

// Note ends the wait on a row that names a confirming pod. The
// query names this run alone, so every row it carries is this Job's.
func (w *handoffWaiter) note(columns []string, cells []any) {
	cell, held := cellNamed(columns, cells, confirmerColumn)
	if !held {
		return
	}
	if confirmer, ok := cell.(string); !ok || confirmer == "" {
		return
	}
	w.once.Do(func() { close(w.confirmed) })
}

// Renew writes the run row again, with its finish time moved
// forward. A write that fails is one log line and not the end of the wait,
// because the writes before it may still be confirmed.
func (w *handoffWaiter) renew(ctx context.Context) {
	w.run.Finished = nextFinish(w.run.Finished, time.Now().UTC())
	if _, _, err := w.catalog.UpsertRun(ctx, w.library, w.run); err != nil && w.log != nil {
		fmt.Fprintf(w.log, "library.liken.sh: could not write the %s run of %s again: %v\n",
			w.run.Worker, w.run.Job, err)
	}
}

// nextFinish is the finish time the next write carries: now, or one
// second past the last write where now is inside the same second. The
// runs table holds Unix seconds, so a time inside the same second would
// write the same cell and make no new version.
func nextFinish(was, now time.Time) time.Time {
	if now.Unix() > was.Unix() {
		return now
	}
	return was.Add(time.Second)
}
