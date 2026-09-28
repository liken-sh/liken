package main

// What these tests read: the merge of two .contributors/ entries that hold one
// id, the entry that stays, the record the removed entry keeps while a credit
// names it, the groups the merge leaves, and the gap that opens the enricher
// for a merge.

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A library with two entries of one person: the entry at the plain slug with
// the TMDb id, and an entry at the slug with the IMDb id suffix with the TMDb
// id, the IMDb id, a biography, and a headshot.
const otherSlug = "oren-tally-imdb-nm9000031"

func twoEntriesOfOnePerson(t *testing.T) (*enricher, *Catalog, string) {
	t.Helper()
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedWalkedEntry(t, catalog, root, "oren-tally", "name: Oren Tally\nids: {tmdb: 9031}\n")
	other := filepath.Join(root, contributorDirectory(otherSlug))
	writeFile(t, filepath.Join(other, contributorBiographyName), "An actor.\n")
	writeFile(t, filepath.Join(other, contributorHeadshotName), testImage)
	seedWalkedEntry(t, catalog, root, otherSlug,
		"name: Oren Tally\nids: {imdb: nm9000031, tmdb: 9031}\nborn: \"1961-03-14\"\n")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	return work, catalog, root
}

func TestTwoEntriesThatHoldOneIDMergeIntoOne(t *testing.T) {
	work, catalog, root := twoEntriesOfOnePerson(t)

	if err := work.mergeContributors(t.Context()); err != nil {
		t.Fatal(err)
	}

	stays := filepath.Join(root, ".contributors/or/oren-tally")
	entry := readFileString(t, filepath.Join(stays, contributorFileName))
	if entry != "name: Oren Tally\nids: {imdb: nm9000031, tmdb: 9031}\nborn: \"1961-03-14\"\n" {
		t.Errorf("contributor.yaml = %q, want both ids and the birth date", entry)
	}
	if got := readFileString(t, filepath.Join(stays, contributorBiographyName)); got != "An actor.\n" {
		t.Errorf("biography.txt = %q, want the one the removed entry held", got)
	}
	if got := readFileString(t, filepath.Join(stays, contributorHeadshotName)); got != testImage {
		t.Errorf("headshot.jpg = %q, want the one the removed entry held", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".contributors/or/oren-tally-imdb-nm9000031")); !os.IsNotExist(err) {
		t.Errorf("the removed entry is on the volume (%v), want it deleted, because no credit names it", err)
	}
	people := catalogLines(t, catalog, `SELECT path || '|' || born || '|' || biography FROM contributors WHERE library = ?`)
	if len(people) != 1 || people[0] != ".contributors/or/oren-tally|1961-03-14|1" {
		t.Errorf("contributors = %v, want the entry that stays", people)
	}
	ids := catalogLines(t, catalog, `SELECT path || '|' || scheme FROM contributor_ids WHERE library = ? ORDER BY scheme`)
	if strings.Join(ids, ",") != ".contributors/or/oren-tally|imdb,.contributors/or/oren-tally|tmdb" {
		t.Errorf("contributor_ids = %v, want the ids of the entry that stays", ids)
	}
}

// The ids fact's ledger holds the hash of the file the merge wrote, so the
// fact's next run reads the merge as its own write and not as a hand edit.
func TestTheMergeKeepsTheIDsFactsFightCheck(t *testing.T) {
	work, _, root := twoEntriesOfOnePerson(t)
	if err := work.mergeContributors(t.Context()); err != nil {
		t.Fatal(err)
	}

	stays := filepath.Join(root, ".contributors/or/oren-tally")
	fought, err := work.contributorHeldByAnother(stays, []byte(readFileString(t, filepath.Join(stays, contributorFileName))))
	if err != nil {
		t.Fatal(err)
	}
	if fought {
		t.Error("the ids fact reads the merge as a hand edit")
	}
}

