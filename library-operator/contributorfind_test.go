package main

// What these tests read: how the ids fact fills an entry that holds an IMDb id
// and no TMDb id, through TMDb's find call, and the ids gap that names such an
// entry.

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// The answer of TMDb's find call for one IMDb id: the people it names.
func findAnswer(people string) string {
	return `{"movie_results":[],"person_results":[` + people + `]}`
}

func TestTheIDsFactFindsTheTMDbIDOfAnEntryThatHoldsOnlyAnIMDbID(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\nids: {imdb: nm0000158}\n")
	work, log := testEnricher(t, libraryKindMovies, root, nil)
	client, _ := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/find/nm0000158", "", ""):         findAnswer(`{"id":31,"name":"Tom Hanks"}`),
		tmdbKey("/3/person/31", "", ""):              personAnswer("1956-07-09", "", "", ""),
		tmdbKey("/3/person/31/external_ids", "", ""): `{"imdb_id":"nm0000158"}`,
	})

	if !work.fillContributorIDs(t.Context(), client, folder,
		contributorGap{path: contributorDirectory("tom-hanks"), imdb: "nm0000158"}) {
		t.Fatalf("the fact wrote nothing, log = %q", log.String())
	}

	want := "name: Tom Hanks\nids: {imdb: nm0000158, tmdb: 31}\nborn: \"1956-07-09\"\n"
	if got := readFileString(t, filepath.Join(folder, contributorFileName)); got != want {
		t.Errorf("contributor.yaml = %q, want %q", got, want)
	}
}

// An IMDb id TMDb names no person for is a miss, and the entry stays as it is.
func TestAnIMDbIDTMDbDoesNotKnowIsAMiss(t *testing.T) {
	root := t.TempDir()
	entry := "name: Tom Hanks\nids: {imdb: nm0000158}\n"
	folder := seedEntry(t, root, "tom-hanks", entry)
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	client, fake := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/find/nm0000158", "", ""): findAnswer(""),
	})

	if work.fillContributorIDs(t.Context(), client, folder,
		contributorGap{path: contributorDirectory("tom-hanks"), imdb: "nm0000158"}) {
		t.Error("the fact reported a write, want none")
	}

	if got := readFileString(t, filepath.Join(folder, contributorFileName)); got != entry {
		t.Errorf("contributor.yaml = %q, want the entry as it was", got)
	}
	if fake.served[tmdbKey("/3/person/0", "", "")] != 0 || len(fake.requestPath) != 1 {
		t.Errorf("the fact made %v, want the find call alone", fake.requestPath)
	}
	ledger := artLedger(t, folder, factContributorIDs)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("ledger attempts = %+v, want the miss", ledger.Attempts)
	}
}

// The ids gap names an entry that holds only an IMDb id, with that id, and the
// biography gap does not, because the biography call keys on the TMDb id.
func TestTheIDsGapNamesAnEntryWithOnlyAnIMDbID(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	path := ".contributors/to/tom-hanks"
	if err := upsertWalk(t.Context(), catalog, &walkResult{
		contributors: []contributorRow{{Library: contributorLibrary, Path: path, Name: "Tom Hanks", Born: "1956-07-09"}},
		contributorAliases: []contributorAliasRow{
			{Library: contributorLibrary, Scheme: contributorIMDbScheme, ID: "nm0000158", Path: path},
		},
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		fact string
		want []contributorGap
	}{
		{fact: factContributorIDs, want: []contributorGap{{path: path, imdb: "nm0000158"}}},
		{fact: factContributorBiography},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			gaps, err := catalog.contributorGaps(t.Context(), contributorLibrary, test.fact, time.Now().UTC(), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if len(gaps) != len(test.want) || (len(gaps) == 1 && gaps[0] != test.want[0]) {
				t.Errorf("gaps = %+v, want %+v", gaps, test.want)
			}
		})
	}
}

// A find call TMDb refuses is an error attempt, so the next run asks again.
func TestAFindCallTMDbRefusesIsAnErrorAttempt(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\nids: {imdb: nm0000158}\n")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	client, fake := newPersonTMDb(t, map[string]string{})
	fake.statuses[tmdbKey("/3/find/nm0000158", "", "")] = http.StatusUnauthorized

	if work.fillContributorIDs(t.Context(), client, folder,
		contributorGap{path: contributorDirectory("tom-hanks"), imdb: "nm0000158"}) {
		t.Error("the fact reported a write, want none")
	}

	ledger := artLedger(t, folder, factContributorIDs)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("ledger attempts = %+v, want an error", ledger.Attempts)
	}
}
