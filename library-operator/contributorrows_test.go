package main

// What these tests read: the walk of .contributors/, the credits a title's own
// ledger carries, and the three tables the two become, against the shipped
// schema in a real SQLite.

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// One person's directory as the credits fact and the three contributor facts
// leave it, with the files the caller names.
func writeContributorEntry(t *testing.T, root, slug, entry string, files ...string) {
	t.Helper()
	folder := filepath.Join(root, contributorDirectory(slug))
	writeFile(t, filepath.Join(folder, contributorFileName), entry)
	for _, name := range files {
		writeFile(t, filepath.Join(folder, name), "the file the fact wrote")
	}
}

// One title folder with the credits ledger the credits fact wrote beside it.
func writeCreditsLedger(t *testing.T, root, title string, credits []creditEntry) {
	t.Helper()
	data, err := yaml.Marshal(likenLedger{Credits: credits})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, title, title+".mkv"), "video")
	writeFile(t, filepath.Join(root, title, likenDirectory, likenLedgerName(factCredits)), string(data))
}

// One read of a table as one line per row, so a test states the whole row it
// wants in one string.
func catalogLines(t *testing.T, catalog *Catalog, sql string) []string {
	t.Helper()
	lines, err := catalog.queryStrings(t.Context(), sql, []any{contributorLibrary})
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestTheScannerLiftsThePeopleIntoTheCatalog(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	writeContributorEntry(t, root, "tom-hanks",
		"name: Tom Hanks\nids: {imdb: nm0000158, tmdb: 31}\nborn: \"1956-07-09\"\n",
		contributorBiographyName, contributorHeadshotName)
	writeContributorEntry(t, root, "iris-kell", "name: Iris Kell\nids: {tmdb: 11}\n")
	writeCreditsLedger(t, root, "The Signal (2014)", []creditEntry{
		{Name: "Tom Hanks", Part: creditPartActor, Role: "The Captain", Order: 0,
			Contributor: ".contributors/to/tom-hanks"},
		{Name: "宮崎 駿", Part: creditPartActor, Role: "Himself", Order: 1},
		{Name: "Iris Kell", Part: creditPartDirector, Order: 2,
			Contributor: ".contributors/ir/iris-kell"},
		{Name: "Iris Kell", Part: creditPartWriter, Order: 3,
			Contributor: ".contributors/ir/iris-kell"},
	})

	result := collectFolders(walkContributors(root, contributorLibrary))
	scanMovieFolder(folderScan{root: root, library: contributorLibrary, kind: libraryKindMovies}, filepath.Join(root, "The Signal (2014)"), result)
	if result.readError {
		t.Fatal("the walk reported a read error, want none")
	}
	if err := upsertWalk(t.Context(), catalog, result); err != nil {
		t.Fatal(err)
	}

	people := catalogLines(t, catalog, `SELECT path || '|' || name || '|' || born || '|' || died ||`+
		`'|' || biography || '|' || headshot FROM contributors WHERE library = ? ORDER BY path`)
	wantPeople := []string{
		".contributors/ir/iris-kell|Iris Kell|||0|0",
		".contributors/to/tom-hanks|Tom Hanks|1956-07-09||1|1",
	}
	if strings.Join(people, ",") != strings.Join(wantPeople, ",") {
		t.Errorf("contributors = %v, want %v", people, wantPeople)
	}
	ids := catalogLines(t, catalog, `SELECT scheme || '|' || id || '|' || path FROM contributor_aliases `+
		`WHERE library = ? ORDER BY scheme, id`)
	want := []string{"imdb|nm0000158|.contributors/to/tom-hanks", "tmdb|11|.contributors/ir/iris-kell",
		"tmdb|31|.contributors/to/tom-hanks"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("contributor_aliases = %v, want %v", ids, want)
	}
	credits := catalogLines(t, catalog, `SELECT billing || '|' || contributor || '|' || name || '|' || `+
		`part || '|' || role FROM credits WHERE library = ? ORDER BY billing`)
	wantCredits := []string{
		"0|.contributors/to/tom-hanks|Tom Hanks|actor|The Captain",
		"1||宮崎 駿|actor|Himself",
		"2|.contributors/ir/iris-kell|Iris Kell|director|",
		"3|.contributors/ir/iris-kell|Iris Kell|writer|",
	}
	if strings.Join(credits, ",") != strings.Join(wantCredits, ",") {
		t.Errorf("credits = %v, want %v", credits, wantCredits)
	}
}

// A credit names the title the walk read it beside, so a person's page reads
// the titles that credit them.
func TestACreditNamesItsTitle(t *testing.T) {
	root := t.TempDir()
	writeCreditsLedger(t, root, "The Signal (2014)",
		[]creditEntry{{Name: "Tom Hanks", Contributor: ".contributors/to/tom-hanks"}})

	result := &walkResult{}
	scanMovieFolder(folderScan{root: root, library: contributorLibrary, kind: libraryKindMovies}, filepath.Join(root, "The Signal (2014)"), result)

	if len(result.movies) != 1 {
		t.Fatalf("movies = %+v, want the one title", result.movies)
	}
	if len(result.credits) != 1 || result.credits[0].Item != result.movies[0].Id {
		t.Errorf("credits = %+v, want the credit of %q", result.credits, result.movies[0].Id)
	}
}

// The walk of the store reads the attempts of the three contributor facts, so
// a gap query excludes a person the enricher already tried.
func TestTheWalkOfTheStoreReadsTheAttempts(t *testing.T) {
	root := t.TempDir()
	writeContributorEntry(t, root, "tom-hanks", "name: Tom Hanks\n")
	folder := filepath.Join(root, contributorDirectory("tom-hanks"))
	data, err := yaml.Marshal(likenLedger{Attempts: []likenAttempt{{Path: likenSelfPath, Result: attemptNothing}}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(folder, likenDirectory, likenLedgerName(factContributorHeadshot)), string(data))

	result := collectFolders(walkContributors(root, contributorLibrary))

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want the one the ledger holds", result.attempts)
	}
	got := result.attempts[0]
	if got.Item != ".contributors/to/tom-hanks" || got.Fact != factContributorHeadshot {
		t.Errorf("attempt = %+v, want the person and the fact", got)
	}
}

// A directory with no entry is no person, and a store that is not there is no
// error, because a library whose credits fact has not run holds none.
func TestTheWalkOfTheStoreReadsOnlyTheEntries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, contributorsDirectory, "t", "a-stray", "notes.txt"), "a stray")
	writeContributorEntry(t, root, "tom-hanks", "name: Tom Hanks\n")

	result := collectFolders(walkContributors(root, contributorLibrary))
	if len(result.contributors) != 1 || result.contributors[0].Path != ".contributors/to/tom-hanks" {
		t.Errorf("contributors = %+v, want the one entry", result.contributors)
	}
	if result.readError {
		t.Error("the walk reported a read error, want none")
	}

	empty := collectFolders(walkContributors(t.TempDir(), contributorLibrary))
	if len(empty.contributors) != 0 || empty.readError {
		t.Errorf("a library with no store read %+v, want no rows and no error", empty)
	}
}