// A removed entry that a credit names keeps a record of the merge, which the
// credits fact reads to move the credit.
func TestARemovedEntryACreditNamesKeepsTheRecord(t *testing.T) {
	work, catalog, root := twoEntriesOfOnePerson(t)
	if _, err := catalog.UpsertCredits(t.Context(), []creditRow{{
		Library: contributorLibrary, Item: "movie:tmdb:1001", Name: "Oren Tally",
		Contributor: ".contributors/or/oren-tally-imdb-nm9000031",
	}}); err != nil {
		t.Fatal(err)
	}

	if err := work.mergeContributors(t.Context()); err != nil {
		t.Fatal(err)
	}

	removed := filepath.Join(root, ".contributors/or/oren-tally-imdb-nm9000031")
	record := readFileString(t, filepath.Join(removed, contributorFileName))
	if record != "mergedInto: .contributors/or/oren-tally\n" {
		t.Errorf("contributor.yaml = %q, want the one field that names the entry that stays", record)
	}
	if ledger := artLedger(t, removed, factContributorIDs); len(ledger.Items) != 1 ||
		ledger.Items[0].Wrote != contentHash([]byte(record)) {
		t.Errorf("ledger items = %+v, want the hash of the record", ledger.Items)
	}
	merges := catalogLines(t, catalog, `SELECT path || '|' || merged_into FROM contributor_merges WHERE library = ?`)
	if len(merges) != 1 || merges[0] != ".contributors/or/oren-tally-imdb-nm9000031|.contributors/or/oren-tally" {
		t.Errorf("contributor_merges = %v, want the record", merges)
	}
	if people := catalogLines(t, catalog, `SELECT path FROM contributors WHERE library = ?`); len(people) != 1 {
		t.Errorf("contributors = %v, want the entry that stays alone", people)
	}
}

// A record no credit names any more is deleted, with its row.
func TestARecordNoCreditNamesIsDeleted(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedWalkedEntry(t, catalog, root, "oren-tally", "name: Oren Tally\nids: {tmdb: 9031}\n")
	seedWalkedEntry(t, catalog, root, "oren-talley", "mergedInto: .contributors/or/oren-tally\n")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.mergeContributors(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(root, ".contributors/or/oren-talley")); !os.IsNotExist(err) {
		t.Errorf("the record is on the volume (%v), want it deleted", err)
	}
	if merges := catalogLines(t, catalog, `SELECT path FROM contributor_merges WHERE library = ?`); len(merges) != 0 {
		t.Errorf("contributor_merges = %v, want none", merges)
	}
	if _, err := os.Stat(filepath.Join(root, ".contributors/or/oren-tally", contributorFileName)); err != nil {
		t.Errorf("the entry that stays is gone: %v", err)
	}
}

// With no entry at the plain slug of its own name, the entry with the most ids
// stays.
func TestTheEntryWithTheMostIDsStaysWhereNoneIsAtThePlainSlug(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedWalkedEntry(t, catalog, root, "oren-tally-tmdb-9031", "name: Oren Tally\nids: {tmdb: 9031}\n")
	seedWalkedEntry(t, catalog, root, "oren-tally-imdb-nm9000031", "name: Oren Tally\nids: {imdb: nm9000031, tmdb: 9031}\n")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.mergeContributors(t.Context()); err != nil {
		t.Fatal(err)
	}

	people := catalogLines(t, catalog, `SELECT path FROM contributors WHERE library = ?`)
	if len(people) != 1 || people[0] != ".contributors/or/oren-tally-imdb-nm9000031" {
		t.Errorf("contributors = %v, want the entry with the most ids", people)
	}
}

