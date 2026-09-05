package main

// The runs table is how a Job proves its rows reached the standing
// catalog. A Corrosion agent drops the broadcasts it has not sent when
// it receives SIGTERM, and its peers fill a gap only by pulling from
// the agent that holds the rows. So a Job's agent must not exit until
// the standing pod holds what the Job wrote. Every worker writes one
// runs row per library as its last catalog write, the standing pod's
// reporter publishes that row back in the library's report, and the
// Job exits only once it reads its own name there.
//
// The last write is not the last row to arrive, because the
// agent applies a version as it arrives and fills the gaps behind it
// by pulling from the source, so the report has to carry the Job's own
// counts as well as its run.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
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

// The environment every Job container carries: the Job's own name,
// which its runs row carries so the echo can name it, and how long it
// waits for the echo before it fails.
const (
	jobNameVariable     = "JOB_NAME"
	echoTimeoutVariable = "ECHO_TIMEOUT"
)

// The wait a Job gives the echo when the environment names none. The
// echo arrives within the update stream's latency in the normal case,
// and two minutes covers a catalog pod that is restarting.
const defaultEchoTimeout = 2 * time.Minute

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
	// Commit is the commit that run read, for a library whose storage is
	// git. A run that failed carries the commit the last good run read,
	// so a forge outage does not lose the mark the next scan compares
	// against. The column is commit_id, because commit is a word SQLite
	// reserves.
	Commit string `json:"commit,omitempty"`
	// Failure is why that run failed, and empty for a run that finished
	// its work. status.phase reads Failed while the scan run carries one.
	Failure string `json:"failure,omitempty"`
}

// echoTimeout reads the echo wait out of the environment. An empty,
// unreadable, or negative value takes the default rather than failing
// the Job, because the wait is a bound and not a fact about the volume.
func echoTimeout(raw string) time.Duration {
	if raw == "" {
		return defaultEchoTimeout
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return defaultEchoTimeout
	}
	return timeout
}

// echoBusAddress reads the broker address every Job role needs. A Job
// with no broker can never hear the echo that ends its wait, so a role
// refuses to start on an empty address, rather than write the catalog
// and hold the claim for the whole timeout. The one log line names the
// variable the pod is missing.
func echoBusAddress(log io.Writer) (string, error) {
	address := os.Getenv(busAddressVariable)
	if address == "" {
		fmt.Fprintf(log, "library.liken.sh: %s is empty, and a job with no broker cannot hear its echo\n",
			busAddressVariable)
		return "", fmt.Errorf("%s names no broker", busAddressVariable)
	}
	return address, nil
}

// The read of the whole runs table. It needs no LIMIT, because the
// table holds one row per library and worker.
const runsQuery = `SELECT library, worker, job, started, finished, unidentified, removed, commit_id, failure FROM runs`

// The column the reporter reads out of a runs change to learn which
// library's report to publish again.
const runsLibraryColumn = "library"

