package main

// enrichsync.go is the wait every phase container makes before it reads its
// gap. A fresh or stale claim answers SELECT 1 long before the standing pod's
// rows have reached it, and a gap query against an empty copy reports work
// that is not there, or asks a provider again about a title whose attempt
// has not arrived. On the first drill the probe read zero files where the
// reporter counted 24.
//
// The copy is synced when it holds the write that the Library's last
// confirmed run made: every version of that run's agent up to the version
// the run names. The operator reads that run off the reporter's report and
// writes it into the Job, because the local copy cannot tell a library with
// no history from a copy that has not synced its history yet. Ranges missing
// from other writers do not count, because a copy carries the gaps of agents
// that died with versions unsent, and those never fill.

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// The bound on the wait, as an environment variable. Ten minutes is the
// default because a first sync of a whole library onto a fresh claim takes
// minutes on the testbed.
const (
	syncTimeoutVariable = "SYNC_TIMEOUT"
	defaultSyncTimeout  = 10 * time.Minute
)

// The poll of the local copy is a variable so a test drives it in
// milliseconds.
var catalogSyncInterval = time.Second

// An empty, unreadable, or negative value takes the default, the
// rule handoffTimeout follows, because the wait is a bound and not a fact.
func syncTimeout(raw string) time.Duration {
	if raw == "" {
		return defaultSyncTimeout
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return defaultSyncTimeout
	}
	return timeout
}

// The write a copy must hold: the agent that made it and the db version the
// agent gave it. An empty actor is a Library with no confirmed run, which
// has nothing to wait for.
type syncTarget struct {
	actor   string
	version int64
}

// The environment that carries the target into a phase container.
const (
	syncActorVariable   = "LIBRARY_SYNC_ACTOR"
	syncVersionVariable = "LIBRARY_SYNC_VERSION"
)

// A version this image cannot read is version zero, which every copy of that
// agent holds, so a bad value costs the wait and never blocks the Job.
func syncTargetOf(actor, version string) syncTarget {
	number, _ := strconv.ParseInt(version, 10, 64)
	return syncTarget{actor: actor, version: number}
}

// The newest confirmed run of one library: the finished run with a version
// that finished last, whatever its worker. Its write covers every row the
// Job that wrote it made, and every Job of a Library writes on one claim.
func syncTargetFor(runs []libraryRun) syncTarget {
	var newest libraryRun
	for _, run := range runs {
		if run.Version == 0 || run.Finished.IsZero() || !run.Finished.After(newest.Finished) {
			continue
		}
		newest = run
	}
	return syncTarget{actor: newest.Actor, version: newest.Version}
}

// Whether this copy holds the target.
func catalogSynced(ctx context.Context, catalog *Catalog, target syncTarget) (bool, error) {
	if target.actor == "" {
		return true, nil
	}
	return versionsHeld(ctx, catalog, target.actor, target.version)
}

// The wait polls the local copy until it holds the target. The timeout
// is a failure exit, so the Job retries instead of working from a short list.
func awaitCatalogSync(ctx context.Context, catalog *Catalog, target syncTarget, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(catalogSyncInterval)
	defer ticker.Stop()
	for {
		synced, err := catalogSynced(ctx, catalog, target)
		if err != nil {
			return err
		}
		if synced {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("the catalog did not reach version %d of %s on this claim within %s",
				target.version, target.actor, timeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Every phase container makes this wait before its first gap read.
func (e *enricher) awaitCatalogSync(ctx context.Context) error {
	return awaitCatalogSync(ctx, e.catalog, e.sync, e.syncTimeout)
}
