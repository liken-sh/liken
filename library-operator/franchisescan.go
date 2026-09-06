package main

// franchisescan.go is the whole of a franchises scan: fetch the art, read,
// write, prune. The checkout is a mounted claim and the files are a few
// hundred kilobytes, so every scan walks it, the way every other kind's
// scan walks its volume. The art ledger is what keeps a scan from reading
// a link twice, and that is the one cost worth avoiding.

import (
	"context"
	"net/http"
	"time"
)

// franchiseScan reads the checkout on the claim into the catalog. A
// checkout the walk could not read in full prunes nothing, which is the
// incomplete-walk guard every kind has.
func (s *scanner) franchiseScan(ctx context.Context) error {
	s.walkMutex.Lock()
	defer s.walkMutex.Unlock()

	started := time.Now()

	// The art the files link to is downloaded into the art claim before the
	// rows are read, so a row reads the file the fetch just wrote. A fetch
	// that failed last time is asked again here.
	s.fetchFranchiseArt(ctx)

	if err := s.catalog.ensureSeen(ctx); err != nil {
		return s.walkFailed("ensure the seen table", err)
	}
	epoch := time.Now().UnixNano()
	before, err := s.catalog.countItems(ctx, s.library)
	if err != nil {
		return s.walkFailed("count the catalog before the walk", err)
	}

	result := walkFranchises(s.root, s.art, s.library)
	for _, failure := range result.readFailures {
		s.logf("could not read %s: %v", failure.path, failure.err)
	}
	if err := flushWalk(ctx, s.catalog, result, epoch); err != nil {
		return s.walkFailed("write the franchises", err)
	}

	// A checkout the walk could not read in full describes only part of
	// the repository, so it prunes nothing and keeps the rows the catalog
	// holds.
	if incompleteWalk(result.readError, len(result.franchises), before) {
		s.logIncompleteWalk(result.readError, len(result.franchises), before)
		return errIncompleteWalk
	}

	s.settleWalk(ctx, epoch, before, result.titles, result.unidentified,
		result.unidentifiedNames, started)
	return nil
}

// fetchFranchiseArt downloads the art every franchise.yaml in the checkout
// links to into the art claim, which is the one claim this scan mounts
// writable.
func (s *scanner) fetchFranchiseArt(ctx context.Context) {
	franchiseArtFetch{
		client: &http.Client{Timeout: franchiseArtTimeout},
		writer: newVolumeWriter(s.job),
		log:    s.logf,
	}.fetchAll(ctx, s.root, s.art)
}
