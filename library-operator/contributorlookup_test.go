package main

// What these tests read: how the credits fact finds a person's entry by id
// before it looks by name, how a credit with only an IMDb id asks TMDb for the
// TMDb id, and how a credit that reaches an entry a merge removed links to the
// entry that stays.

import (
	"net/http"
	"path/filepath"
	"testing"
)

// One entry on the volume and its rows in the catalog, the way the walk leaves
// them.
func seedWalkedEntry(t *testing.T, catalog *Catalog, root, slug, entry string) string {
	t.Helper()
	writeContributorEntry(t, root, slug, entry)
	result := &walkResult{}
	readContributorFolder(root, "house/movies", filepath.Join(root, contributorDirectory(slug)), result)
	if err := upsertWalk(t.Context(), catalog, result); err != nil {
		t.Fatal(err)
	}
	return contributorDirectory(slug)
}

// The TMDb client the credits fact asks for the TMDb id of an IMDb id.
func withPersonFinder(t *testing.T, work *enricher, answers map[string]string) *fakeTMDb {
	t.Helper()
	client, fake := newFakeTMDb(t, answers)
	work.personFinder = client
	return fake
}

func TestACreditFindsItsEntryByIDBeforeItsName(t *testing.T) {
	cases := []struct {
		name    string
		credit  creditedActor
		answers map[string]string
		want    string
	}{
		{
			name:   "a TMDb id the store holds under another spelling",
			credit: creditedActor{Name: "Theodore Harker", IDs: providerIDs{"tmdb": "4031"}},
			want:   ".contributors/to/tod-harker",
		},
		{
			name:   "an IMDb id the store holds",
			credit: creditedActor{Name: "Theodore Harker", IDs: providerIDs{"imdb": "nm9000158"}},
			want:   ".contributors/to/tod-harker",
		},
		{
			name:   "an IMDb id whose TMDb id the store holds",
			credit: creditedActor{Name: "Theodore Harker", IDs: providerIDs{"imdb": "nm9999999"}},
			answers: map[string]string{
				tmdbKey("/3/find/nm9999999", "", ""): findAnswer(`{"id":4031}`),
			},
			want: ".contributors/to/tod-harker",
		},
		{
			name:   "an IMDb id of another person of the same name",
			credit: creditedActor{Name: "Tod Harker", IDs: providerIDs{"imdb": "nm7777777"}},
			answers: map[string]string{
				tmdbKey("/3/find/nm7777777", "", ""): findAnswer(`{"id":992}`),
			},
			want: ".contributors/to/tod-harker-tmdb-992",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			seedWalkedEntry(t, catalog, root, "tod-harker", "name: Tod Harker\nids: {imdb: nm9000158, tmdb: 4031}\n")
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			withPersonFinder(t, work, test.answers)
			folder := titleFolder(t, root, "Quiet Station (2014)")

			work.writeCredits(folder, factAnswer{Cast: []creditedActor{test.credit}})

			credits := artLedger(t, folder, factCredits).Credits
			if len(credits) != 1 || credits[0].Contributor != test.want {
				t.Errorf("credits = %+v, want the entry %s", credits, test.want)
			}
		})
	}
}

// The entry a credit creates holds the TMDb id the find call gave, so the next
// credit of the same person resolves by id and makes no call.
func TestAFoundTMDbIDLandsInTheNewEntry(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := withPersonFinder(t, work, map[string]string{
		tmdbKey("/3/find/nm9000158", "", ""): findAnswer(`{"id":4031}`),
	})
	credit := creditedActor{Name: "Tod Harker", IDs: providerIDs{"imdb": "nm9000158"}}

	work.writeCredits(titleFolder(t, root, "One Film (1999)"), factAnswer{Cast: []creditedActor{credit}})
	work.writeCredits(titleFolder(t, root, "Another Film (2001)"), factAnswer{Cast: []creditedActor{credit}})

	entry := readFileString(t, filepath.Join(root, ".contributors/to/tod-harker", contributorFileName))
	if entry != "name: Tod Harker\nids: {imdb: nm9000158, tmdb: 4031}\n" {
		t.Errorf("contributor.yaml = %q, want the IMDb id and the TMDb id", entry)
	}
	if got := fake.served[tmdbKey("/3/find/nm9000158", "", "")]; got != 1 {
		t.Errorf("the find call ran %d times, want 1", got)
	}
}

