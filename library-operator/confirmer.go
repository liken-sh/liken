package main

// The confirmer is the container beside every standing catalog
// agent. It follows the finished runs of the namespace, and for each one it
// reads whether its own copy holds every version that Job's agent wrote up
// to the version the run names. When it does, it writes its confirmations
// row, and the Job exits on it. It holds no Kubernetes credential, it
// answers on no port, and it never exits on its own.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// The argument that selects this role, the way reportMode selects
// the reporter. The operator writes it over the image's entrypoint.
const confirmMode = "confirm"

// How long a run that is not held yet waits before the confirmer
// reads again. Gossip fills a gap in seconds, so two seconds costs a few
// reads per run. A test shortens it.
var confirmerRecheck = 2 * time.Second

// The runs a confirmer acts on: the finished ones that name the
// write their Job made. A run with version zero is one whose Job has not
// written the answer of its own write yet.
const confirmerRunsQuery = `SELECT library, worker, job, actor, version FROM runs WHERE finished > 0 AND version > 0`

// The columns the confirmer reads out of a runs row by name,
// because the agent's matcher prepends the primary key to the projection.
const (
	runWorkerColumn  = "worker"
	runJobColumn     = "job"
	runActorColumn   = "actor"
	runVersionColumn = "version"
)

// One finished run, as the confirmer holds it while it waits for
// its own copy to catch up.
type finishedRun struct {
	library string
	worker  string
	job     string
	actor   string
	version int64
}

// The key one run is held under: the columns a confirmations row is
// keyed by, and the write that run names. A retried pod of the same Job
// writes a version of its own, and a key that stopped at the Job name would
// read that retry as a run this process had already settled.
func (r finishedRun) key() string {
	return r.library + "\x1f" + r.worker + "\x1f" + r.job +
		"\x1f" + r.actor + "\x1f" + strconv.FormatInt(r.version, 10)
}

// The runs row one run was read from. The runs table holds one row per
// library and worker, so a newer run of the same row replaces the older one,
// and no Job waits on the older run any more.
func (r finishedRun) row() string {
	return r.library + "\x1f" + r.worker
}

// One confirmer: its pod name, the catalog it reads, and the runs
// it has confirmed and the ones it still waits on. The confirmed set is
// keyed by run and the pending set by runs row.
type confirmer struct {
	name    string
	catalog *Catalog
	log     io.Writer

	// One mutex covers both sets, because the run stream reads them
	// on its own goroutine and the recheck reads them on another.
	mutex     sync.Mutex
	confirmed map[string]bool
	pending   map[string]finishedRun
}

// RunConfirm is the confirm role's whole program: read the
// environment and confirm until the kubelet stops the container.
func runConfirm() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	newConfirmer(os.Stdout).serve(stopped)
}

// NewConfirmer reads the pod's own name and the agent's address out
// of the environment, the only place a pod with no API credential learns
// them. The pod name is the key of the confirmations row this container
// writes, so two copies of one catalog write two rows and never one counter
// they would merge.
func newConfirmer(log io.Writer) *confirmer {
	api := os.Getenv(catalogAPIVariable)
	if api == "" {
		api = defaultCatalogAPI
	}
	name := os.Getenv(podNameVariable)

	fmt.Fprintf(log, "library.liken.sh: confirming runs against the copy on %s\n", name)

	return &confirmer{
		name: name,
		// No client timeout, because the run stream stays open for the life
		// of the pod. Every read bounds itself with a context instead.
		catalog:   NewCatalog(api, &http.Client{}),
		log:       log,
		confirmed: map[string]bool{},
		pending:   map[string]finishedRun{},
	}
}

// Serve holds the run stream and the recheck open until the context
// ends.
func (c *confirmer) serve(ctx context.Context) {
	var rechecking sync.WaitGroup
	rechecking.Add(1)
	go func() {
		defer rechecking.Done()
		c.recheckWhilePending(ctx)
	}()

	c.follow(ctx)
	rechecking.Wait()
}

