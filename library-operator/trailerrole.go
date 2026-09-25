package main

// trailerrole.go is the trailer container's run: the titles it asks about,
// the providers it asks, and what it leaves in the ledger and the trailers
// table. It records ids and links. Nothing here plays or downloads a video.

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// The name of the container that runs the trailer group, which is one fact.
const trailerContainerName = "trailer"

// The answerers the trailer container asks, in the order the Library's
// sources name their blocks.
type trailerLine struct {
	answerers []trailerAnswerer
}

// The answerer of each block the trailer fact can ask. A PeerTube instance is
// built on the address that reached the container. The archive takes no key
// and no address of its own, so the block's presence in the source order is
// the whole account.
var trailerAnswerers = map[string]func(base, token string, record *tallies) trailerAnswerer{
	providerBlockTMDb: func(base, token string, record *tallies) trailerAnswerer {
		client := newTMDbClient(base, token)
		client.recordTo(record)
		return newTMDbTrailerAnswerer(client)
	},
	providerBlockPeerTube: func(base, _ string, record *tallies) trailerAnswerer {
		client := newPeertubeClient(base)
		client.recordTo(record)
		return newPeertubeTrailerAnswerer(client)
	},
	providerBlockArchive: func(base, _ string, record *tallies) trailerAnswerer {
		client := newArchiveClient(base)
		client.recordTo(record)
		return newArchiveTrailerAnswerer(client)
	},
}

// The line, in the order the Library's own spec.sources names the blocks.
func newTrailerLine(blocks []string, value func(string) string, record *tallies) *trailerLine {
	return &trailerLine{answerers: recordingAnswerers(blocks, value, record, trailerAnswerers)}
}

// One title's ask: every answerer, because the trailers of a title are the
// union of what the providers hold, never the first answer alone. A provider
// that is down leaves the other blocks their answer, so the error stands only
// where no block answered at all.
func (l *trailerLine) ask(ctx context.Context, title trailerTitle) ([]trailerEntry, []string, error) {
	// Every answerer is asked at once, so one title costs the slowest provider
	// and not the sum of them. Each provider paces itself, so the asks never
	// share a slot. Each goroutine writes the slot of its own answerer, so the
	// answers need no lock.
	held := make([][]trailerEntry, len(l.answerers))
	failures := make([]error, len(l.answerers))
	var asking sync.WaitGroup
	for at, one := range l.answerers {
		asking.Add(1)
		go func() {
			defer asking.Done()
			held[at], failures[at] = one.trailers(ctx, title)
		}()
	}
	asking.Wait()

	// The slots are read in the line's order, so the answer does not depend on
	// which answerer finished first.
	var entries []trailerEntry
	var blocks []string
	var failure error
	for at, one := range l.answerers {
		if failures[at] != nil {
			if failure == nil {
				failure = failures[at]
			}
			continue
		}
		if len(held[at]) == 0 {
			continue
		}
		entries = append(entries, held[at]...)
		blocks = append(blocks, one.providerBlock())
	}
	if len(entries) == 0 {
		return nil, nil, failure
	}
	return entries, blocks, nil
}

// The line is built once for the container, so the settings a provider states
// are read once. A container with no answerer at all is a manifest to repair,
// because the operator creates it only where a source serves the fact.
func (e *enricher) trailerFact(ctx context.Context) error {
	if e.trailers == nil {
		e.trailers = newTrailerLine(commaNames(os.Getenv(librarySourcesVariable)), os.Getenv, e.tallies)
	}
	if len(e.trailers.answerers) == 0 {
		return fmt.Errorf("no provider key reached this container, and the %s fact cannot ask without one", factTrailer)
	}
	return e.trailerGap(ctx, e.trailers)
}

// How many titles the trailer gap asks about at once.
// How many titles one gap asks about at once. An Internet Archive search
// costs about 1.7 s and a metadata read about 3 s, so a title waits on the
// providers and not on this container. Four titles in flight keep the
// providers' own pacers busy without a burst.
const trailerWorkers = 4

