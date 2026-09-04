package main

// arrivalfact.go is the arrival fact: the enricher concern that writes the
// arrival ledger. It is a fact and not part of the walk because the scan Job
// mounts the volume read-only and the enrich Jobs mount it read-write. It
// asks no provider, because the file's own change time is the answer. It
// never rewrites an entry that exists, because the ledger is what makes the
// first sighting durable against every later sweep of the volume.

import (
	"context"
	"path/filepath"
	"time"
)

// The name of the container that runs this fact.
const arrivalContainerName = "arrival"

// The gap: a present video file whose files.arrived is zero, which is what
// the walk writes for a file the ledger holds no entry for, outside the
// attempt window. The fact's own row write and the next walk both close it.
func arrivalGapSQL() string {
	return `SELECT path FROM files ` +
		`WHERE library = ?1 AND type = '` + fileTypeVideo + `' AND present = 1 ` +
		`AND ` + gapClause(factArrival, "path", `arrived = 0`)
}

// One folder's work: the folder whose .liken directory holds the ledger, and
// the entry path of each gap file under it, in the gap's order.
type arrivalWork struct {
	folder  string
	entries []string
}

// The whole run. A catalog read that fails ends the container, because the
// gap list is the work. The gap is grouped by folder, because one folder is
// one file on the volume and one write, however many videos it holds.
func (e *enricher) arrivalFact(ctx context.Context) error {
	paths, err := e.gaps(ctx, factArrival, time.Now().UTC())
	if err != nil {
		return err
	}
	stamped := 0
	for _, work := range e.arrivalWork(paths) {
		if err := ctx.Err(); err != nil {
			return err
		}
		stamped += e.stampArrivals(ctx, work)
	}
	e.logf("stamped %d of the %d files with no arrival", stamped, len(paths))
	return nil
}

// The gap grouped by the folder that holds each file's ledger, in the order
// the gap named the folders. The files this Job's scope does not cover are
// left out.
func (e *enricher) arrivalWork(paths []string) []arrivalWork {
	var works []arrivalWork
	index := map[string]int{}
	for _, path := range paths {
		if !e.inScope(path) {
			continue
		}
		folder, entry := likenFolderFor(e.kind, filepath.Join(e.root, path))
		at, held := index[folder]
		if !held {
			at = len(works)
			index[folder] = at
			works = append(works, arrivalWork{folder: folder})
		}
		works[at].entries = append(works[at].entries, entry)
	}
	return works
}

// One folder: an entry with the change time for every gap file the ledger
// holds none for, an attempt per file, and one write of the file. An entry
// that exists is kept as it is. Then the rows: files.arrived and the
// folder's item rows, so added follows. A volume that refuses the write is
// an error attempt written straight to the catalog, because the ledger is
// the one file this fact may write and a refused volume cannot hold the
// attempt. That row stands until the next walk sweeps it, so a refused
// volume costs one try per walk and never one per run. The answer is how
// many files gained an entry.
func (e *enricher) stampArrivals(ctx context.Context, work arrivalWork) int {
	now := time.Now().UTC()
	stamped := 0
	err := e.writer.updateLikenLedger(work.folder, factArrival, func(ledger *likenLedger) {
		held := map[string]bool{}
		for _, entry := range ledger.Files {
			held[entry.Path] = true
		}
		for _, entry := range work.entries {
			result := attemptFound
			if !held[entry] {
				at, err := changeTime(filepath.Join(work.folder, entry))
				if err != nil {
					e.logf("could not read the change time of %s: %v", entry, err)
					result = attemptError
				} else {
					ledger.Files = append(ledger.Files, arrivalEntry{Path: entry, At: time.Unix(at, 0).UTC()})
					held[entry] = true
					stamped++
				}
			}
			ledger.noteAttempt(likenAttempt{Path: entry, At: now, Result: result})
		}
	})
	if err != nil {
		e.logf("could not write the arrival ledger at %s: %v", relativePath(e.root, work.folder), err)
		e.writeArrivalErrors(ctx, work, now)
		return 0
	}
	e.writeRows(factArrival, work.folder, true)
	return stamped
}

// The error attempt rows of a folder the volume refused, one per gap file,
// keyed the way the walk keys a file fact's attempt.
func (e *enricher) writeArrivalErrors(ctx context.Context, work arrivalWork, at time.Time) {
	if e.catalog == nil {
		return
	}
	var rows []attemptRow
	for _, entry := range work.entries {
		rows = append(rows, attemptRow{
			Library: e.library,
			Item:    relativePath(e.root, filepath.Join(work.folder, entry)),
			Fact:    factArrival,
			At:      at.Unix(),
			Result:  attemptError,
		})
	}
	if _, err := e.catalog.UpsertAttempts(ctx, rows); err != nil {
		e.logf("could not write the %s attempt rows of %s: %v", factArrival, relativePath(e.root, work.folder), err)
	}
}
