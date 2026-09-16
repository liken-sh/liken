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

// One confirmer: its pod name, the catalog it reads, and the runs
// it has confirmed and the ones it still waits on.
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
			func(columns []string, cells []any) { c.noteRun(ctx, columns, cells) })
		if err != nil && ctx.Err() == nil {
			c.logf("the run stream ended: %v", err)
		}
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
// seconds.
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
			c.settle(ctx, run)
		}
	}
}

// NoteRun reads one streamed runs row and settles it. A row this
// image cannot read is skipped, so one row of a shape this operator did not
// write never costs the confirmer the rest of the table.
func (c *confirmer) noteRun(ctx context.Context, columns []string, cells []any) {
	run, ok := decodeFinishedRun(columns, cells)
	if !ok {
		return
	}
	c.settle(ctx, run)
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

// Settle confirms one run where this copy holds it, and holds it for
// the next recheck where it does not.
func (c *confirmer) settle(ctx context.Context, run finishedRun) {
	if c.done(run.key()) {
		return
	}
	confirmed, err := c.confirm(ctx, run)
	if err != nil {
		c.logf("could not confirm the %s run %s of %s: %v", run.worker, run.job, run.library, err)
	}
	if !confirmed {
		c.hold(run)
		return
	}
	c.mark(run.key())
	c.logf("confirmed the %s run %s of %s", run.worker, run.job, run.library)
}

// Confirm reads whether this copy holds every version the run names
// and writes the confirmations row where it does. A run this pod already
// confirmed is confirmed again by nobody, because a second write of the row
// would be a version every peer has to carry for nothing.
func (c *confirmer) confirm(ctx context.Context, run finishedRun) (bool, error) {
	already, err := c.catalog.confirmedBy(ctx, run.library, run.worker, run.job, c.name, run.version)
	if err != nil || already {
		return already, err
	}
	held, err := versionsHeld(ctx, c.catalog, run.actor, run.version)
	if err != nil || !held {
		return false, err
	}
	if err := c.catalog.UpsertConfirmation(ctx, run.library, run.worker, run.job,
		c.name, run.version, time.Now().UTC()); err != nil {
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
// recheck reads.
func (c *confirmer) mark(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.confirmed[key] = true
	delete(c.pending, key)
}

// Hold keeps a run for the next recheck. The newest row replaces the
// last one, because a Job writes its run again while it waits.
func (c *confirmer) hold(run finishedRun) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.pending[run.key()] = run
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
