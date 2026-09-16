package main

// The cleanup role is the container of one cleanup Job. It deletes
// one departed library's rows out of the namespace's catalog, through its
// own agent's loopback API, and replication carries the deletes to every
// peer. The API binds loopback alone, which is why a pod does this work
// and the operator cannot.
//
// The sweep is one pass, not a loop. The Job writes its own runs
// row as its last write and waits for a catalog pod to confirm it, because
// an agent that receives SIGTERM drops whatever broadcasts it still holds.
// The confirmation is what says a standing pod holds the deletes.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// The argument that selects this role, the way scanMode selects the
// scanner. The operator writes it over the image's entrypoint.
const cleanupMode = "cleanup"

// cleanupTimeout bounds each request of one sweep, so an agent that
// stops answering cannot hold a sweep open forever.
var cleanupTimeout = 2 * time.Minute

// One cleanup container: the library it deletes, the catalog it
// deletes through, how long it waits to be confirmed, and the log it
// writes.
type sweeper struct {
	library        string
	job            string
	catalog        *Catalog
	handoffTimeout time.Duration
	log            io.Writer
}

// The role's whole program: read the environment, sweep once, and
// end the process with what the Job left. A failure is a non-zero exit,
// so the Job fails, its rows stay on its own claim, and the retry carries
// them.
func runCleanup() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	sweep := newSweeper(os.Stdout)
	if err := sweep.runJob(stopped); err != nil {
		fmt.Fprintf(sweep.log, "library.liken.sh: the cleanup job failed: %v\n", err)
		stop()
		os.Exit(1)
	}
}

// newSweeper reads the library to sweep from the environment, the
// only place a container with no API credential can learn it, and
// says so in the pod log before the first sweep.
func newSweeper(log io.Writer) *sweeper {
	namespace := os.Getenv(libraryNamespaceVariable)
	name := os.Getenv(libraryNameVariable)
	api := os.Getenv(catalogAPIVariable)
	if api == "" {
		api = defaultCatalogAPI
	}

	fmt.Fprintf(log, "library.liken.sh: sweeping %s/%s out of the catalog\n", namespace, name)

	return &sweeper{
		library:        libraryKey(namespace, name),
		job:            os.Getenv(jobNameVariable),
		catalog:        NewCatalog(api, &http.Client{Timeout: cleanupTimeout}),
		handoffTimeout: handoffTimeout(os.Getenv(handoffTimeoutVariable)),
		log:            log,
	}
}

// The whole of a cleanup Job: take every row the library holds,
// including the runs and confirmations of every other worker, then write
// its own run as the last row the agent has to broadcast, and wait for a
// catalog pod to confirm it.
func (s *sweeper) runJob(ctx context.Context) error {
	started := time.Now().UTC()
	if err := s.sweep(ctx); err != nil {
		return err
	}

	run := libraryRun{Worker: workerCleanup, Job: s.job, Started: started, Finished: time.Now().UTC()}
	return handOff(ctx, s.catalog, s.library, run, s.log, s.handoffTimeout)
}

// Deletes every row of the library in every table, the runs of
// every other worker with them, so the only row this library holds after
// the sweep is the one the Job writes next.
func (s *sweeper) sweep(ctx context.Context) error {
	removed, err := s.catalog.SweepLibrary(ctx, s.library)
	if err != nil {
		fmt.Fprintf(s.log, "library.liken.sh: could not sweep %s: %v\n", s.library, err)
		return fmt.Errorf("sweeping %s: %w", s.library, err)
	}
	runs, err := s.catalog.DeleteRuns(ctx, s.library)
	if err != nil {
		fmt.Fprintf(s.log, "library.liken.sh: could not sweep the runs of %s: %v\n", s.library, err)
		return fmt.Errorf("sweeping the runs of %s: %w", s.library, err)
	}
	// The confirmations go with the runs they answer. This Job's own
	// confirmation is written after the sweep, so the sweep never takes it.
	confirmations, err := s.catalog.DeleteConfirmations(ctx, s.library)
	if err != nil {
		fmt.Fprintf(s.log, "library.liken.sh: could not sweep the confirmations of %s: %v\n", s.library, err)
		return fmt.Errorf("sweeping the confirmations of %s: %w", s.library, err)
	}
	fmt.Fprintf(s.log, "library.liken.sh: swept %s: %d rows removed\n",
		s.library, removed+runs+confirmations)
	return nil
}
