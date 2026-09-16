package main

// enrichsync.go is the wait every fact container makes before it reads its
// gap. A fresh or stale claim answers SELECT 1 long before the standing pod's
// rows have reached it, and a gap query against an empty copy reports work
// that is not there. On the first drill the probe read zero files where the
// reporter counted 24.
//
// The copy is synced when it holds the walk's own write and has no
// hole behind it: a finished scan run that names the agent and version its
// writer made, every version of that writer up to it, and no range this
// copy knows it is missing. The counts alone are not enough: a walk that
// changes no item and no file, such as the one after a refresh, leaves the
// counts as they were while its attempts and rows are still on their way,
// and a container that read its gap then found most of the work missing.

import (
	"context"
	"fmt"
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

// A copy is synced when its runs table holds a finished scan of this
// library that names the write its writer made, when this copy holds every
// version of that writer up to it, and when this copy knows of no missing
// range at all. A copy with a hole anywhere is one whose gap read would
// report work that is already done.
//
// A finished scan that names no version was written by a scanner from
// before the confirmation existed, and there is nothing about it to
// prove. Such a row takes the rest of the rule and skips the version
// check. Without this, every enricher of a library would fail its start
// wait until that library's next walk.
func catalogSynced(ctx context.Context, catalog *Catalog, library string) (bool, error) {
	runs, err := catalog.Runs(ctx)
	if err != nil {
		return false, err
	}
	walk, held := runOf(runs[library], workerScan)
	if !held || walk.Finished.IsZero() {
		return false, nil
	}
	if walk.Version > 0 {
		whole, err := versionsHeld(ctx, catalog, walk.Actor, walk.Version)
		if err != nil || !whole {
			return false, err
		}
	}
	gaps, err := catalog.openGaps(ctx)
	return gaps == 0 && err == nil, err
}

// The wait polls the local copy until it holds the walk. The timeout
// is a failure exit, so the Job retries instead of working from a short list.
func awaitCatalogSync(ctx context.Context, catalog *Catalog, library string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(catalogSyncInterval)
	defer ticker.Stop()
	for {
		synced, err := catalogSynced(ctx, catalog, library)
		if err != nil {
			return err
		}
		if synced {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("the catalog did not sync onto the claim of %s within %s", library, timeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Every fact container makes this wait before its gap read.
func (e *enricher) awaitCatalogSync(ctx context.Context) error {
	return awaitCatalogSync(ctx, e.catalog, e.library, e.syncTimeout)
}
