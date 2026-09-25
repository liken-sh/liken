package main

// phaseloop.go is the loop every phase container of a library Job runs. All
// the phases start together, and a phase works on each title as soon as the
// phases before it have written that title's rows. So a phase does not run
// its facts once. It runs passes: a pass reads the gap of each fact and runs
// the fact over it, and every fact already writes its own rows, so the next
// pass finds the titles the phases before it finished since the last one.
//
// A phase ends when every phase it waits for has ended and one pass after
// that found no work. Between passes it waits for an event: a row change on
// a table its gap queries read, which the local agent's update stream
// reports, or a mark on the phases volume. It polls nothing.

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sync"
	"time"
)

// How long trickplay and the trailer files work in one run. One trickplay
// title decodes for minutes, and one trailer download takes up to ten, so a
// backlog would hold the Job for hours and a webhook's folders would wait
// behind all of it. At the limit the phase finishes the title it has, starts
// no other, and ends, and the rest stays as gaps for the next Job. A
// variable, so a test reaches the limit in milliseconds.
var phaseTimeLimit = 15 * time.Minute

// The phases whose run stops at the time limit, by container name.
var timeLimitedPhases = map[string]bool{
	trickplayContainerName:   true,
	trailerFileContainerName: true,
}

// How long a phase waits after an event before its next pass, so a walk that
// writes a thousand rows costs one pass and not a thousand. A variable, so a
// test runs a pass in milliseconds.
var phaseSettle = time.Second

// The gap of every fact of one phase at one moment: the keys each gap query
// returned, sorted.
type gapSnapshot map[string][]string

func (s gapSnapshot) empty() bool {
	for _, keys := range s {
		if len(keys) > 0 {
			return false
		}
	}
	return true
}

func (s gapSnapshot) equal(other gapSnapshot) bool {
	return maps.EqualFunc(s, other, slices.Equal)
}

// Reads the gap of every fact the phase runs.
func (e *enricher) gapSnapshot(ctx context.Context, facts []string) (gapSnapshot, error) {
	snapshot := gapSnapshot{}
	now := time.Now().UTC()
	for _, fact := range facts {
		keys, err := e.gaps(ctx, fact, now)
		if err != nil {
			return nil, err
		}
		slices.Sort(keys)
		snapshot[fact] = keys
	}
	return snapshot, nil
}

// One pass: every fact of the phase once, in the order the container names
// them.
func (e *enricher) pass(ctx context.Context, facts []string) error {
	for _, name := range facts {
		if err := factRuns[name](ctx, e); err != nil {
			return fmt.Errorf("the %s fact failed: %w", name, err)
		}
	}
	return nil
}

// The phase's loop, from the first pass to the end. A pass runs when the gap
// holds work and the gap changed since the last pass, because a gap that did
// not change holds only titles the last pass left: titles outside the Job's
// folders, and titles a fact could not work on. Once every phase this one
// waits for has ended, one more pass runs over what is left, and the phase
// ends when a later read finds the gap empty or unchanged.
func (e *enricher) phaseLoop(ctx context.Context, facts []string) error {
	if timeLimitedPhases[e.container] {
		e.stopStarting = time.Now().Add(phaseTimeLimit)
	}
	wake, stop, err := e.wakes(ctx, facts)
	if err != nil {
		return err
	}
	defer stop()
	var last gapSnapshot
	passedSinceEnded := false
	for {
		if !e.mayStartTitle() {
			e.logf("the %s phase reached its time limit of %s", e.container, phaseTimeLimit)
			return nil
		}
		ended := e.needsEnded(ctx)
		snapshot, err := e.gapSnapshot(ctx, facts)
		if err != nil {
			return err
		}
		if !snapshot.empty() && (!snapshot.equal(last) || (ended && !passedSinceEnded)) {
			if err := e.pass(ctx, facts); err != nil {
				return err
			}
			last = snapshot
			passedSinceEnded = passedSinceEnded || ended
			continue
		}
		if ended {
			return nil
		}
		if err := e.awaitWake(ctx, wake); err != nil {
			return err
		}
	}
}

// Waits for one event, then for the settle time, so the pass that follows
// reads the rows of a burst of writes at once. A phase with a time limit
// also wakes when the limit passes.
func (e *enricher) awaitWake(ctx context.Context, wake <-chan struct{}) error {
	var limit <-chan time.Time
	if !e.stopStarting.IsZero() {
		timer := time.NewTimer(time.Until(e.stopStarting))
		defer timer.Stop()
		limit = timer.C
	}
	select {
	case <-wake:
	case <-limit:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-time.After(phaseSettle):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Whether every phase this container waits for has ended. A failed phase
// counts as ended, so a phase that depends on it finishes the gaps it has
// and does not wait for rows that will not come. A scan container from an
// image built before the phases volume existed writes no mark, and its
// finished runs row stands for one.
func (e *enricher) needsEnded(ctx context.Context) bool {
	for _, need := range e.needs {
		if e.board.ended(need).ended {
			continue
		}
		if need == scanPhase && e.walkFinished(ctx) {
			continue
		}
		return false
	}
	return true
}

// Whether this Job's walk has written its finished runs row, whether it
// walked the whole library or its folders.
func (e *enricher) walkFinished(ctx context.Context) bool {
	runs, err := e.catalog.Runs(ctx)
	if err != nil {
		return false
	}
	for _, worker := range []string{workerScan, workerRescan} {
		if run, held := runOf(runs[e.library], worker); held && run.Job == e.job && !run.Finished.IsZero() {
			return true
		}
	}
	return false
}

// The events a phase waits for, as one channel: the marks on the phases
// volume, and the update streams of the tables its gap queries read. A phase
// that waits for the scan follows the runs table as well, for the scan
// container that writes no mark. The stop ends every stream and returns once
// each has closed, so no stream outlives the loop that reads it.
func (e *enricher) wakes(ctx context.Context, facts []string) (<-chan struct{}, func(), error) {
	following, cancel := context.WithCancel(ctx)
	marks, err := e.board.watch(following)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	changed := make(chan struct{}, 1)
	var streams sync.WaitGroup
	streams.Go(func() {
		for {
			select {
			case <-following.Done():
				return
			case <-marks:
				markChanged(changed)
			}
		}
	})
	tables := gapTables(facts)
	if slices.Contains(e.needs, scanPhase) {
		tables = append(tables, "runs")
	}
	catalog := e.catalog.streaming()
	for _, table := range tables {
		streams.Go(func() { followTableChanges(following, catalog, table, changed, e.logf) })
	}
	return changed, func() {
		cancel()
		streams.Wait()
	}, nil
}

// The table names a query reads, after FROM or JOIN.
var queryTables = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-z_]+)`)

// The tables whose changes can open a gap of these facts: every replicated
// table their gap queries read, less the attempts table. An attempt only
// closes a gap, and every attempt the phase writes would wake it again.
func gapTables(facts []string) []string {
	replicated := map[string]bool{}
	for _, table := range catalogTables {
		replicated[table] = true
	}
	delete(replicated, "attempts")
	held := map[string]bool{}
	for _, fact := range facts {
		for _, match := range queryTables.FindAllStringSubmatch(gapQueries[fact], -1) {
			if replicated[match[1]] {
				held[match[1]] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(held))
}
