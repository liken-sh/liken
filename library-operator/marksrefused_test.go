package main

// What these tests read: the marks gap ends on a catalog read it is refused,
// and a Job narrowed to one folder asks about no file outside it.

import (
	"os"
	"path/filepath"
	"testing"
)

// A catalog read that fails ends the run, because the gap list is the work,
// and the file gets no ledger, so it stays in the gap. refused names the read
// the catalog refuses, counted from one in the order the run reads them.
func TestTheMarksGapEndsWhereTheCatalogRefusesARead(t *testing.T) {
	cases := []struct {
		name    string
		refused int
	}{
		{name: "the gap", refused: 1},
		{name: "the file", refused: 2},
		{name: "the work's aliases", refused: 3},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			root := t.TempDir()
			seedMarkedMovie(t, catalog, root, "Some Film (1999)", "movie:tmdb:1001", 8160000)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			work.library = marksLibrary
			asked := 0
			agent.queriesLeft = one.refused

			if err := work.marksGap(t.Context(), markLineOf(scriptedMarks{block: providerBlockIntroDB, asked: &asked})); err == nil {
				t.Error("the run read every file, want the error the catalog gave")
			}
			if asked != 0 {
				t.Errorf("the provider was asked %d times, want none", asked)
			}
			if _, err := os.Stat(filepath.Join(root, "Some Film (1999)", likenDirectory)); !os.IsNotExist(err) {
				t.Errorf("stat .liken = %v, want no ledger", err)
			}
		})
	}
}

// A Job narrowed to another folder, as a webhook's rescan is, asks about no
// file outside its scope and records no attempt.
func TestANarrowedJobAsksForNoMarksOutsideItsScope(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedMarkedMovie(t, catalog, root, "Some Film (1999)", "movie:tmdb:1001", 8160000)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.library = marksLibrary
	work.scopes = []string{"Another Film (2001)"}
	asked := 0

	if err := work.marksGap(t.Context(), markLineOf(scriptedMarks{block: providerBlockIntroDB, asked: &asked})); err != nil {
		t.Fatal(err)
	}

	if asked != 0 {
		t.Errorf("the provider was asked %d times, want none outside the scope", asked)
	}
	if _, err := os.Stat(filepath.Join(root, "Some Film (1999)", likenDirectory)); !os.IsNotExist(err) {
		t.Errorf("stat .liken = %v, want no ledger", err)
	}
}