// The workers of one gap write one log. A writer that is not goroutine-safe
// takes one line at a time behind this lock.
type serialLog struct {
	mutex sync.Mutex
	to    io.Writer
}

func (s *serialLog) Write(line []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.to.Write(line)
}

// What the workers of one gap share: the count of the titles a provider
// named, and the first failure, which stops the feed.
type trailerRun struct {
	mutex   sync.Mutex
	found   int
	failure error
	stopped chan struct{}
}

func (r *trailerRun) note() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.found++
}

// The first failure is the one the run reports, and it closes the feed so no
// worker takes another title.
func (r *trailerRun) fail(err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.failure != nil {
		return
	}
	r.failure = err
	close(r.stopped)
}

// The titles of one gap, fed to a pool of workers. The ids of one gap are
// unique, so no two workers write one title's folder, and the first failure
// closes the feed so no worker takes another title.
func (e *enricher) titlePool(ctx context.Context, ids []string, workers int,
	work func(ctx context.Context, run *trailerRun, id string)) *trailerRun {
	if e.log != nil {
		e.log = &serialLog{to: e.log}
	}
	run := &trailerRun{stopped: make(chan struct{})}
	titles := make(chan string)
	var pool sync.WaitGroup
	for range workers {
		pool.Add(1)
		go func() {
			defer pool.Done()
			for id := range titles {
				work(ctx, run, id)
			}
		}()
	}
feeding:
	for _, id := range ids {
		select {
		case titles <- id:
		case <-run.stopped:
			break feeding
		}
	}
	close(titles)
	pool.Wait()
	return run
}

// A catalog read that fails ends the container, because the gap list is the
// work. One title that fails records an error attempt, and the run carries on
// to the next. trailerWorkers titles are asked about at once.
func (e *enricher) trailerGap(ctx context.Context, line *trailerLine) error {
	ids, err := e.gaps(ctx, factTrailer, time.Now().UTC())
	if err != nil {
		return err
	}
	run := e.titlePool(ctx, ids, trailerWorkers,
		func(ctx context.Context, run *trailerRun, id string) {
			e.trailerGapTitle(ctx, line, run, id)
		})
	if run.failure != nil {
		return run.failure
	}
	e.logf("named the trailers of %d of the %d titles the gap held", run.found, len(ids))
	return nil
}

// One title of the gap: the catalog read, the scope test, and the ask.
func (e *enricher) trailerGapTitle(ctx context.Context, line *trailerLine, run *trailerRun, id string) {
	if err := ctx.Err(); err != nil {
		run.fail(err)
		return
	}
	item, held, err := e.catalog.identityItem(ctx, e.library, id)
	if err != nil {
		run.fail(err)
		return
	}
	if !held || !e.inScope(item.path) {
		return
	}
	if e.trailerOne(ctx, line, item) {
		run.note()
	}
}

// One title's ask and the record of it. The whole list is replaced, because
// the ledger says which trailers the providers hold now.
func (e *enricher) trailerOne(ctx context.Context, line *trailerLine, item identityItem) bool {
	folder := filepath.Join(e.root, item.path)
	entries, blocks, err := line.ask(ctx, e.trailerTitle(item, folder))
	if err != nil {
		e.logf("could not read the trailers of %s: %v", item.id, err)
		e.recordTrailers(folder, nil, nil, attemptError)
		return false
	}
	if len(entries) == 0 {
		e.recordTrailers(folder, []trailerEntry{}, nil, attemptNothing)
		return false
	}
	e.recordTrailers(folder, sortedTrailers(trimTrailers(entries)), blocks, attemptFound)
	return true
}

// The ids a provider keys on come from the .nfo file, where the identity fact
// wrote every one of them. The title and the year come off the catalog, for a
// provider keyed by search. A folder with no .nfo file has no id, which is
// not an error.
func (e *enricher) trailerTitle(item identityItem, folder string) trailerTitle {
	title := trailerTitle{kind: e.kind, title: item.title, year: item.year,
		languages: commaNames(os.Getenv(libraryLanguagesVariable))}
	nfoPath, _ := identityNFO(e.kind, folder)
	document, err := os.ReadFile(nfoPath)
	if err != nil {
		return title
	}
	title.ids = nfoIDs(document)
	return title
}

