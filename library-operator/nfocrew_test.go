package main

// What these tests read: the credits fact writing the directors and the
// writers of a title beside its cast, from the sidecar and from every provider
// that names them.

import (
	"path/filepath"
	"slices"
	"testing"
)

// The union of the crew the sidecar holds and the crew the provider names: the
// sidecar's people keep their place, the writer Kodi's credits element names is
// one of the writers, and the provider's own people follow.
func TestTheCreditsFactKeepsTheCrewTheSidecarHolds(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeJellyfinCrew(t, sidecar)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: map[string]factAnswer{
		factCredits: {
			Cast: []creditedActor{{Name: "Nora Vance", Role: "Captain"}},
			Directors: []creditedPerson{{Name: "Iris Kell", IDs: providerIDs{"tmdb": "11"}},
				{Name: "Mira Solberg", IDs: providerIDs{"tmdb": "21"}}},
			Writers: []creditedPerson{{Name: "Iris Kell", IDs: providerIDs{"tmdb": "11"}}},
		},
	}}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	want := []creditEntry{
		{Name: "Nora Vance", Part: creditPartActor, Role: "Captain", Order: 0,
			Contributor: ".contributors/no/nora-vance"},
		{Name: "Iris Kell", Part: creditPartDirector, Order: 1,
			Contributor: ".contributors/ir/iris-kell"},
		{Name: "Mira Solberg", Part: creditPartDirector, Order: 2,
			Contributor: ".contributors/mi/mira-solberg"},
		{Name: "Petra Lund", Part: creditPartWriter, Order: 3,
			Contributor: ".contributors/pe/petra-lund"},
		{Name: "Iris Kell", Part: creditPartWriter, Order: 4,
			Contributor: ".contributors/ir/iris-kell"},
	}
	if !slices.Equal(ledger.Credits, want) {
		t.Fatalf("credits = %+v, want %+v", ledger.Credits, want)
	}
	if entry := readFileString(t, filepath.Join(root, ".contributors/ir/iris-kell", contributorFileName)); entry !=
		"name: Iris Kell\nids: {tmdb: 11}\n" {
		t.Errorf("contributor.yaml = %q, want the ids the provider gave for the director", entry)
	}
	if entry := readFileString(t, filepath.Join(root, ".contributors/pe/petra-lund", contributorFileName)); entry !=
		"name: Petra Lund\n" {
		t.Errorf("contributor.yaml = %q, want the person the sidecar alone holds", entry)
	}
}

// The group edit writes the actors, the directors, and the writers where the
// first of them stood, and leaves every other byte of the sidecar: the plot,
// the credits element Kodi reads, and the URL after the root element.
func TestTheCreditsGroupLeavesEveryOtherByteOfTheSidecar(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeJellyfinCrew(t, sidecar)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: map[string]factAnswer{
		factCredits: {
			Cast:      []creditedActor{{Name: "Ada Ferris", Role: "The Pilot"}},
			Directors: []creditedPerson{{Name: "Mira Solberg"}},
		},
	}}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	want := `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <title>Winter Harbour</title>
  <year>2011</year>
  <plot>A keeper watches the ice.</plot>
  <actor>
    <name>Nora Vance</name>
    <role>Captain</role>
    <order>0</order>
  </actor>
  <actor>
    <name>Ada Ferris</name>
    <role>The Pilot</role>
    <order>1</order>
  </actor>
  <director>Iris Kell</director>
  <director>Mira Solberg</director>
  <writer>Petra Lund</writer>
  <credits>Petra Lund</credits>
  <uniqueid type="tmdb" default="true">4242</uniqueid>
</movie>
<url function="GetDetails" cache="4242.xml">https://api.example.test/series?apikey=k&amp;id=4242</url>
`
	if held := readFileString(t, sidecar); held != want {
		t.Errorf("the sidecar reads\n%s\nwant\n%s", held, want)
	}
}