// UpsertRun writes one worker's run of one library in place. The conflict
// target is the whole primary key, and the update names no key column,
// because cr-sqlite reads a change to a key column as a delete and a
// create. A run that names no commit keeps the one the row already
// holds: a scan that could not read the mark and a scan that failed both
// name none, so the mark the next scan compares against stands, and a
// movies or series run always names none, so its column stays empty.
func (c *Catalog) UpsertRun(ctx context.Context, library string, run libraryRun) error {
	_, err := c.apply(ctx, []statement{{
		sql: `INSERT INTO runs (library, worker, job, started, finished, unidentified, removed, commit_id, failure) ` +
			`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
			`ON CONFLICT (library, worker) DO UPDATE SET ` +
			`job = excluded.job, started = excluded.started, finished = excluded.finished, ` +
			`unidentified = excluded.unidentified, removed = excluded.removed, ` +
			`commit_id = CASE WHEN excluded.commit_id = '' THEN commit_id ELSE excluded.commit_id END, ` +
			`failure = excluded.failure`,
		params: []any{library, run.Worker, run.Job, runSeconds(run.Started), runSeconds(run.Finished),
			run.Unidentified, run.Removed, run.Commit, run.Failure},
	}})
	return err
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
	return c.subscribe(ctx, runsQuery, onReady, func(columns []string, cells []any) {
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
	if len(cells) < 9 {
		return "", libraryRun{}, false
	}
	library, ok := cells[0].(string)
	if !ok {
		return "", libraryRun{}, false
	}
	worker, _ := cells[1].(string)
	job, _ := cells[2].(string)
	commit, _ := cells[7].(string)
	failure, _ := cells[8].(string)
	return library, libraryRun{
		Worker:       worker,
		Job:          job,
		Started:      runTime(cellNumber(cells[3])),
		Finished:     runTime(cellNumber(cells[4])),
		Unidentified: int(cellNumber(cells[5])),
		Removed:      int(cellNumber(cells[6])),
		Commit:       commit,
		Failure:      failure,
	}, true
}

// lastScan reads the commit and the failure the last scan of one library
// left. The scan Job reads it before it clones, so a scan that finds the
// same commit writes no row, and a failed scan keeps the mark. A library
// with no scan run yet reads both as empty.
func (c *Catalog) lastScan(ctx context.Context, library string) (libraryRun, error) {
	run := libraryRun{Worker: workerScan}
	err := c.stream(ctx, `SELECT commit_id, failure FROM runs WHERE library = ? AND worker = ?`,
		[]any{library, workerScan}, func(cells []any) error {
			if len(cells) < 2 {
				return nil
			}
			run.Commit, _ = cells[0].(string)
			run.Failure, _ = cells[1].(string)
			return nil
		})
	return run, err
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

// runOf reads one worker's run out of a library's runs.
func runOf(runs []libraryRun, worker string) (libraryRun, bool) {
	for _, run := range runs {
		if run.Worker == worker {
			return run, true
		}
	}
	return libraryRun{}, false
}

// echoWaiter is one Job's wait for the report that names its own run.
// The report comes from the standing catalog pod, so a report that
// names this Job proves that pod holds every row the Job wrote.
type echoWaiter struct {
	topic  string
	worker string
	job    string
	// The counts the report has to carry beside the run, or nil
	// where the Job could not read them and waits on the run alone.
	counts *echoCounts

	once   sync.Once
	echoed chan struct{}
}

// What the Job's own agent held for this library after the Job's
// last write, which the standing pod's report has to match.
type echoCounts struct {
	items int
	files int
}

func newEchoWaiter(topic, worker, job string) *echoWaiter {
	return &echoWaiter{topic: topic, worker: worker, job: job, echoed: make(chan struct{})}
}

// Sets the counts the echo has to carry. It is called before the
// wait starts, which is before the bus handler can read them.
func (w *echoWaiter) expect(items, files int) {
	w.counts = &echoCounts{items: items, files: files}
}

// note is the bus handler. A report on this library's topic whose run
// for this worker names this Job, with a finish time on it and the
// counts the Job expects, ends the wait. Every other message is
// ignored.
//
// The run row can reach the standing pod before the rows the Job
// wrote before it, because the agent applies a version when it arrives
// and fills the gaps behind it by pulling from the source. The counts
// are what say the gaps are filled.
func (w *echoWaiter) note(topic string, payload []byte) {
	if topic != w.topic {
		return
	}
	var report libraryReport
	if json.Unmarshal(payload, &report) != nil {
		return
	}
	run, held := runOf(report.Runs, w.worker)
	if !held || run.Job != w.job || run.Finished.IsZero() {
		return
	}
	if w.counts != nil && (report.Items != w.counts.items || report.Files != w.counts.files) {
		return
	}
	w.once.Do(func() { close(w.echoed) })
}

// wait runs the bus, subscribes, and waits for the echo. The wait is
// bounded by the timeout and by the context, so a Job that never hears
// an echo fails and Kubernetes retries it, rather than holding the
// claim forever.
func (w *echoWaiter) wait(ctx context.Context, bus *Bus, timeout time.Duration) error {
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

	bus.Subscribe(w.topic)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.echoed:
		return nil
	case <-timer.C:
		return fmt.Errorf("the catalog did not report the %s run of %s within %s", w.worker, w.job, timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}
