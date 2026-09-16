package main

// trailerfilerole.go is the trailers container's run: the gap of titles, the
// pick of one trailer and one of its files, the pull, and what it leaves in
// the ledger.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The name of the container that runs this fact.
const trailerFileContainerName = "trailers"

// What this fact counts: one row per attempt, by site and outcome, and the
// bytes every pull took.
const (
	tallyTrailerFetches    = "trailer_fetches"
	tallyTrailerFetchBytes = "trailer_fetch_bytes"
)

// How much memory this container may take. The pull streams onto the volume
// and the remux copies streams, so ffprobe's own 60 MB is the line it has to
// clear.
const trailersMemoryLimit = "256Mi"

// How many titles pull at once. A pull is bytes over the network, so two keep
// the link busy without filling the volume with temporaries.
const trailerFileWorkers = 2

// The line is built once for the container, so the settings a site states
// are read once.
func (e *enricher) trailerFileFact(ctx context.Context) error {
	if e.trailerFiles == nil {
		e.trailerFiles = newTrailerFetchLine(commaNames(os.Getenv(librarySourcesVariable)),
			os.Getenv, e.tallies)
	}
	if len(e.trailerFiles.sources) == 0 {
		return fmt.Errorf("no site this fact can fetch from reached this container, and the %s fact cannot pull without one", factTrailerFile)
	}
	e.sweepOldTallies(ctx, time.Now().UTC())
	return e.trailerFileGap(ctx, e.trailerFiles)
}

// A catalog read that fails ends the container, because the gap list is the
// work. One title that fails records an error attempt, and the run carries on.
func (e *enricher) trailerFileGap(ctx context.Context, line *trailerFetchLine) error {
	ids, err := e.gaps(ctx, factTrailerFile, time.Now().UTC())
	if err != nil {
		return err
	}
	run := e.titlePool(ctx, ids, trailerFileWorkers,
		func(ctx context.Context, run *trailerRun, id string) {
			e.trailerFileGapTitle(ctx, line, run, id)
		})
	if run.failure != nil {
		return run.failure
	}
	e.logf("pulled the trailer of %d of the %d titles the gap held", run.found, len(ids))
	return nil
}

// One title of the gap: the catalog reads, the scope test, and the pull.
func (e *enricher) trailerFileGapTitle(ctx context.Context, line *trailerFetchLine,
	run *trailerRun, id string) {
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
	rows, err := e.catalog.trailersOf(ctx, e.library, id)
	if err != nil {
		run.fail(err)
		return
	}
	ceiling, err := e.catalog.featureHeight(ctx, e.library, item.path)
	if err != nil {
		run.fail(err)
		return
	}
	if e.trailerFileOne(ctx, line, item, rows, ceiling) {
		run.note()
	}
}

// One title: the two picks, then the pull, then the record.
func (e *enricher) trailerFileOne(ctx context.Context, line *trailerFetchLine,
	item identityItem, rows []trailerRow, ceiling int) bool {
	folder := filepath.Join(e.root, item.path)
	row, held := pickTrailerRow(rows, line.sources)
	if !held {
		e.logf("no trailer of %s plays from a site this fact can fetch", item.id)
		e.recordTrailerFile(folder, "", nil, attemptNothing)
		return false
	}
	source := line.sources[row.Site]
	files, err := source.fetcher.files(ctx, row)
	if err != nil {
		e.logf("could not read the files of %s: %v", row.URL, err)
		e.recordTrailerFile(folder, row.Site, nil, attemptError)
		return false
	}
	file, held := pickTrailerFile(files, ceiling)
	if !held {
		e.logf("%s holds no video file of %s", row.Site, row.Key)
		e.recordTrailerFile(folder, row.Site, nil, attemptNothing)
		return false
	}
	e.logf("taking the %s of %s from %s: %dp, of the %d files it holds, under a feature of %dp",
		row.Kind, item.id, row.Site, file.Height, len(files), ceiling)

	entry, result := e.pullTrailerFile(ctx, source, item, row, file, folder)
	e.recordTrailerFile(folder, row.Site, entry, result)
	return result == attemptFound
}

// The entry and the attempt are one write of one file, so a reader never
// sees a record without its attempt.
func (e *enricher) recordTrailerFile(folder, site string, entry *trailerFileEntry, result string) {
	e.tallies.add(tallyTrailerFetches, 1, "site", site, "result", result)
	now := time.Now().UTC()
	err := e.writer.updateLikenLedger(folder, factTrailerFile, func(ledger *likenLedger) {
		if entry != nil {
			ledger.TrailerFile = entry
		}
		ledger.noteAttempt(likenAttempt{Path: likenSelfPath, At: now, Result: result})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v",
			factTrailerFile, relativePath(e.root, folder), err)
	}
	e.writeRows(factTrailerFile, folder, result == attemptFound)
}