// A group the merge must not join is left whole: an entry a person edited, and
// two entries that hold two ids in one scheme. Each entry records why, and the
// fights count holds an edited group.
func TestTheMergeLeavesAGroupItMustNotJoin(t *testing.T) {
	cases := []struct {
		name       string
		other      string
		edit       bool
		want       string
		wantReason string
		wantFights int
	}{
		{
			name: "an entry a person edited", other: "name: Oren Talley\nids: {tmdb: 9031}\n", edit: true,
			want: attemptHeld, wantReason: "a person edited .contributors/or/oren-tally/contributor.yaml", wantFights: 2,
		},
		{
			name: "two ids in one scheme", other: "name: Oren Talley\nids: {imdb: nm9000031, tmdb: 992}\n",
			want:       attemptConflict,
			wantReason: ".contributors/or/oren-talley holds tmdb 992 and .contributors/or/oren-tally holds tmdb 9031",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			entry := "name: Oren Tally\nids: {imdb: nm9000031, tmdb: 9031}\n"
			seedWalkedEntry(t, catalog, root, "oren-tally", entry)
			seedWalkedEntry(t, catalog, root, "oren-talley", test.other)
			stays := filepath.Join(root, ".contributors/or/oren-tally")
			if test.edit {
				writeFile(t, filepath.Join(stays, likenDirectory, likenLedgerName(factContributorIDs)),
					"items:\n  - path: .\n    wrote: not-the-hash-of-the-file\n")
			}
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)

			if err := work.mergeContributors(t.Context()); err != nil {
				t.Fatal(err)
			}

			if got := readFileString(t, filepath.Join(stays, contributorFileName)); got != entry {
				t.Errorf("contributor.yaml = %q, want the entry as it was", got)
			}
			for _, slug := range []string{"oren-tally", "oren-talley"} {
				ledger := artLedger(t, filepath.Join(root, contributorDirectory(slug)), factContributorMerge)
				if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != test.want {
					t.Errorf("%s attempts = %+v, want %s", slug, ledger.Attempts, test.want)
				}
				if len(ledger.Items) != 1 || ledger.Items[0].Reason != test.wantReason {
					t.Errorf("%s items = %+v, want the reason %q", slug, ledger.Items, test.wantReason)
				}
			}
			fights, err := catalog.fightCount(t.Context(), contributorLibrary)
			if err != nil {
				t.Fatal(err)
			}
			if fights != test.wantFights {
				t.Errorf("fights = %d, want %d", fights, test.wantFights)
			}
		})
	}
}

// The merge gap names every entry of a group and every record no credit
// names, and it leaves an entry inside the window of its last merge attempt.
func TestTheMergeGap(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name     string
		attempts []attemptRow
		credits  []creditRow
		want     []string
	}{
		{
			name: "a group and a record",
			want: []string{".contributors/on/one-record", ".contributors/or/oren-talley", ".contributors/or/oren-tally"},
		},
		{
			name: "a record a credit names",
			credits: []creditRow{{Library: contributorLibrary, Item: "movie:tmdb:1", Name: "One",
				Contributor: ".contributors/on/one-record"}},
			want: []string{".contributors/or/oren-talley", ".contributors/or/oren-tally"},
		},
		{
			name: "an entry the merge left inside its window",
			attempts: []attemptRow{{Library: contributorLibrary, Item: ".contributors/or/oren-tally",
				Fact: factContributorMerge, At: now.Unix(), Result: attemptHeld}},
			want: []string{".contributors/on/one-record", ".contributors/or/oren-talley"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			if err := upsertWalk(t.Context(), catalog, &walkResult{
				contributorAliases: []contributorAliasRow{
					{Library: contributorLibrary, Scheme: "tmdb", ID: "9031", Path: ".contributors/or/oren-tally"},
					{Library: contributorLibrary, Scheme: "tmdb", ID: "9031", Path: ".contributors/or/oren-talley"},
					{Library: contributorLibrary, Scheme: "tmdb", ID: "7", Path: ".contributors/al/alone"},
				},
				contributorMerges: []contributorMergeRow{{Library: contributorLibrary,
					Path: ".contributors/on/one-record", MergedInto: ".contributors/al/alone"}},
				credits:  test.credits,
				attempts: test.attempts,
			}); err != nil {
				t.Fatal(err)
			}

			paths, err := catalog.queryStrings(t.Context(), gapQueries[factContributorMerge],
				gapParams(factContributorMerge, contributorLibrary, now, time.Time{}))
			if err != nil {
				t.Fatal(err)
			}

			slices.Sort(paths)
			if !slices.Equal(paths, test.want) {
				t.Errorf("gap = %v, want %v", paths, test.want)
			}
		})
	}
}

