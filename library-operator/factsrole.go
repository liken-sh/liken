package main

// The facts container of a library Job. LIBRARY_FACTS names the facts it
// runs, in order, and the container waits once for its synced copy of the
// catalog before the first of them. The pod names the facts, so the operator
// holds no order of its own, and a container is one phase of the run.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// One fact's whole run against one Library. A fact reads its own gap out of
// the local copy, does its work, and records an attempt per item.
type factRun func(ctx context.Context, e *enricher) error

// Every fact this image runs, by the name a container puts in LIBRARY_FACTS.
// A name this map does not hold ends the container, because a pod that asks
// for work this image cannot do is a manifest to repair.
var factRuns = map[string]factRun{
	factProbe:     func(ctx context.Context, e *enricher) error { return e.probeFact(ctx) },
	factArrival:   func(ctx context.Context, e *enricher) error { return e.arrivalFact(ctx) },
	factIdentity:  func(ctx context.Context, e *enricher) error { return e.identityFact(ctx) },
	factTrickplay: func(ctx context.Context, e *enricher) error { return e.trickplayFact(ctx) },

	factOverview:             nfoFactRun(factOverview),
	factCertification:        nfoFactRun(factCertification),
	factRatingTMDb:           nfoFactRun(factRatingTMDb),
	factRatingIMDb:           nfoFactRun(factRatingIMDb),
	factRatingRottenTomatoes: nfoFactRun(factRatingRottenTomatoes),
	factRatingMetacritic:     nfoFactRun(factRatingMetacritic),
	factCredits:              nfoFactRun(factCredits),

	factPoster:       artFactRun(factPoster),
	factBackdrop:     artFactRun(factBackdrop),
	factLogo:         artFactRun(factLogo),
	factClearart:     artFactRun(factClearart),
	factBanner:       artFactRun(factBanner),
	factLandscape:    artFactRun(factLandscape),
	factDiscart:      artFactRun(factDiscart),
	factSeasonPoster: artFactRun(factSeasonPoster),
	factSeasonBanner: artFactRun(factSeasonBanner),
	factEpisodeThumb: artFactRun(factEpisodeThumb),

	factTrailer:     func(ctx context.Context, e *enricher) error { return e.trailerFact(ctx) },
	factTrailerFile: func(ctx context.Context, e *enricher) error { return e.trailerFileFact(ctx) },
	factMarks:       func(ctx context.Context, e *enricher) error { return e.marksFact(ctx) },

	factContributorIDs:       contributorFactRun(factContributorIDs),
	factContributorBiography: contributorFactRun(factContributorBiography),
	factContributorHeadshot:  contributorFactRun(factContributorHeadshot),
}

// The role's whole program. A failure is a non-zero exit, so the Job fails
// and Kubernetes retries it.
func runFacts() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	work, err := newEnricher(os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "library.liken.sh: %v\n", err)
		stop()
		os.Exit(1)
	}
	if err := work.runFacts(stopped, namedFacts(os.Getenv(libraryFactsVariable))); err != nil {
		work.logf("the facts container failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// The facts a container runs, in the order its list names them. An empty name
// is dropped, so a trailing comma or a space around a name names no fact.
func namedFacts(list string) []string {
	return commaNames(list)
}

// Every name is checked before the first fact runs, so a container that names
// a fact this image cannot run fails before it writes to the volume. Then the
// container waits once for its own copy of the catalog to hold what the
// standing pod reports, because a gap query against a copy that has not
// synced names a fraction of the work.
//
// In a library Job the container is one phase. It holds its running lock,
// runs the phase loop, and writes its mark. A phase that fails writes a
// failed mark and returns no error, so the container exits zero and the Job
// still succeeds when its close container writes the runs row. The failed
// phase's gaps stay open for the next Job. A container with no phases volume
// runs its facts once.
func (e *enricher) runFacts(ctx context.Context, facts []string) error {
	if len(facts) == 0 {
		return fmt.Errorf("%s names no fact", libraryFactsVariable)
	}
	for _, name := range facts {
		if _, held := factRuns[name]; !held {
			return fmt.Errorf("%s names %s, which this image does not run", libraryFactsVariable, name)
		}
	}
	// The recorder runs for the life of the container and flushes once more
	// when the facts end, because no scrape reaches a container that has
	// exited.
	defer e.tallies.recording(ctx)()
	if e.board == nil {
		return e.runSynced(ctx, facts, e.pass)
	}
	if err := e.board.start(e.container); err != nil {
		return err
	}
	worked := e.runSynced(ctx, facts, e.phaseLoop)
	if ctx.Err() != nil {
		return worked
	}
	if worked != nil {
		e.logf("the %s phase failed: %v", e.container, worked)
	}
	return e.board.finish(e.container, worked)
}

// The wait for the synced copy, and then the work.
func (e *enricher) runSynced(ctx context.Context, facts []string,
	work func(context.Context, []string) error) error {
	// The wait is silent otherwise, and it can run for minutes on a fresh
	// claim, so its two ends are logged.
	e.logf("waiting for the catalog to sync onto this claim")
	started := time.Now()
	if err := e.awaitCatalogSync(ctx); err != nil {
		return err
	}
	e.logf("the catalog synced in %s", time.Since(started).Round(time.Second))
	return work(ctx, facts)
}