// A store the scanner cannot read marks the pass incomplete, so the prune
// never sweeps the people a walk could not reach.
func TestAStoreThatCannotBeReadMarksThePassIncomplete(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, contributorsDirectory), "a file where the store should be")

	if result := collectFolders(walkContributors(root, contributorLibrary)); !result.readError {
		t.Error("the walk reported no read error, want the incomplete mark")
	}
}

// A person whose directory left the store leaves the catalog with their ids
// and their credits, the way every other row leaves it: the walk marks what it
// read, and the prune takes what it did not mark.
func TestPruningThePeopleTheWalkDidNotMark(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	held := &walkResult{
		contributors: []contributorRow{
			{Library: contributorLibrary, Path: ".contributors/to/tom-hanks", Name: "Tom Hanks"},
			{Library: contributorLibrary, Path: ".contributors/on/one-who-left", Name: "One Who Left"},
		},
		contributorAliases: []contributorAliasRow{
			{Library: contributorLibrary, Scheme: "tmdb", ID: "31", Path: ".contributors/to/tom-hanks"},
			{Library: contributorLibrary, Scheme: "tmdb", ID: "99", Path: ".contributors/on/one-who-left"},
		},
		credits: []creditRow{
			{Library: contributorLibrary, Item: "movie:tmdb:603", Billing: 0, Name: "Tom Hanks",
				Contributor: ".contributors/to/tom-hanks"},
			{Library: contributorLibrary, Item: "movie:tmdb:603", Billing: 1, Name: "One Who Left",
				Contributor: ".contributors/on/one-who-left"},
		},
	}
	if err := upsertWalk(ctx, catalog, held); err != nil {
		t.Fatal(err)
	}

	// The second walk read the first person alone, so only that person, that id,
	// and that credit carry the epoch.
	epoch := int64(1000)
	stayed := &walkResult{
		contributors:       held.contributors[:1],
		contributorAliases: held.contributorAliases[:1],
		credits:            held.credits[:1],
	}
	if _, err := catalog.markSeen(ctx, markKeys(stayed), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, contributorLibrary, epoch); err != nil {
		t.Fatal(err)
	}

	people := catalogLines(t, catalog, `SELECT path FROM contributors WHERE library = ?`)
	if len(people) != 1 || people[0] != ".contributors/to/tom-hanks" {
		t.Errorf("contributors = %v, want the person the walk read", people)
	}
	ids := catalogLines(t, catalog, `SELECT id FROM contributor_aliases WHERE library = ?`)
	if len(ids) != 1 || ids[0] != "31" {
		t.Errorf("contributor_aliases = %v, want the id of the person the walk read", ids)
	}
	credits := catalogLines(t, catalog, `SELECT name FROM credits WHERE library = ?`)
	if len(credits) != 1 || credits[0] != "Tom Hanks" {
		t.Errorf("credits = %v, want the credit the walk read", credits)
	}
}

// A credit with no name is no row, because a name is the one thing a person's
// page has to draw.
func TestACreditWithNoNameIsNoRow(t *testing.T) {
	rows := creditRows(contributorLibrary, "movie:tmdb:603", []creditEntry{
		{Name: " ", Order: 0}, {Name: "Tom Hanks", Order: 1},
	})
	if len(rows) != 1 || rows[0].Name != "Tom Hanks" {
		t.Errorf("credits = %+v, want the one named person", rows)
	}
}
