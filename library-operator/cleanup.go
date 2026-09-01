package main

// The cleanup role deletes one departed library's rows out of the
// namespace's catalog, through its own agent's loopback API, and
// replication carries the deletes to every peer. The API binds
// loopback alone, which is why a pod does this work and the operator
// cannot.
//
// The loop never ends on its own. Corrosion's sync is a pull: a peer
// fetches what it is missing, there is no acknowledgment to wait on,
// and an agent that receives SIGTERM drops whatever broadcasts it
// still has queued. So the pod's lifetime is the replication window,
// and the operator retires the pod only after the survivors' own
// reports say the rows are gone from their copies.

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

// The wait between sweeps. The sweep repeats because it is cheap
// once the rows are gone, and a repeat catches rows a returning peer
// gossips back in. A variable so the tests can shorten it.
var cleanupInterval = 30 * time.Second

// cleanupTimeout bounds each request of one sweep, so an agent that
// stops answering cannot hold a sweep open forever.
var cleanupTimeout = 2 * time.Minute

// sweeper is one cleanup container: the library it deletes, the
// catalog client it deletes through, and the log it writes.
type sweeper struct {
	library string
	catalog *Catalog
	log     io.Writer
}

// runCleanup is the role's whole program: read the environment,
// sweep on a loop, and hold the pod open until the operator retires
// it. The signal context is what ends the loop, because this process
// is PID 1 and the kernel runs no default action for its signals.
func runCleanup() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	newSweeper(os.Stdout).run(stopped)
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
		library: libraryKey(namespace, name),
		catalog: NewCatalog(api, &http.Client{Timeout: cleanupTimeout}),
		log:     log,
	}
}

// run sweeps on every tick with no attempt cap, and no success ends
// it. The pod stays up so its agent serves the deletes to every peer
// that pulls, for as long as the operator keeps the pod standing.
func (s *sweeper) run(stopped context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		s.sweep(stopped)
		select {
		case <-stopped.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweep logs a failure and leaves it for the next tick, because an
// exit would take down the agent that serves the deletes.
func (s *sweeper) sweep(ctx context.Context) {
	removed, err := s.catalog.SweepLibrary(ctx, s.library)
	if err != nil {
		fmt.Fprintf(s.log, "library.liken.sh: could not sweep %s: %v\n", s.library, err)
		return
	}
	fmt.Fprintf(s.log, "library.liken.sh: swept %s: %d rows removed\n", s.library, removed)
}
