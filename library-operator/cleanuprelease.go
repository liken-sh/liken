package main

// The release is the cleanup Job's last step, after a catalog pod
// confirmed its run. The run and its confirmations are the last rows
// the departed library holds, and they keep its key in the catalog, so
// the namespace's reporter would publish a report for a library that is
// gone. The Job deletes its own run, and the confirmer in each catalog
// pod answers that delete by dropping every confirmation whose run is
// gone (confirmer.go).
//
// The delete needs the same proof as the sweep. An agent that receives
// SIGTERM drops the broadcasts it has not sent, so the Job must not exit
// until a standing pod holds the delete. No confirmation can answer a
// delete, because the run it would name is the row the delete removes.
// The drop is the proof instead: the Job never deletes a confirmation
// itself, so a confirmation that leaves the Job's copy is one that a
// standing pod dropped after it held the delete.

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// The subscription the release follows: every confirmation of this
// Job's run, at any version. The sweep took the confirmations of every
// earlier pod of the Job, so the rows it carries answer this pod's run.
const releaseQuery = `SELECT confirmer FROM confirmations ` +
	`WHERE library = ? AND worker = ? AND job = ?`

// releaseRun deletes the cleanup Job's own run and waits for a standing
// pod to drop its confirmation. The wait is bounded by the timeout, so a
// delete that no pod answers fails the Job, and its retry sweeps and
// hands off again.
//
// The wait writes nothing while it stands. A hand-off writes its run
// again to make a fresh broadcast, but a delete has nothing to write
// again. The standing pod confirmed this agent's versions up to the run,
// so it knows this agent, and its periodic sync pulls the one version the
// delete makes.
func releaseRun(ctx context.Context, catalog *Catalog, library, job string,
	log io.Writer, timeout time.Duration) error {
	if _, err := catalog.DeleteRun(ctx, library, workerCleanup); err != nil {
		return fmt.Errorf("deleting the cleanup run of %s: %w", library, err)
	}
	wait := &releaseWaiter{catalog: catalog, library: library, job: job, released: make(chan struct{})}
	if err := wait.wait(ctx, timeout); err != nil {
		return err
	}
	fmt.Fprintf(log, "library.liken.sh: a catalog pod holds the delete of the cleanup run of %s\n", library)
	return nil
}

// releaseWaiter is one cleanup Job's wait for a standing pod to drop
// the confirmation of its deleted run.
type releaseWaiter struct {
	catalog *Catalog
	library string
	job     string

	once     sync.Once
	released chan struct{}
}

// Wait follows the confirmations of the Job's run and ends when one
// is dropped. It is bounded by the timeout and by the context.
func (w *releaseWaiter) wait(ctx context.Context, timeout time.Duration) error {
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
	select {
	case <-w.released:
		return nil
	case <-timer.C:
		return fmt.Errorf("no catalog dropped the confirmation of the cleanup run %s within %s",
			w.job, timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Follow holds the confirmations stream open, and opens it again after
// a hold-off when it ends. Two events end the wait: a delete of a
// confirmation, and a snapshot that holds none. The hand-off saw a
// confirmation in this copy before the release began, so a snapshot with
// none means a standing pod dropped it, perhaps before this stream opened
// or while an earlier one was down.
func (w *releaseWaiter) follow(ctx context.Context) {
	for ctx.Err() == nil {
		held := false
		_ = w.catalog.subscribe(ctx, releaseQuery,
			[]any{w.library, workerCleanup, w.job},
			func() {
				if !held {
					w.release()
				}
			},
			func(_ []string, _ []any, deleted bool) {
				if deleted {
					w.release()
					return
				}
				held = true
			})
		select {
		case <-ctx.Done():
			return
		case <-time.After(handoffRetry):
		}
	}
}

func (w *releaseWaiter) release() {
	w.once.Do(func() { close(w.released) })
}