// A merge is work only for a phase that runs contributor.ids, and a credit
// move only for a phase that runs credits, because each runs in the container
// of that fact.
func TestAMergeGapOpensThePhaseThatRunsItsFact(t *testing.T) {
	cases := []struct {
		name  string
		gap   string
		facts []string
		want  bool
	}{
		{name: "a merge where the source serves the ids", gap: factContributorMerge,
			facts: []string{factContributorIDs}, want: true},
		{name: "a merge where the source serves the identity alone", gap: factContributorMerge,
			facts: []string{factIdentity}},
		{name: "a move where the source serves the credits", gap: factCreditsMove,
			facts: []string{factCredits}, want: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := &libraryReport{Gaps: map[string]int{test.gap: 1}}

			if got := phaseGapOpen(studioMovies(), report, providerSet{}, test.facts, time.Now()); got != test.want {
				t.Errorf("phaseGapOpen = %v, want %v", got, test.want)
			}
		})
	}
}

// A catalog that cannot be read ends the container, because the gap list is
// the work. The merge and the move both read one.
func TestTheMergeAndTheMoveEndWhenTheCatalogFails(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(),
		NewCatalog("http://127.0.0.1:1", http.DefaultClient))

	if err := work.mergeContributors(t.Context()); err == nil {
		t.Error("the merge reported no error, want one")
	}
	if err := work.moveCredits(t.Context()); err == nil {
		t.Error("the move reported no error, want one")
	}
}

// A record the delete door refuses stays on the volume, and its attempt holds
// it out of the gap for the error window.
func TestARecordTheDeleteRefusesIsAnErrorAttempt(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	record := "elsewhere/oren-talley"
	writeFile(t, filepath.Join(root, record, contributorFileName), "mergedInto: .contributors/or/oren-tally\n")
	if _, err := catalog.UpsertContributorMerges(t.Context(), []contributorMergeRow{{
		Library: contributorLibrary, Path: record, MergedInto: ".contributors/or/oren-tally",
	}}); err != nil {
		t.Fatal(err)
	}
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.mergeContributors(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(root, record, contributorFileName)); err != nil {
		t.Errorf("the record left the volume: %v", err)
	}
	ledger := artLedger(t, filepath.Join(root, record), factContributorMerge)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want an error", ledger.Attempts)
	}
	if !strings.Contains(log.String(), "could not delete") {
		t.Errorf("log = %q, want the refused delete", log.String())
	}
}

// What the merge records when it cannot read or write a group: an entry whose
// contributor.yaml does not parse leaves the group, a ledger that does not
// parse stops the group, and a volume that refuses the write stops it too.
func TestWhatTheMergeRecordsWhenItCannotReadOrWriteAGroup(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, other string)
		want  string
	}{
		{
			name: "an entry that does not parse",
			setup: func(t *testing.T, other string) {
				writeFile(t, filepath.Join(other, contributorFileName), "name: [Oren Tally\n")
			},
			want: "could not read entry",
		},
		{
			name: "a ledger that does not parse",
			setup: func(t *testing.T, other string) {
				writeFile(t, filepath.Join(other, likenDirectory, likenLedgerName(factContributorIDs)), "items: [")
			},
			want: "could not read the ledger",
		},
		{
			name: "a volume that refuses the write",
			setup: func(t *testing.T, other string) {
				if err := os.Chmod(other, 0o555); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(other, 0o755) })
			},
			want: "could not merge",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			work, _, root := twoEntriesOfOnePerson(t)
			var log strings.Builder
			work.log = &log
			other := filepath.Join(root, contributorDirectory(otherSlug))
			test.setup(t, other)

			if err := work.mergeContributors(t.Context()); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(filepath.Join(other, contributorFileName)); err != nil {
				t.Errorf("the other entry left the volume: %v", err)
			}
			if !strings.Contains(log.String(), test.want) {
				t.Errorf("log = %q, want %q", log.String(), test.want)
			}
		})
	}
}
