package main

// closerun.go is the close container of a library Job. It starts with the
// phases and does no work on titles. It writes the run's start first, so the
// operator reads a run in flight from the moment the Job starts. It then waits
// for the mark of every phase the Job includes, derives the Library's set
// rows, writes the work list of each heavy fact, writes the one finished runs
// row, and waits for a catalog pod to confirm it. Every container of the Job writes through the one agent, so that
// confirmation covers every row the Job wrote.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// The role the close container runs, and its container name.
const closeMode = "close"

// The variable that names the heavy facts whose work lists the close
// container writes, separated by commas. A Library that runs none names none.
const libraryWorkListsVariable = "LIBRARY_WORK_LISTS"

// The close container: the phase environment every container reads, and how
// long it waits to be confirmed.
type closeRun struct {
	*enricher
	handoffTimeout time.Duration
	// The heavy facts whose work lists the run writes.
	workLists []string
}

// The role's whole program. A Job no catalog pod confirms fails, so its rows
// stay on its claim and the retry carries them.
func runClose() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	run, err := newCloseRun(os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "library.liken.sh: %v\n", err)
		stop()
		os.Exit(1)
	}
	if err := run.runJob(stopped); err != nil {
		run.logf("the library job failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// The container reads everything it needs from its environment, as every
// phase container does.
func newCloseRun(log io.Writer) (*closeRun, error) {
	work, err := newEnricher(log)
	if err != nil {
		return nil, err
	}
	return &closeRun{
		enricher:       work,
		handoffTimeout: handoffTimeout(os.Getenv(handoffTimeoutVariable)),
		workLists:      commaNames(os.Getenv(libraryWorkListsVariable)),
	}, nil
}

// The start, the wait for every phase, the sets, the work lists, and the
// hand-off. A phase that failed does not fail the Job: its failure goes into
// the runs row, and its gaps stay open for the next Job.
//
// The lists go after every phase has ended, so each one reads the gap after
// the probe has measured the lengths this Job found. They go before the
// finished runs row, because the operator starts a worker when it reads that
// row, and the worker reads the list.
func (r *closeRun) runJob(ctx context.Context) error {
	run := libraryRun{Worker: workerEnrich, Job: r.job, Started: time.Now().UTC()}
	if _, _, err := r.catalog.UpsertRun(ctx, r.library, run); err != nil {
		return fmt.Errorf("writing the run of %s: %w", r.library, err)
	}
	r.sweepOldTallies(ctx, run.Started)

	failures, err := r.awaitPhases(ctx)
	if err != nil {
		return err
	}
	if r.kind == libraryKindMovies {
		if err := deriveLibrarySets(ctx, r.catalog, r.library); err != nil {
			r.logf("could not derive the sets of %s: %v", r.library, err)
			failures = append(failures, "the sets: "+err.Error())
		}
	}
	for _, fact := range r.workLists {
		if err := r.writeWorkList(ctx, fact); err != nil {
			r.logf("could not write the %s work list of %s: %v", fact, r.library, err)
			failures = append(failures, "the "+fact+" work list: "+err.Error())
		}
	}
	run.Finished = time.Now().UTC()
	// The failure reaches the Library's log word for word, so a path in it
	// turns opaque here, where the root is known.
	run.Failure = opaqueText(strings.Join(failures, "; "), r.root)
	return handOff(ctx, r.catalog, r.library, run, r.log, r.handoffTimeout)
}

// Waits until every phase of the Job has ended, and returns the failures the
// failed phases wrote. A container with no phases volume waits for nothing.
func (r *closeRun) awaitPhases(ctx context.Context) ([]string, error) {
	if r.board == nil {
		return nil, nil
	}
	wake, stop, err := r.wakes(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer stop()
	for !r.needsEnded(ctx) {
		if err := r.awaitWake(ctx, wake); err != nil {
			return nil, err
		}
	}
	_, failures := r.board.allEnded(r.needs)
	return failures, nil
}

// One fact's gap, read from this Job's copy of the catalog, written onto the
// volume as the list the fact's worker reads. A Job narrowed to some folders
// still lists the whole Library, because the list replaces the one before it.
func (r *closeRun) writeWorkList(ctx context.Context, fact string) error {
	items, err := r.catalog.workItems(ctx, fact, r.library, time.Now().UTC(), r.refresh[fact])
	if err != nil {
		return err
	}
	if err := r.writer.writeWorkList(r.root, fact, items); err != nil {
		return err
	}
	r.logf("wrote the %s work list: %s", fact, counted(len(items), "video"))
	return nil
}