// How many entries of one provider one title's list holds at most.
const trailersPerProvider = 5

// One entry per provider and folded name, and at most trailersPerProvider
// entries of each provider. A name two providers hold is two entries, because
// they are two videos. The order the entries arrived in survives.
func trimTrailers(entries []trailerEntry) []trailerEntry {
	collapsed := make([]trailerEntry, 0, len(entries))
	first := map[string]int{}
	for _, entry := range entries {
		name := entry.Provider + "\n" + foldTitle(entry.Name)
		at, seen := first[name]
		if !seen {
			first[name] = len(collapsed)
			collapsed = append(collapsed, entry)
			continue
		}
		if betterTrailer(entry, collapsed[at]) {
			collapsed[at] = entry
		}
	}
	return cappedTrailers(collapsed)
}

// Which of two entries of one name the list keeps: the higher score, then the
// earlier date, with no date last, then the one seen first.
func betterTrailer(entry, held trailerEntry) bool {
	if entry.Score != held.Score {
		return entry.Score > held.Score
	}
	if entry.Published == "" || held.Published == "" {
		return entry.Published != "" && held.Published == ""
	}
	return entry.Published < held.Published
}

// The highest scores of each provider, ties kept in the order they arrived.
func cappedTrailers(entries []trailerEntry) []trailerEntry {
	held := map[string][]int{}
	for at, entry := range entries {
		held[entry.Provider] = append(held[entry.Provider], at)
	}
	dropped := map[int]bool{}
	for _, positions := range held {
		if len(positions) <= trailersPerProvider {
			continue
		}
		slices.SortStableFunc(positions, func(a, b int) int {
			return cmp.Compare(entries[b].Score, entries[a].Score)
		})
		for _, at := range positions[trailersPerProvider:] {
			dropped[at] = true
		}
	}
	kept := make([]trailerEntry, 0, len(entries)-len(dropped))
	for at, entry := range entries {
		if !dropped[at] {
			kept = append(kept, entry)
		}
	}
	return kept
}

// The order a person reads the list in: the provider, then the score, highest
// first, then the provider's own key.
func sortedTrailers(entries []trailerEntry) []trailerEntry {
	sorted := slices.Clone(entries)
	slices.SortStableFunc(sorted, func(a, b trailerEntry) int {
		if order := cmp.Compare(a.Provider, b.Provider); order != 0 {
			return order
		}
		if order := cmp.Compare(b.Score, a.Score); order != 0 {
			return order
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return sorted
}

// The list and the attempt are one write of one file, so a reader never sees
// an answer without its attempt. A provider that was down leaves the list as
// it is, because a failed ask says nothing about which trailers the title
// has.
func (e *enricher) recordTrailers(folder string, entries []trailerEntry, blocks []string, result string) {
	e.tallies.add(tallyAttempts, 1, "fact", factTrailer, "result", result)
	now := time.Now().UTC()
	err := e.writer.updateLikenLedger(folder, factTrailer, func(ledger *likenLedger) {
		if result != attemptError {
			ledger.Trailers = entries
		}
		ledger.noteAttempt(likenAttempt{
			Path: likenSelfPath, At: now, Result: result, Provider: blocks,
		})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v",
			factTrailer, relativePath(e.root, folder), err)
	}
	e.writeRows(factTrailer, folder, result == attemptFound)
}

// The trailer gap reads no table of its own. Every identified title is asked
// again once its last attempt has passed that attempt's own window, because a
// provider gains trailers over time and drops them.
func trailerGapQuery() string {
	return `SELECT id FROM (` +
		`SELECT library, id FROM movies WHERE id NOT LIKE 'movie:path:%' ` +
		`UNION ALL SELECT library, id FROM series WHERE id NOT LIKE 'series:path:%') AS items ` +
		`WHERE library = ?1 AND (` + attemptClause(factTrailer, "id") +
		beforeReleaseClause(factTrailer, "id") + `)`
}