// A credit that reaches an entry a merge removed links to the entry that
// stays, by its id and by its slug both.
func TestACreditThatReachesAMergedEntryLinksToTheEntryThatStays(t *testing.T) {
	cases := []struct {
		name   string
		credit creditedActor
	}{
		{name: "by the id the catalog still holds", credit: creditedActor{Name: "Someone", IDs: providerIDs{"tmdb": "4031"}}},
		{name: "by the slug", credit: creditedActor{Name: "Theodore Harker"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			seedWalkedEntry(t, catalog, root, "tod-harker", "name: Tod Harker\nids: {tmdb: 4031}\n")
			if _, err := catalog.UpsertContributorIDs(t.Context(), []contributorAliasRow{{
				Library: "house/movies", Scheme: "tmdb", ID: "4031", Path: ".contributors/th/theodore-harker",
			}}); err != nil {
				t.Fatal(err)
			}
			writeContributorEntry(t, root, "theodore-harker", "mergedInto: .contributors/to/tod-harker\n")
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			folder := titleFolder(t, root, "Quiet Station (2014)")

			work.writeCredits(folder, factAnswer{Cast: []creditedActor{test.credit}})

			credits := artLedger(t, folder, factCredits).Credits
			if len(credits) != 1 || credits[0].Contributor != ".contributors/to/tod-harker" {
				t.Errorf("credits = %+v, want the entry that stays", credits)
			}
		})
	}
}

// A catalog row whose entry left the volume is no answer, and the slug join
// finds the person.
func TestAnIDWhoseEntryLeftTheVolumeFallsBackToTheSlug(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	if _, err := catalog.UpsertContributorIDs(t.Context(), []contributorAliasRow{{
		Library: "house/movies", Scheme: "tmdb", ID: "4031", Path: ".contributors/go/gone",
	}}); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	folder := titleFolder(t, root, "Quiet Station (2014)")

	work.writeCredits(folder, factAnswer{Cast: []creditedActor{{Name: "Tod Harker", IDs: providerIDs{"tmdb": "4031"}}}})

	credits := artLedger(t, folder, factCredits).Credits
	if len(credits) != 1 || credits[0].Contributor != ".contributors/to/tod-harker" {
		t.Errorf("credits = %+v, want the entry at the slug", credits)
	}
}

// The credits fact asks TMDb for a person's TMDb id only where the Library's
// sources reach a TMDb account.
func TestThePersonFinderNeedsATMDbSourceAndItsKey(t *testing.T) {
	cases := []struct {
		name    string
		sources []string
		token   string
		want    bool
	}{
		{name: "no TMDb source", sources: []string{providerBlockOMDb}, token: "a-key"},
		{name: "a TMDb source with no key", sources: []string{providerBlockTMDb}},
		{name: "a TMDb source and its key", sources: []string{providerBlockTMDb}, token: "a-key", want: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := func(name string) string {
				if name == tmdbTokenVariable {
					return test.token
				}
				return ""
			}
			if got := newPersonFinder(test.sources, value, nil) != nil; got != test.want {
				t.Errorf("finder = %v, want %v", got, test.want)
			}
		})
	}
}

// A find call that fails leaves the credit the ids it came with, and the slug
// join names the person.
func TestAFindCallThatFailsFallsBackToTheSlug(t *testing.T) {
	root := t.TempDir()
	work, log := testEnricher(t, libraryKindMovies, root, nil)
	fake := withPersonFinder(t, work, map[string]string{})
	fake.statuses[tmdbKey("/3/find/nm9000158", "", "")] = 500
	folder := titleFolder(t, root, "Quiet Station (2014)")

	work.writeCredits(folder, factAnswer{Cast: []creditedActor{{Name: "Tod Harker", IDs: providerIDs{"imdb": "nm9000158"}}}})

	entry := readFileString(t, filepath.Join(root, ".contributors/to/tod-harker", contributorFileName))
	if entry != "name: Tod Harker\nids: {imdb: nm9000158}\n" {
		t.Errorf("contributor.yaml = %q, want the ids the credit came with", entry)
	}
	if log.Len() == 0 {
		t.Error("the log is empty, want the failed call")
	}
}

// An id under a scheme the slug never takes still finds the entry, and a
// catalog that cannot be read leaves the slug join to find it.
func TestTheLookupByIDReadsEverySchemeAndSurvivesTheCatalog(t *testing.T) {
	cases := []struct {
		name    string
		catalog func(t *testing.T, root string) *Catalog
		want    string
	}{
		{
			name: "a Wikidata id the store holds",
			catalog: func(t *testing.T, root string) *Catalog {
				catalog, _ := newSQLiteCatalog(t)
				seedWalkedEntry(t, catalog, root, "tod-harker", "name: Tod Harker\nids: {wikidata: Q999001}\n")
				return catalog
			},
			want: ".contributors/to/tod-harker",
		},
		{
			name: "a catalog that cannot be read",
			catalog: func(t *testing.T, _ string) *Catalog {
				return NewCatalog("http://127.0.0.1:1", http.DefaultClient)
			},
			want: ".contributors/th/theodore-harker",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			work, _ := testEnricher(t, libraryKindMovies, root, test.catalog(t, root))
			folder := titleFolder(t, root, "Quiet Station (2014)")

			work.writeCredits(folder, factAnswer{Cast: []creditedActor{
				{Name: "Theodore Harker", IDs: providerIDs{"wikidata": "Q999001"}},
			}})

			credits := artLedger(t, folder, factCredits).Credits
			if len(credits) != 1 || credits[0].Contributor != test.want {
				t.Errorf("credits = %+v, want the entry %s", credits, test.want)
			}
		})
	}
}
