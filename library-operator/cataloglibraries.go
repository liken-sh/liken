package main

// This file is the scanner's half of the departure in plan 21. The
// operator cannot read a catalog, because every agent's API binds
// its own pod's loopback. So each scanner reports the set of
// libraries its catalog holds rows for, and the operator releases a
// departing Library's finalizer when no survivor's set names it any
// more.
//
// The set is read on a timer, not through the agent's subscription
// API, and the local harness settled that. A subscription cannot
// carry this question: Corrosion's matcher prepends the primary-key
// columns to the query, so DISTINCT runs over the key and matches
// every row, and a projection of key columns alone streams its first
// snapshot and then goes silent with no error. The endpoint that
// does fire, /v1/updates/<table>, needs one stream per table and
// answers with a changed key, so it would still end in this same
// read.
//
// The read is what makes the timer good enough. The six-table UNION
// through /v1/queries measured a median of 1.5 ms over 5,000 rows
// and 6.6 ms over 50,000, a covering index scan per table, linear in
// the rows the catalog holds. A peer's delete reached a poll on the
// harness in under 100 ms, so the interval below, and not the
// replication, bounds how long a departure waits.

import (
	"context"
	"slices"
	"time"
)

// How often the set is re-read. Seconds rather than the walk timer,
// because this read is what ends a departure and a deleted Library
// waits on it. A variable so the tests can shorten it.
var catalogLibrariesInterval = 5 * time.Second

// watchCatalogLibraries re-reads the set for the scanner's whole
// life, with no attempt cap. It is a level read rather than a change
// feed, so a change that arrives by gossip from a peer reaches the
// bus the same way a local write does.
func (s *scanner) watchCatalogLibraries(ctx context.Context) {
	ticker := time.NewTicker(catalogLibrariesInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.publishCatalogLibraries(ctx)
		}
	}
}

// publishCatalogLibraries publishes only on a change, because the
// report is retained and a republish of the same value is a message
// that says nothing new.
func (s *scanner) publishCatalogLibraries(ctx context.Context) {
	if s.refreshCatalogLibraries(ctx) {
		s.publishReport()
	}
}

// refreshCatalogLibraries reads the set and reports whether it
// changed. A failed read leaves the last set in the report, the way
// a failed count does, so an agent that stops answering never reads
// as an empty catalog.
func (s *scanner) refreshCatalogLibraries(ctx context.Context) bool {
	keys, err := s.catalog.LibraryKeys(ctx)
	if err != nil {
		s.logWalk("read the libraries the catalog holds", err)
		return false
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if slices.Equal(s.report.CatalogLibraries, keys) {
		return false
	}
	s.report.CatalogLibraries = keys
	return true
}
