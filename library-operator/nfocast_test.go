package main

// What these tests read: the credits fact writing the cast of a title,
// which is the provider's list where a provider named one and the
// .nfo file's own actors where none did.

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// An .nfo file with the actors Jellyfin wrote, which is the shape of every
// title of a library it filled before this operator existed.
func writeJellyfinActors(t *testing.T, nfoPath string) {
	t.Helper()
	writeFile(t, nfoPath, `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <title>Winter Harbour</title>
  <year>2011</year>
  <uniqueid type="tmdb" default="true">4242</uniqueid>
  <actor>
    <name>Nora Vance</name>
    <role>Captain</role>
    <order>0</order>
  </actor>
  <actor>
    <name>Ivo Brandt</name>
    <role>The Mate</role>
    <order>1</order>
  </actor>
  <actor>
    <name>Rea Solberg</name>
    <order>2</order>
  </actor>
</movie>
`)
}

// The provider's cast replaces the .nfo file's actors: the provider's
// order is the billing, the provider's roles are the roles, a person
// only the .nfo file names is dropped, and every person of the new cast
// gets an entry in .contributors/.
func TestTheProvidersCastReplacesTheActorsTheNFOHolds(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	nfoPath := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeJellyfinActors(t, nfoPath)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: map[string]factAnswer{
		factCredits: {Cast: []creditedActor{
			{Name: "Nora Vance", Role: "The Keeper", IDs: providerIDs{"tmdb": "31"}},
			{Name: "Ada Ferris", Role: "The Pilot", IDs: providerIDs{"tmdb": "77"}},
		}},
	}}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	want := []creditEntry{
		{Name: "Nora Vance", Part: creditPartActor, Role: "The Keeper", Order: 0,
			Contributor: ".contributors/no/nora-vance"},
		{Name: "Ada Ferris", Part: creditPartActor, Role: "The Pilot", Order: 1,
			Contributor: ".contributors/ad/ada-ferris"},
	}
	if !slices.Equal(ledger.Credits, want) {
		t.Fatalf("credits = %+v, want %+v", ledger.Credits, want)
	}
	if entry := readFileString(t, filepath.Join(root, ".contributors/no/nora-vance", contributorFileName)); entry !=
		"name: Nora Vance\nids: {tmdb: 31}\n" {
		t.Errorf("contributor.yaml = %q, want the ids the provider gave for the person the .nfo file holds", entry)
	}
	if _, err := os.Stat(filepath.Join(root, ".contributors/iv/ivo-brandt")); !os.IsNotExist(err) {
		t.Errorf("the store holds an entry for a person the provider's cast does not name")
	}
	if held := readFileString(t, nfoPath); !strings.Contains(held, "<name>Ada Ferris</name>") ||
		strings.Contains(held, "<name>Ivo Brandt</name>") {
		t.Errorf("the .nfo file is not the provider's cast:\n%s", held)
	}
}

// The actor group is another writer's until this fact writes it, so the first
// run over an .nfo file that Jellyfin filled rewrites the group and is no
// fight.
func TestTheFirstCreditsWriteOverAnotherWritersActorsIsNoFight(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	nfoPath := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeJellyfinActors(t, nfoPath)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Fatalf("attempts = %+v, want the one that took the group over", ledger.Attempts)
	}
	if strings.Contains(log.String(), "another writer holds") {
		t.Errorf("log = %q, want no fight over a group this fact never wrote", log.String())
	}
}

// The actor elements are rewritten only where the provider's cast is
// not what a player already reads. credits.yaml and the entries are
// written either way.
func TestTheCreditsFactRewritesTheActorsOnlyWhenTheCastDiffers(t *testing.T) {
	cases := []struct {
		name    string
		cast    []creditedActor
		changed bool
	}{
		{
			name: "a provider that names the people the .nfo file holds",
			cast: []creditedActor{
				{Name: "Nora Vance", Role: "Captain", IDs: providerIDs{"tmdb": "31"}},
				{Name: "Ivo Brandt", Role: "The Mate"},
				{Name: "Rea Solberg"},
			},
		},
		{
			name: "a provider that names the part and the picture the .nfo file left empty",
			cast: []creditedActor{
				{Name: "Nora Vance", Role: "Captain"},
				{Name: "Ivo Brandt", Role: "The Mate"},
				{Name: "Rea Solberg", Role: "The Cook", Thumb: "https://example.test/r.jpg"},
			},
			changed: true,
		},
		{
			name: "a provider that names one more person",
			cast: []creditedActor{
				{Name: "Nora Vance", Role: "Captain"},
				{Name: "Ivo Brandt", Role: "The Mate"},
				{Name: "Rea Solberg"},
				{Name: "Ada Ferris", Role: "The Pilot"},
			},
			changed: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			folder := "Winter Harbour (2011)"
			nfoPath := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
			writeJellyfinActors(t, nfoPath)
			before := readFileString(t, nfoPath)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts,
				answers: map[string]factAnswer{factCredits: {Cast: test.cast}}}

			if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
				t.Fatal(err)
			}

			if changed := readFileString(t, nfoPath) != before; changed != test.changed {
				t.Errorf("the .nfo file changed = %v, want %v:\n%s", changed, test.changed,
					readFileString(t, nfoPath))
			}
			ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Credits) != len(test.cast) {
				t.Errorf("credits = %+v, want one entry per person the provider named", ledger.Credits)
			}
		})
	}
}

