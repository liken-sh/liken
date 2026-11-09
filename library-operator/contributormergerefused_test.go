package main

// What these tests read: the merge of contributor entries ends on a catalog
// read it is refused, and a Job narrowed to one folder merges no entry
// outside it. Both entries stay on the volume either way.

import (
	"os"
	"path/filepath"
	"testing"
)

// The two entries of one person that the merge would join into one.
func twoEntriesOnAgent(t *testing.T) (*enricher, *sqliteAgent, string) {
	t.Helper()
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	seedWalkedEntry(t, catalog, root, "oren-tally", "name: Oren Tally\nids: {tmdb: 9031}\n")
	seedWalkedEntry(t, catalog, root, otherSlug, "name: Oren Tally\nids: {imdb: nm9000031, tmdb: 9031}\n")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	return work, agent, root
}

// Whether both entries are still on the volume.
func bothEntriesStay(t *testing.T, root string) {
	t.Helper()
	for _, slug := range []string{"oren-tally", otherSlug} {
		if _, err := os.Stat(filepath.Join(root, contributorDirectory(slug), contributorFileName)); err != nil {
			t.Errorf("the entry %s: %v, want it on the volume", slug, err)
		}
	}
}

// A read of the entries that hold an id ends the merge with the catalog's
// error, because the group it would join is not whole.
func TestTheMergeEndsWhereTheCatalogRefusesTheEntriesOfAnID(t *testing.T) {
	work, agent, root := twoEntriesOnAgent(t)
	// The gap is the first read, and the entries of the first id the second.
	agent.queriesLeft = 2

	if err := work.mergeContributors(t.Context()); err == nil {
		t.Error("the merge reported no error, want the refusal")
	}

	bothEntriesStay(t, root)
}

// A Job narrowed to another folder, as a webhook's rescan is, merges no
// entry outside its scope.
func TestANarrowedJobMergesNoEntryOutsideItsScope(t *testing.T) {
	work, _, root := twoEntriesOnAgent(t)
	work.scopes = []string{"Another Film (2015)"}

	if err := work.mergeContributors(t.Context()); err != nil {
		t.Fatal(err)
	}

	bothEntriesStay(t, root)
}
