package main

// What these tests read: the contributor_ids rows and the contributor_merges
// rows the walk of .contributors/ writes, and the prune that removes the rows
// of an entry that left the store, against the shipped schema.

import (
	"path/filepath"
	"strings"
	"testing"
)

// One count out of the catalog, read for the test library.
func catalogCount(t *testing.T, catalog *Catalog, sql string) int {
	t.Helper()
	count, err := catalog.queryInt(t.Context(), sql, []any{contributorLibrary})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// Two entries that hold one IMDb id are two rows of contributor_ids, and one
// row of contributor_aliases, whose key holds one path for each id.
func TestTheWalkRecordsEveryIDOfEveryEntry(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	writeContributorEntry(t, root, "tom-hanks", "name: Tom Hanks\nids: {imdb: nm0000158, tmdb: 31}\n")
	writeContributorEntry(t, root, "thomas-hanks", "name: Thomas Hanks\nids: {imdb: nm0000158}\n")

	if err := upsertWalk(t.Context(), catalog, collectFolders(walkContributors(root, contributorLibrary))); err != nil {
		t.Fatal(err)
	}

	ids := catalogLines(t, catalog, `SELECT path || '|' || scheme || '|' || id FROM contributor_ids `+
		`WHERE library = ? ORDER BY path, scheme`)
	want := []string{
		".contributors/th/thomas-hanks|imdb|nm0000158",
		".contributors/to/tom-hanks|imdb|nm0000158",
		".contributors/to/tom-hanks|tmdb|31",
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("contributor_ids = %v, want %v", ids, want)
	}
	if got := catalogCount(t, catalog, `SELECT count(*) FROM contributor_aliases WHERE library = ? AND scheme = 'imdb'`); got != 1 {
		t.Errorf("contributor_aliases holds %d rows of the IMDb id, want 1", got)
	}
}

// An entry a merge removed is a record of the merge and no person: the walk
// writes its merge row and its merge attempts, and no contributors row, no id,
// and no alias.
func TestTheWalkReadsAMergedEntryAsARecordOfTheMerge(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	writeContributorEntry(t, root, "thomas-hanks", "mergedInto: .contributors/to/tom-hanks\n")
	writeFile(t, filepath.Join(root, contributorDirectory("thomas-hanks"), likenDirectory,
		likenLedgerName(factContributorMerge)), "attempts:\n  - path: .\n    at: 2026-09-25T00:00:00Z\n    result: error\n")

	result := collectFolders(walkContributors(root, contributorLibrary))
	if err := upsertWalk(t.Context(), catalog, result); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"contributors", "contributor_ids", "contributor_aliases"} {
		if got := catalogCount(t, catalog, `SELECT count(*) FROM `+table+` WHERE library = ?`); got != 0 {
			t.Errorf("%s holds %d rows, want none for a merged entry", table, got)
		}
	}
	merges := catalogLines(t, catalog, `SELECT path || '|' || merged_into FROM contributor_merges WHERE library = ?`)
	if len(merges) != 1 || merges[0] != ".contributors/th/thomas-hanks|.contributors/to/tom-hanks" {
		t.Errorf("contributor_merges = %v, want the record of the merge", merges)
	}
	if len(result.attempts) != 1 || result.attempts[0].Fact != factContributorMerge {
		t.Errorf("attempts = %+v, want the merge attempt", result.attempts)
	}
}

// The rows of an entry that left the store leave the catalog, the ids and the
// merge records the same way as the people.
func TestPruningTheIDsAndTheMergesTheWalkDidNotMark(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	held := &walkResult{
		contributorAliases: []contributorAliasRow{
			{Library: contributorLibrary, Scheme: "tmdb", ID: "31", Path: ".contributors/to/tom-hanks"},
			{Library: contributorLibrary, Scheme: "imdb", ID: "nm0000158", Path: ".contributors/to/tom-hanks"},
		},
		contributorMerges: []contributorMergeRow{
			{Library: contributorLibrary, Path: ".contributors/th/thomas-hanks", MergedInto: ".contributors/to/tom-hanks"},
		},
	}
	if err := upsertWalk(ctx, catalog, held); err != nil {
		t.Fatal(err)
	}

	epoch := int64(1000)
	stayed := &walkResult{contributorAliases: held.contributorAliases[:1]}
	if _, err := catalog.markSeen(ctx, markKeys(stayed), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, contributorLibrary, epoch); err != nil {
		t.Fatal(err)
	}

	ids := catalogLines(t, catalog, `SELECT scheme FROM contributor_ids WHERE library = ?`)
	if len(ids) != 1 || ids[0] != "tmdb" {
		t.Errorf("contributor_ids = %v, want the id the walk read", ids)
	}
	if merges := catalogLines(t, catalog, `SELECT path FROM contributor_merges WHERE library = ?`); len(merges) != 0 {
		t.Errorf("contributor_merges = %v, want none", merges)
	}
}