// A title whose providers named no cast is written as an empty credits.yaml
// with the attempt beside it, so the title is not a gap on every run that
// follows.
func TestACreditsAnswerWithNoCastWritesAnEmptyLedger(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	nfoPath := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	empty := &fakeAnswerer{name: "tmdb", facts: nfoFacts,
		answers: map[string]factAnswer{factCredits: {}}}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(empty)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Credits) != 0 {
		t.Errorf("credits = %+v, want none", ledger.Credits)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Fatalf("attempts = %+v, want the one that wrote the empty file", ledger.Attempts)
	}
	if held := readFileString(t, nfoPath); strings.Contains(held, "<actor>") {
		t.Errorf("the fact wrote an actor no provider named:\n%s", held)
	}
}

// The actors the .nfo file holds, in billing order, with the people it
// names nothing left out. They are the cast where no provider named
// one.
func TestTheNFOsActorsReadInBillingOrder(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     []creditedActor
	}{
		{
			name:     "an .nfo file with no actors",
			document: "<movie><title>One</title></movie>",
		},
		{
			name: "the actors another writer left",
			document: "<movie><actor><name>Nora Vance</name><role>Captain</role>" +
				"<thumb>https://example.test/a.jpg</thumb></actor>" +
				"<actor><name>Ivo Brandt</name></actor></movie>",
			want: []creditedActor{
				{Name: "Nora Vance", Role: "Captain", Thumb: "https://example.test/a.jpg", Order: 0},
				{Name: "Ivo Brandt", Order: 1},
			},
		},
		{
			name: "actors Jellyfin wrote out of billing order",
			document: "<movie><actor><name>Ivo Brandt</name><order>2</order></actor>" +
				"<actor><name>Nora Vance</name><order>0</order></actor>" +
				"<actor><name>Ada Ferris</name></actor>" +
				"<actor><name>Mira Solberg</name><order>1</order></actor></movie>",
			want: []creditedActor{
				{Name: "Nora Vance", Order: 0},
				{Name: "Mira Solberg", Order: 1},
				{Name: "Ivo Brandt", Order: 2},
				{Name: "Ada Ferris", Order: 3},
			},
		},
		{
			name:     "an actor with no name at all",
			document: "<movie><actor><role>Captain</role></actor></movie>",
		},
		{
			name:     "a document that is not XML",
			document: "this is not xml <<<",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := nfoCast([]byte(test.document)); !reflect.DeepEqual(got, test.want) {
				t.Errorf("cast = %+v, want %+v", got, test.want)
			}
		})
	}
}

// Jellyfin writes a producer as an actor element whose role is
// "Producer", so a person who acted and produced appears twice in the
// .nfo file. The provider's cast names them once, in the part they acted,
// and that is the cast the fact writes.
func TestAProducerTheNFOHoldsAsAnActorIsWrittenOnce(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	nfoPath := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeFile(t, nfoPath, `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <title>Winter Harbour</title>
  <uniqueid type="tmdb" default="true">4242</uniqueid>
  <actor>
    <name>Nora Vance</name>
    <role>Captain</role>
    <order>0</order>
  </actor>
  <actor>
    <name>Nora Vance</name>
    <role>Producer</role>
    <order>1</order>
  </actor>
</movie>
`)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: map[string]factAnswer{
		factCredits: {Cast: []creditedActor{{Name: "Nora Vance", Role: "Captain"}}},
	}}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	want := []creditEntry{{Name: "Nora Vance", Part: creditPartActor, Role: "Captain", Order: 0,
		Contributor: ".contributors/no/nora-vance"}}
	if !slices.Equal(ledger.Credits, want) {
		t.Fatalf("credits = %+v, want %+v", ledger.Credits, want)
	}
	if held := readFileString(t, nfoPath); strings.Count(held, "<name>Nora Vance</name>") != 1 {
		t.Errorf("the .nfo file names the person twice:\n%s", held)
	}
}

// A provider that named no person at all leaves the .nfo file's own
// actors where they are, in their order in the .nfo file, and
// credits.yaml names them.
func TestACreditsAnswerWithNoPeopleLeavesTheNFOsOwn(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	nfoPath := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeJellyfinActors(t, nfoPath)
	before := readFileString(t, nfoPath)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	empty := &fakeAnswerer{name: "tmdb", facts: nfoFacts,
		answers: map[string]factAnswer{factCredits: {}}}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(empty)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	want := []creditEntry{
		{Name: "Nora Vance", Part: creditPartActor, Role: "Captain", Order: 0,
			Contributor: ".contributors/no/nora-vance"},
		{Name: "Ivo Brandt", Part: creditPartActor, Role: "The Mate", Order: 1,
			Contributor: ".contributors/iv/ivo-brandt"},
		{Name: "Rea Solberg", Part: creditPartActor, Order: 2,
			Contributor: ".contributors/re/rea-solberg"},
	}
	if !slices.Equal(ledger.Credits, want) {
		t.Fatalf("credits = %+v, want %+v", ledger.Credits, want)
	}
	if held := readFileString(t, nfoPath); held != before {
		t.Errorf("the .nfo file reads\n%s\nwant the actors it already held", held)
	}
}
