package main

// enrichsync.go is the wait every fact container makes before it reads its
// gap. A fresh or stale claim answers SELECT 1 long before the standing pod's
// rows have reached it, and a gap query against an empty copy reports work
// that is not there. On the first drill the probe read zero files where the
// reporter counted 24.
//
// The copy is synced when it holds the counts the report carries and the
// walk the report names. The counts alone are not enough: a walk that
// changes no item and no file, such as the one after a refresh, leaves the
// counts as they were while its attempts and rows are still on their way,
// and a container that read its gap then found most of the work missing.
// The runs row is the walk's last write, so a copy that holds it holds
// what came before it.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// An empty, unreadable, or negative value takes the default, the rule
// echoTimeout follows, because the wait is a bound and not a fact.
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

// What a catalogSync holds: the topic the standing pod's report arrives on,
// and what the newest report said the copy must hold.
type catalogSync struct {
	topic string

	// The mutex covers the target, because the bus handler runs on the bus
	// reader's goroutine and the poll runs on the caller's.
	mutex    sync.Mutex
	reported *syncTarget
}

// What a report says the copy must hold: its counts, and the finish of
// the last walk, zero where the report names no finished walk.
type syncTarget struct {
	counts libraryCounts
	walked time.Time
}

func newCatalogSync(topic string) *catalogSync {
	return &catalogSync{topic: topic}
}

// The newest report replaces the last one, because a report is a whole
// observation of the standing pod's copy.
func (s *catalogSync) note(topic string, payload []byte) {
	if topic != s.topic {
		return
	}
	var report libraryReport
	if json.Unmarshal(payload, &report) != nil {
		return
	}
	s.mutex.Lock()
	s.reported = &syncTarget{
		counts: libraryCounts{items: report.Items, files: report.Files},
		walked: lastScanFinish(report.Runs),
	}
	s.mutex.Unlock()
}

// A copy is synced when its own counts equal the ones the report carries
// and its own runs hold a walk that finished no earlier than the one the
// report names. A container that has heard no report yet is not synced.
func (s *catalogSync) synced(ctx context.Context, catalog *Catalog, library string) (bool, error) {
	s.mutex.Lock()
	reported := s.reported
	s.mutex.Unlock()
	if reported == nil {
		return false, nil
	}
	counts, err := catalog.countsOf(ctx, library)
	if err != nil {
		return false, err
	}
	if counts != reported.counts {
		return false, nil
	}
	if reported.walked.IsZero() {
		return true, nil
	}
	runs, err := catalog.Runs(ctx)
	if err != nil {
		return false, err
	}
	return !lastScanFinish(runs[library]).Before(reported.walked), nil
}

// The wait runs the bus, subscribes to the retained report, and polls the
// local copy until it matches. The timeout is a failure exit, so the Job
// retries instead of working from a short list.
func (s *catalogSync) wait(ctx context.Context, bus *Bus, catalog *Catalog,
	library string, timeout time.Duration) error {
	running, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		bus.Run(running)
	}()
	defer func() {
		stop()
		<-done
	}()

	bus.Subscribe(s.topic)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(catalogSyncInterval)
	defer ticker.Stop()
	for {
		synced, err := s.synced(ctx, catalog, library)
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

// Every fact container makes this wait before its gap read. A container
// with no broker refuses to start, because it could never hear the report.
func (e *enricher) awaitCatalogSync(ctx context.Context, fact string) error {
	address, err := echoBusAddress(e.log)
	if err != nil {
		return err
	}
	sync := newCatalogSync(e.statusTopic)
	client := fact + "-sync-" + strings.ReplaceAll(e.library, "/", "-")
	return sync.wait(ctx, newBus(address, client, nil, nil, sync.note),
		e.catalog, e.library, e.syncTimeout)
}
