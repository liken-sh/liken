package main

// The facts container of the enricher Job. LIBRARY_FACTS names the facts it
// runs, in order, and the container waits once for its synced copy of the
// catalog before the first of them. The pod names the facts, so the operator
// holds no order of its own, and a container is one phase of the run.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// One fact's whole run against one Library. A fact reads its own gap out of
// the local copy, does its work, and records an attempt per item.
type factRun func(ctx context.Context, e *enricher) error

// Every fact this image runs, by the name a container puts in LIBRARY_FACTS.
// A name this map does not hold ends the container, because a pod that asks
// for work this image cannot do is a manifest to repair.
var factRuns = map[string]factRun{
	factProbe:    func(ctx context.Context, e *enricher) error { return e.probeFact(ctx) },
	factIdentity: func(ctx context.Context, e *enricher) error { return e.identityFact(ctx) },

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
	factSeasonPoster: artFactRun(factSeasonPoster),
	factEpisodeThumb: artFactRun(factEpisodeThumb),
}

// The role's whole program. A failure is a non-zero exit, so the Job fails
// and Kubernetes retries it.
func runFacts() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	work := newEnricher(os.Stdout)
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
// synced names a fraction of the work. One wait covers every fact in the
// container, because no fact reads a write that another fact in the same
// container made.
func (e *enricher) runFacts(ctx context.Context, facts []string) error {
	if len(facts) == 0 {
		return fmt.Errorf("%s names no fact", libraryFactsVariable)
	}
	for _, name := range facts {
		if _, held := factRuns[name]; !held {
			return fmt.Errorf("%s names %s, which this image does not run", libraryFactsVariable, name)
		}
	}
	if err := e.awaitCatalogSync(ctx, facts[0]); err != nil {
		return err
	}
	for _, name := range facts {
		if err := factRuns[name](ctx, e); err != nil {
			return fmt.Errorf("the %s fact failed: %w", name, err)
		}
	}
	return nil
}
