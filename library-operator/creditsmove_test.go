package main

// What these tests read: the credits fact's move of each credit that names an
// entry a merge removed, from the gap in the catalog to credits.yaml and the
// credits rows.

import (
	"path/filepath"
	"testing"
)

// A title whose credits name an entry a merge removed, walked into the
// catalog with the merge record and the entry that stays.
func titleCreditingAMergedEntry(t *testing.T, credits []creditEntry) (*enricher, *Catalog, string) {
	t.Helper()
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedWalkedEntry(t, catalog, root, "tom-hanks", "name: Tom Hanks\nids: {tmdb: 31}\n")
	seedWalkedEntry(t, catalog, root, "thomas-hanks", "mergedInto: .contributors/to/tom-hanks\n")
	writeCreditsLedger(t, root, "The Signal (2014)", credits)
	result := &walkResult{}
	scanMovieFolder(folderScan{root: root, library: contributorLibrary, kind: libraryKindMovies},
		filepath.Join(root, "The Signal (2014)"), result)
	if err := upsertWalk(t.Context(), catalog, result); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	return work, catalog, filepath.Join(root, "The Signal (2014)")
}

func TestTheCreditsFactMovesACreditToTheEntryThatStays(t *testing.T) {
	work, catalog, folder := titleCreditingAMergedEntry(t, []creditEntry{
		{Name: "Thomas Hanks", Part: creditPartActor, Order: 0, Contributor: ".contributors/th/thomas-hanks"},
		{Name: "Iris Kell", Part: creditPartDirector, Order: 1},
	})

	if err := work.moveCredits(t.Context()); err != nil {
		t.Fatal(err)
	}

	credits := artLedger(t, folder, factCredits).Credits
	if len(credits) != 2 || credits[0].Contributor != ".contributors/to/tom-hanks" || credits[1].Contributor != "" {
		t.Errorf("credits = %+v, want the credit moved to the entry that stays", credits)
	}
	rows := catalogLines(t, catalog, `SELECT contributor FROM credits WHERE library = ? AND billing = 0`)
	if len(rows) != 1 || rows[0] != ".contributors/to/tom-hanks" {
		t.Errorf("credits rows = %v, want the entry that stays", rows)
	}
	ledger := artLedger(t, folder, factCreditsMove)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want the move", ledger.Attempts)
	}
	gap, err := work.gaps(t.Context(), factCreditsMove, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(gap) != 0 {
		t.Errorf("gap = %v, want none once the credits moved", gap)
	}
}

// A record whose entry that stays is not on the volume moves nothing, and the
// attempt says so.
func TestARecordThatNamesNoEntryMovesNothing(t *testing.T) {
	work, _, folder := titleCreditingAMergedEntry(t, []creditEntry{
		{Name: "Thomas Hanks", Part: creditPartActor, Order: 0, Contributor: ".contributors/th/thomas-hanks"},
	})
	writeContributorEntry(t, work.root, "thomas-hanks", "mergedInto: .contributors/go/gone\n")

	if err := work.moveCredits(t.Context()); err != nil {
		t.Fatal(err)
	}

	credits := artLedger(t, folder, factCredits).Credits
	if len(credits) != 1 || credits[0].Contributor != ".contributors/th/thomas-hanks" {
		t.Errorf("credits = %+v, want the credit as it was", credits)
	}
	ledger := artLedger(t, folder, factCreditsMove)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("attempts = %+v, want nothing moved", ledger.Attempts)
	}
}

// A credits ledger the fact cannot read is an error attempt, and the credits
// stay as they are.
func TestACreditsLedgerThatCannotBeReadIsAnErrorAttempt(t *testing.T) {
	work, _, folder := titleCreditingAMergedEntry(t, []creditEntry{
		{Name: "Thomas Hanks", Part: creditPartActor, Order: 0, Contributor: ".contributors/th/thomas-hanks"},
	})
	writeFile(t, filepath.Join(folder, likenDirectory, likenLedgerName(factCredits)), "credits: [")

	if err := work.moveCredits(t.Context()); err != nil {
		t.Fatal(err)
	}

	ledger := artLedger(t, folder, factCreditsMove)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want an error", ledger.Attempts)
	}
}

// The credits fact moves the credits after its own gap, in the same run of
// the nfo container.
func TestTheCreditsFactRunsTheMove(t *testing.T) {
	work, _, folder := titleCreditingAMergedEntry(t, []creditEntry{
		{Name: "Thomas Hanks", Part: creditPartActor, Order: 0, Contributor: ".contributors/th/thomas-hanks"},
	})
	t.Setenv(librarySourcesVariable, providerBlockTMDb)
	t.Setenv(tmdbTokenVariable, "the-key")

	if err := factRuns[factCredits](t.Context(), work); err != nil {
		t.Fatal(err)
	}

	credits := artLedger(t, folder, factCredits).Credits
	if len(credits) != 1 || credits[0].Contributor != ".contributors/to/tom-hanks" {
		t.Errorf("credits = %+v, want the credit moved", credits)
	}
	if work.personFinder == nil {
		t.Error("the container holds no person finder, want the TMDb account")
	}
}