// Follow reads every finished run the catalog holds and every
// change to one, and opens the stream again after a backoff when it ends.
// Nothing the catalog answers ends this loop. Only the context does.
func (c *confirmer) follow(ctx context.Context) {
	backoff := reportMinBackoff
	for ctx.Err() == nil {
		reached := false
		err := c.catalog.subscribe(ctx, confirmerRunsQuery, nil,
			func() { reached = true },
			func(columns []string, cells []any, deleted bool) { c.noteRun(ctx, columns, cells, deleted) })
		if err != nil && ctx.Err() == nil {
			c.logf("the run stream ended: %v", err)
		}
		// The events between one stream and the next are gone, deletes
		// among them, and the next stream's snapshot sends every run that
		// still exists. So the pending set is emptied when the stream ends,
		// and no run of the ended stream stays pending through the backoff
		// for the recheck to confirm.
		c.forget()
		if reached {
			backoff = reportMinBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !reached {
			backoff = min(backoff*2, reportMaxBackoff)
		}
	}
}

// RecheckWhilePending reads the copy again for every run it could
// not confirm, until the context ends. A run reaches the pending set when
// the versions behind it have not arrived yet, which gossip answers within
// seconds. No event covers the arrival: the proof is in the cr-sqlite
// bookkeeping, and a subscription or an update stream follows only the
// catalog's own tables. So the recheck is a backstop timer. It reads
// nothing when no run is pending. A pending run leaves the set when it is
// confirmed, when a newer run of its row replaces it, or when its row
// leaves the query's result. The set is emptied when a run stream ends,
// and the next stream's snapshot fills it again. A confirmed run never
// enters the set again. A run whose versions never arrive stays pending
// for as long as its row names it, at up to three small reads of the
// local copy every two seconds.
func (c *confirmer) recheckWhilePending(ctx context.Context) {
	ticker := time.NewTicker(confirmerRecheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, run := range c.waiting() {
			c.recheck(ctx, run)
		}
	}
}

// NoteRun reads one streamed runs row and settles it. A row this
// image cannot read is skipped, so one row of a shape this operator did not
// write never costs the confirmer the rest of the table.
//
// The pending set holds at most one run per runs row: the newest run
// the stream carried for it, while that run waits on its versions. Every
// event for a row decides the row's entry alone, with one rule:
//
//   - A row event replaces the entry with its run, or clears the entry
//     when its run is confirmed at once.
//   - A delete clears the entry, whatever run it carries.
//
// The agent buffers changes before it streams them, so a row can move
// from one run to the next in one change, and a delete can carry a run
// other than the pending one. A delete says the row left the query's
// result: a cleanup Job deleted it, or a hand-off's first write set its
// version to zero, which the query does not read. No Job waits on the
// row's old run after either one, and a confirmation written for a
// deleted library's run would keep the library's key in the catalog
// after the library is gone.
func (c *confirmer) noteRun(ctx context.Context, columns []string, cells []any, deleted bool) {
	run, ok := decodeFinishedRun(columns, cells)
	if !ok {
		return
	}
	if deleted {
		c.clearRow(run.row())
		return
	}
	c.place(run, c.settle(ctx, run))
}

// DecodeFinishedRun reads one runs row by column name into the run
// a confirmer acts on. A row with no library, no Job, or no version names
// no write to hold.
func decodeFinishedRun(columns []string, cells []any) (finishedRun, bool) {
	library, _ := namedString(columns, cells, runsLibraryColumn)
	worker, _ := namedString(columns, cells, runWorkerColumn)
	job, _ := namedString(columns, cells, runJobColumn)
	actor, _ := namedString(columns, cells, runActorColumn)
	version, held := cellNamed(columns, cells, runVersionColumn)
	if !held || library == "" || job == "" {
		return finishedRun{}, false
	}
	run := finishedRun{library: library, worker: worker, job: job, actor: actor,
		version: cellNumber(version)}
	return run, run.version > 0
}

// NamedString reads one named column of a streamed row as a string.
func namedString(columns []string, cells []any, name string) (string, bool) {
	cell, held := cellNamed(columns, cells, name)
	if !held {
		return "", false
	}
	value, ok := cell.(string)
	return value, ok
}

// Settle confirms one run the run stream carries where this copy holds
// it, and reports whether the run is confirmed. The stream delivers its
// events on one goroutine, so no delete of the run arrives while this reads.
func (c *confirmer) settle(ctx context.Context, run finishedRun) bool {
	return c.settleIf(ctx, run, func() bool { return true })
}

// Recheck confirms one run of the pending set where this copy holds it.
// The recheck reads a copy of the set, so the stream can replace or delete
// the run while the recheck reads the catalog. The run is confirmed only
// while it is still the pending run of its row. A run the recheck cannot
// confirm stays pending, and the recheck never holds it again.
func (c *confirmer) recheck(ctx context.Context, run finishedRun) {
	c.settleIf(ctx, run, func() bool { return c.pending[run.row()] == run })
}

// SettleIf confirms one run where this copy holds it and current answers
// true. Current runs with the mutex held.
func (c *confirmer) settleIf(ctx context.Context, run finishedRun, current func() bool) bool {
	if c.done(run.key()) {
		return true
	}
	confirmed, err := c.confirm(ctx, run, current)
	if err != nil {
		c.logf("could not confirm the %s run %s of %s: %v", run.worker, run.job, run.library, err)
	}
	if !confirmed {
		return false
	}
	c.mark(run)
	c.logf("confirmed the %s run %s of %s", run.worker, run.job, run.library)
	return true
}

// Confirm reads whether this copy holds every version the run names
// and writes the confirmations row where it does. A run this pod already
// confirmed is confirmed again by nobody, because a second write of the row
// would be a version every peer has to carry for nothing.
//
// The write runs with the mutex held, after current answers true, so a
// delete that the stream carries waits for the write and never falls
// between the check and the write.
//
// Every read and write of one confirmation shares one bound. The
// confirmer's client has no timeout, because the run stream stays open,
// and the stream's own goroutine confirms the runs it carries. An agent
// that stops answering then fails one confirmation, and never holds the
// stream or the mutex for ever.
func (c *confirmer) confirm(ctx context.Context, run finishedRun, current func() bool) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, catalogWriteTimeout)
	defer cancel()
	already, err := c.catalog.confirmedBy(ctx, run.library, run.worker, run.job, c.name, run.version)
	if err != nil || already {
		return already, err
	}
	held, err := versionsHeld(ctx, c.catalog, run.actor, run.version)
	if err != nil || !held {
		return false, err
	}
	if written, err := c.writeIf(ctx, run, current); !written {
		return false, err
	}
	// The older Jobs of this worker leave with this one's arrival, so
	// the table holds the newest run of each worker and not one row per Job
	// that ever ran. A prune that fails leaves rows behind and never the
	// confirmation the Job waits on.
	_, err = c.catalog.pruneConfirmations(ctx, run.library, run.worker, run.job)
	return true, err
}

