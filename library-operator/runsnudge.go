package main

// A Job writes its finished run row once, and then waits for the catalog
// pod to report it. That first write reaches the catalog pod by one
// gossip broadcast, and about one broadcast in forty from a walk that
// took a second misses: the fanout lands on a stalled peer, and the
// catalog's periodic sync does not know it needs anything from an actor
// it has just met. So while the wait runs, the Job writes the row again
// every few seconds with a later finish time. A changed cell is a new
// version, a new version is a new broadcast with fresh peers, and a
// version the catalog sees names the gap it has to pull.

import (
	"context"
	"fmt"
	"time"
)

// How often the wait writes the run row again. Ten seconds is long
// enough for one broadcast to settle and short against the two-minute
// wait. A test shortens it.
var echoNudge = 10 * time.Second

// renew writes the run row again. A write that fails is one log line and
// not the end of the wait, because the first write may still echo.
func (w *echoWaiter) renew(ctx context.Context) {
	if w.nudge == nil {
		return
	}
	if err := w.nudge(ctx); err != nil && w.log != nil {
		fmt.Fprintf(w.log, "library.liken.sh: could not write the %s run of %s again: %v\n",
			w.worker, w.job, err)
	}
}

// renewRun is the write the scan's wait repeats: the finished run, with
// its finish time moved forward. The run is a copy, so the repeat never
// changes the counts the first write carried.
func (s *scanner) renewRun(run libraryRun) func(context.Context) error {
	return func(ctx context.Context) error {
		run.Finished = nextFinish(run.Finished, time.Now().UTC())
		return s.catalog.UpsertRun(ctx, s.library, run)
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
