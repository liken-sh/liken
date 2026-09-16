package main

// The runs table is the record of what each worker last did to a
// library, and the row a Job hands off on. Every worker writes one runs
// row per library as its last catalog write, and handoff.go holds the wait
// that follows it.

import (
	"context"
	"slices"
	"strings"
	"time"
)

// The workers that write a runs row today. The word is the row's
// second key column, so one library holds one row per worker.
//
// A folder scan is its own worker, because it reads one folder
// and its counts do not describe the whole volume, so its row must
// never overwrite the full walk's row.
const (
	workerScan    = "scan"
	workerRescan  = "rescan"
	workerCleanup = "cleanup"
)

// One worker's last run of one library, as the runs table holds it
// and as the reporter publishes it. Finished is zero while the run is
// in progress. Unidentified and Removed are the scan worker's counts,
// and zero for every other worker.
type libraryRun struct {
	Worker       string    `json:"worker"`
	Job          string    `json:"job"`
	Started      time.Time `json:"started"`
	Finished     time.Time `json:"finished,omitempty"`
	Unidentified int       `json:"unidentified,omitempty"`
	Removed      int       `json:"removed,omitempty"`
	// Failure is why that run failed, and empty for a run that finished
	// its work. status.phase reads Failed while the scan run carries one.
	Failure string `json:"failure,omitempty"`
	// The write this run was made by: the agent that applied the
	// finished-run write, and the db version it gave that write. A
	// confirmer that holds every version of that actor up to this one
	// holds every row the Job wrote. Both are zero until the Job writes
	// the row again with the answer its first write gave.
	Actor   string `json:"actor,omitempty"`
	Version int64  `json:"version,omitempty"`
}

// The read of the whole runs table. It needs no LIMIT, because the
// table holds one row per library and worker.
const runsQuery = `SELECT library, worker, job, started, finished, unidentified, removed, failure, actor, version FROM runs`

// The column the reporter reads out of a runs change to learn which
// library's report to publish again.
const runsLibraryColumn = "library"

// UpsertRun writes one worker's run of one library in place. The conflict
// target is the whole primary key, and the update names no key column,
// because cr-sqlite reads a change to a key column as a delete and a
// create.
//
// It answers with the agent that applied the write and the db
// version that agent gave it, which is what the Job writes back into the
// row and what a confirmer proves it holds.
func (c *Catalog) UpsertRun(ctx context.Context, library string, run libraryRun) (string, int64, error) {
	made, err := c.applyMade(ctx, []statement{{
		sql: `INSERT INTO runs (library, worker, job, started, finished, unidentified, removed, failure, actor, version) ` +
			`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
			`ON CONFLICT (library, worker) DO UPDATE SET ` +
			`job = excluded.job, started = excluded.started, finished = excluded.finished, ` +
			`unidentified = excluded.unidentified, removed = excluded.removed, ` +
			`failure = excluded.failure, actor = excluded.actor, version = excluded.version`,
		params: []any{library, run.Worker, run.Job, runSeconds(run.Started), runSeconds(run.Finished),
			run.Unidentified, run.Removed, run.Failure, run.Actor, run.Version},
	}})
	return made.actor, made.version, err
}

// DeleteRuns takes every worker's row for one library. The runs table
// holds one row per worker, so this is a handful of rows and never the
// batch a table of items needs.
func (c *Catalog) DeleteRuns(ctx context.Context, library string) (int, error) {
	return c.apply(ctx, []statement{{
		sql:    `DELETE FROM runs WHERE library = ?`,
		params: []any{library},
	}})
}

// Runs reads every run the catalog holds, keyed by library, each
// library's runs sorted by worker.
func (c *Catalog) Runs(ctx context.Context) (map[string][]libraryRun, error) {
	held := map[string][]libraryRun{}
	err := c.stream(ctx, runsQuery, nil, func(cells []any) error {
		library, run, ok := decodeRun(cells)
		if !ok {
			return nil
		}
		held[library] = append(held[library], run)
		return nil
	})
	for _, runs := range held {
		slices.SortFunc(runs, func(a, b libraryRun) int {
			return strings.Compare(a.Worker, b.Worker)
		})
	}
	return held, err
}

// subscribeRuns follows the runs table for the life of the context.
// It names the library of every row the opening snapshot holds and of
// every change after it, and the reporter publishes that library's
// report again on each one.
func (c *Catalog) subscribeRuns(ctx context.Context, onReady func(), onLibrary func(library string)) error {
	return c.subscribe(ctx, runsQuery, nil, onReady, func(columns []string, cells []any) {
		cell, held := cellNamed(columns, cells, runsLibraryColumn)
		if !held {
			return
		}
		if library, ok := cell.(string); ok && library != "" {
			onLibrary(library)
		}
	})
}

// decodeRun reads one runs row out of the cells the query streams, in
// the column order runsQuery names.
func decodeRun(cells []any) (string, libraryRun, bool) {
	if len(cells) < 10 {
		return "", libraryRun{}, false
	}
	library, ok := cells[0].(string)
	if !ok {
		return "", libraryRun{}, false
	}
	worker, _ := cells[1].(string)
	job, _ := cells[2].(string)
	failure, _ := cells[7].(string)
	actor, _ := cells[8].(string)
	return library, libraryRun{
		Worker:       worker,
		Job:          job,
		Started:      runTime(cellNumber(cells[3])),
		Finished:     runTime(cellNumber(cells[4])),
		Unidentified: int(cellNumber(cells[5])),
		Removed:      int(cellNumber(cells[6])),
		Failure:      failure,
		Actor:        actor,
		Version:      cellNumber(cells[9]),
	}, true
}

// A SQLite integer arrives from the API as a JSON number, so every
// count and every time reads back through float64.
func cellNumber(cell any) int64 {
	number, _ := cell.(float64)
	return int64(number)
}

// The runs table holds Unix seconds. A run that has not finished holds
// zero rather than a time, and runTime reads zero back as the zero time.
func runSeconds(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
}

func runTime(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

// An open run row whose Job is absent from the pass's Job list is a run whose
// pod died before it wrote its finish. Only a pod of a Job that exists writes
// a row, so a row that names a missing Job is always dead. Without this rule,
// one node reboot mid-run left a library in phase Enriching for ever, with
// every condition True and no Job ever scheduled again.
func runAbandoned(run libraryRun, jobs []Job, namespace, library string) bool {
	if !run.Finished.IsZero() {
		return false
	}
	for index := range jobs {
		job := &jobs[index]
		if job.Metadata.Namespace != namespace || job.Metadata.Labels[libraryLabelKey] != library {
			continue
		}
		if job.Metadata.Name == run.Job {
			return false
		}
	}
	return true
}

// runOf reads one worker's run out of a library's runs.
func runOf(runs []libraryRun, worker string) (libraryRun, bool) {
	for _, run := range runs {
		if run.Worker == worker {
			return run, true
		}
	}
	return libraryRun{}, false
}