// What the union starts from: the crew the sidecar holds, in its own order,
// with the writer Kodi's credits element names among the writers and a name
// in both elements read once.
func TestTheUnionStartsFromTheCrewTheSidecarHolds(t *testing.T) {
	cases := []struct {
		name      string
		document  string
		directors []string
		writers   []string
	}{
		{name: "a sidecar with no crew", document: "<movie><title>One</title></movie>"},
		{
			name: "the crew another writer left",
			document: "<movie><director>Iris Kell</director><director>Mira Solberg</director>" +
				"<writer>Petra Lund</writer><credits>Petra Lund</credits>" +
				"<credits>Otto Rhee</credits></movie>",
			directors: []string{"Iris Kell", "Mira Solberg"},
			writers:   []string{"Petra Lund", "Otto Rhee"},
		},
		{name: "an element with no name at all", document: "<movie><director> </director></movie>"},
		{name: "a document that is not XML", document: "this is not xml <<<"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			directors, writers := sidecarCrew([]byte(test.document))

			if got := peopleNames(directors); !slices.Equal(got, test.directors) {
				t.Errorf("directors = %v, want %v", got, test.directors)
			}
			if got := peopleNames(writers); !slices.Equal(got, test.writers) {
				t.Errorf("writers = %v, want %v", got, test.writers)
			}
		})
	}
}

// The crew elements are rewritten only where the union changed what a player
// reads, which is the rule the actor elements follow.
func TestTheCreditsFactRewritesTheCrewOnlyWhenItDiffers(t *testing.T) {
	cases := []struct {
		name    string
		answer  factAnswer
		changed bool
	}{
		{
			name: "a provider that names the crew the sidecar holds",
			answer: factAnswer{
				Cast:      []creditedActor{{Name: "Nora Vance", Role: "Captain"}},
				Directors: []creditedPerson{{Name: "Iris Kell"}},
				Writers:   []creditedPerson{{Name: "Petra Lund"}},
			},
		},
		{
			name: "a provider that names one more director",
			answer: factAnswer{
				Cast:      []creditedActor{{Name: "Nora Vance", Role: "Captain"}},
				Directors: []creditedPerson{{Name: "Mira Solberg"}},
				Writers:   []creditedPerson{{Name: "Petra Lund"}},
			},
			changed: true,
		},
		{
			name: "a provider that names one more writer",
			answer: factAnswer{
				Cast:      []creditedActor{{Name: "Nora Vance", Role: "Captain"}},
				Directors: []creditedPerson{{Name: "Iris Kell"}},
				Writers:   []creditedPerson{{Name: "Otto Rhee"}},
			},
			changed: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			folder := "Winter Harbour (2011)"
			sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
			writeJellyfinCrew(t, sidecar)
			before := readFileString(t, sidecar)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts,
				answers: map[string]factAnswer{factCredits: test.answer}}

			if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
				t.Fatal(err)
			}

			if changed := readFileString(t, sidecar) != before; changed != test.changed {
				t.Errorf("the sidecar changed = %v, want %v:\n%s", changed, test.changed,
					readFileString(t, sidecar))
			}
		})
	}
}

// The crew are sets, so two providers make one list of directors and one of
// writers, in the order the sources name the providers, and a person both of
// them name gains the ids of both.
func TestTwoProvidersMergeTheCrewByTheUnion(t *testing.T) {
	merged, names := mergeAnswers(factCredits, []providerAnswer{
		{block: providerBlockTMDb, answer: factAnswer{
			Directors: []creditedPerson{{Name: "Iris Kell", IDs: providerIDs{"tmdb": "11"}}},
			Writers:   []creditedPerson{{Name: "  "}},
		}},
		{block: providerBlockOMDb, answer: factAnswer{
			Directors: []creditedPerson{
				{Name: "Iris Kell", IDs: providerIDs{"imdb": "nm11"}},
				{Name: "Mira Solberg"},
			},
		}},
	})

	if got := peopleNames(merged.Directors); !slices.Equal(got, []string{"Iris Kell", "Mira Solberg"}) {
		t.Fatalf("directors = %v, want the union of the two providers", got)
	}
	if len(merged.Writers) != 0 {
		t.Errorf("writers = %+v, want none for a provider that named a person with no name", merged.Writers)
	}
	if merged.Directors[0].IDs["tmdb"] != "11" || merged.Directors[0].IDs["imdb"] != "nm11" {
		t.Errorf("ids = %v, want the ids both providers gave for the person", merged.Directors[0].IDs)
	}
	if !slices.Equal(names, providerNames{providerBlockTMDb, providerBlockOMDb}) {
		t.Errorf("providers = %v, want the two that answered", names)
	}
}