// Done reports whether this pod has already confirmed one run in
// this process.
func (c *confirmer) done(key string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.confirmed[key]
}

// Mark records a run as confirmed and takes it out of the set the
// recheck reads, where it is still its row's pending run.
func (c *confirmer) mark(run finishedRun) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.confirmed[run.key()] = true
	if c.pending[run.row()] == run {
		delete(c.pending, run.row())
	}
}

// Place sets the pending entry of one run's row after the stream carried
// the run: the run itself while it waits, or nothing once it is confirmed.
// The newest run of the row replaces the last one: a Job writes its run
// again while it waits, and a retried pod of a Job that timed out writes a
// run with a write of its own. Without the replacement, the run of every
// pod that timed out stays pending for the life of this pod.
//
// A run that is already confirmed clears the entry. The recheck can
// confirm a run while the stream's read of the same run still runs, and
// the stream's place then arrives after the confirmation.
func (c *confirmer) place(run finishedRun, confirmed bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if confirmed || c.confirmed[run.key()] {
		delete(c.pending, run.row())
		return
	}
	c.pending[run.row()] = run
}

// WriteIf writes the confirmations row of one run while current answers
// true, and reports whether it wrote the row.
func (c *confirmer) writeIf(ctx context.Context, run finishedRun, current func() bool) (bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if !current() {
		return false, nil
	}
	err := c.catalog.UpsertConfirmation(ctx, run.library, run.worker, run.job,
		c.name, run.version, time.Now().UTC())
	return err == nil, err
}

// ClearRow takes one row's run out of the pending set.
func (c *confirmer) clearRow(row string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.pending, row)
}

// Forget empties the pending set.
func (c *confirmer) forget() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	clear(c.pending)
}

// Waiting is the runs the recheck reads, copied out from under the
// mutex so the reads run with nothing locked.
func (c *confirmer) waiting() []finishedRun {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	runs := make([]finishedRun, 0, len(c.pending))
	for _, run := range c.pending {
		runs = append(runs, run)
	}
	return runs
}

// Logf writes one line under the shared prefix, or nothing when the
// confirmer was built without a log.
func (c *confirmer) logf(format string, args ...any) {
	if c.log == nil {
		return
	}
	fmt.Fprintf(c.log, "library.liken.sh: "+format+"\n", args...)
}
