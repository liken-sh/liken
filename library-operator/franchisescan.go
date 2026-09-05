package main

// franchisescan.go is the whole of a franchises scan: clone, compare,
// read, write, prune. The other kinds scan a volume that is always there.
// This kind scans a forge that is not, so a failed clone keeps every row
// as it was. The commit the last scan read is the mark that makes a scan
// on a schedule cost one clone and nothing else.

import (
	"context"
	"net/http"
	"time"
)

// franchiseScan reads one commit of the repository into the catalog. A
// clone that fails writes no row and prunes nothing, so a forge outage
// never empties a franchise page. A clone that returns the commit the last
// scan read writes no row either.
func (s *scanner) franchiseScan(ctx context.Context) error {
	s.walkMutex.Lock()
	defer s.walkMutex.Unlock()

	started := time.Now()

	// Nothing writes the catalog before the clone returns. A scan that
	// cannot reach the forge, and a scan of the commit it already read,
	// both leave every row where it is.
	s.logf("cloning %s at %s", s.git.URL, s.git.Ref)
	head, err := cloneRepository(ctx, s.git.URL, s.git.Ref, s.checkout)
	if err != nil {
		return s.walkFailed("read the repository", err)
	}

	// The art the files link to is downloaded into the claim before the rows
	// are read, so a row reads the file the fetch just wrote. A fetch that
	// failed last time is asked again here, which is why a scan that wrote art
	// reads the rows again even on a commit it already has.
	wrote := s.fetchFranchiseArt(ctx)
	if head == s.lastCommit() && wrote == 0 {
		s.logf("the repository holds %s, which the last scan read", head)
		s.noteUnchanged()
		return nil
	}

	if err := s.catalog.ensureSeen(ctx); err != nil {
		return s.walkFailed("ensure the seen table", err)
	}
	epoch := time.Now().UnixNano()
	before, err := s.catalog.countItems(ctx, s.library)
	if err != nil {
		return s.walkFailed("count the catalog before the walk", err)
	}

	result := walkFranchises(s.checkout, s.root, s.library)
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
	s.noteCommit(head)
	return nil
}

// fetchFranchiseArt downloads the art every franchise.yaml links to into the
// claim, and returns how many files it wrote. The claim is the Library's own,
// mounted writable for this kind alone.
func (s *scanner) fetchFranchiseArt(ctx context.Context) int {
	return franchiseArtFetch{
		client: &http.Client{Timeout: franchiseArtTimeout},
		writer: newVolumeWriter(s.job),
		log:    s.logf,
	}.fetchAll(ctx, s.checkout, s.root)
}

// lastCommit is the commit the last scan of this library read, which the
// Job reads out of its own runs row before it clones.
func (s *scanner) lastCommit() string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.commit
}

// noteCommit records the commit this scan read. The Job's finished run
// carries it, and the operator reports it as status.commit.
func (s *scanner) noteCommit(head string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.commit = head
}

// noteUnchanged moves the last-walk time for a scan that found the commit
// it already read, and leaves every count and every row where they are.
func (s *scanner) noteUnchanged() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.report.LastWalk = time.Now().UTC()
}
