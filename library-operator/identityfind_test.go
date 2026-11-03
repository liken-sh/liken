package main

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

// The series .nfo file Sonarr writes when its metadata service has no TMDb
// mapping for the show: TheTVDB, IMDb, and TVmaze ids, and no TMDb id.
const sonarrSeriesNFO = "<tvshow>\n  <title>Pine Hollow</title>\n" +
	"  <uniqueid type=\"tvdb\" default=\"true\">113981</uniqueid>\n" +
	"  <uniqueid type=\"imdb\">tt0099864</uniqueid>\n" +
	"  <uniqueid type=\"tvmaze\">9001</uniqueid>\n</tvshow>\n"

const pineHollow = "Pine Hollow (1990)"

// Two ids that name two different titles on TMDb leave the choice to a
// person, and the title counts as waiting until one names its TMDb id.
func TestIDsThatNameTwoTitlesWaitForAPerson(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		root := t.TempDir()
		seedTitleWithNFO(t, catalog, libraryKindSeries, root, pineHollow, sonarrSeriesNFO)
		work, _ := testEnricher(t, libraryKindSeries, root, catalog)
		client, _ := newFakeTMDb(t, map[string]string{
			tmdbKey("/3/find/113981", "", ""):    `{"movie_results":[],"tv_results":[{"id":19614,"name":"Pine Hollow","first_air_date":"1990-11-18"}]}`,
			tmdbKey("/3/find/tt0099864", "", ""): `{"movie_results":[],"tv_results":[{"id":5150,"name":"Pine Hollow","first_air_date":"2017-09-08"}]}`,
		})

		if err := work.identityGap(t.Context(), client); err != nil {
			t.Fatal(err)
		}

		nfo := readFileString(t, filepath.Join(root, pineHollow, seriesNFOName))
		if strings.Contains(nfo, `type="tmdb"`) {
			t.Errorf("the fact wrote a TMDb id it was not sure of:\n%s", nfo)
		}
		ledger, err := readLikenLedger(filepath.Join(root, pineHollow), factIdentity)
		if err != nil {
			t.Fatal(err)
		}
		if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptCandidates {
			t.Fatalf("attempts = %+v, want one with candidates", ledger.Attempts)
		}
		want := []likenCandidate{
			{ID: providerIDs{"tmdb": "19614"}, Title: "Pine Hollow", Year: 1990, Receipt: map[string]string{"find": "tvdb 113981"}},
			{ID: providerIDs{"tmdb": "5150"}, Title: "Pine Hollow", Year: 2017, Receipt: map[string]string{"find": "imdb tt0099864"}},
		}
		if len(ledger.Items) != 1 || len(ledger.Items[0].Candidates) != len(want) {
			t.Fatalf("items = %+v, want the two candidates", ledger.Items)
		}
		for at, candidate := range ledger.Items[0].Candidates {
			if candidate.ID["tmdb"] != want[at].ID["tmdb"] || candidate.Year != want[at].Year ||
				candidate.Receipt["find"] != want[at].Receipt["find"] {
				t.Errorf("candidate %d = %+v, want %+v", at, candidate, want[at])
			}
		}
		waiting, _, err := catalog.identityCounts(t.Context(), "house/movies")
		if err != nil {
			t.Fatal(err)
		}
		if waiting != 1 {
			t.Errorf("waiting = %d, want the series", waiting)
		}
	})
}

// An id TMDb holds no title for counts as unresolved, the same as a folder
// name TMDb cannot match.
func TestIDsTMDbCannotFindCountAsUnresolved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		root := t.TempDir()
		seedTitleWithNFO(t, catalog, libraryKindSeries, root, pineHollow, sonarrSeriesNFO)
		work, _ := testEnricher(t, libraryKindSeries, root, catalog)
		client, _ := newFakeTMDb(t, map[string]string{
			tmdbKey("/3/find/113981", "", ""):    `{"movie_results":[],"tv_results":[]}`,
			tmdbKey("/3/find/tt0099864", "", ""): `{"movie_results":[],"tv_results":[]}`,
		})

		if err := work.identityGap(t.Context(), client); err != nil {
			t.Fatal(err)
		}

		_, unresolved, err := catalog.identityCounts(t.Context(), "house/movies")
		if err != nil {
			t.Fatal(err)
		}
		if unresolved != 1 {
			t.Errorf("unresolved = %d, want the series", unresolved)
		}
	})
}

// The identity ledger as the fact left it after it found the series' TMDb id,
// with the TheTVDB id the .nfo file named then.
func foundLedger(tvdb string) string {
	return "items:\n  - path: .\n    id: {imdb: tt0099864, tmdb: 19614, tvdb: " + tvdb + "}\n" +
		"    reason: find by tvdb 113981 and imdb tt0099864\n"
}

// Sonarr's rewrite of tvshow.nfo drops the TMDb id. The fact writes back the
// id its ledger holds without asking TMDb, as long as the ids the ledger and
// the .nfo file share still agree.
func TestTheLedgerRestoresAnIDARewriteDropped(t *testing.T) {
	cases := []struct {
		name      string
		ledger    string
		wantTMDb  bool
		wantFinds int
	}{
		{name: "the ids agree", ledger: foundLedger("113981"), wantTMDb: true, wantFinds: 0},
		{name: "a person changed the TheTVDB id", ledger: foundLedger("999999"), wantTMDb: false, wantFinds: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				root := t.TempDir()
				writeFile(t, filepath.Join(root, pineHollow, likenDirectory, likenLedgerName(factIdentity)), test.ledger)
				seedTitleWithNFO(t, catalog, libraryKindSeries, root, pineHollow, sonarrSeriesNFO)
				work, _ := testEnricher(t, libraryKindSeries, root, catalog)
				client, fake := newFakeTMDb(t, map[string]string{
					tmdbKey("/3/find/113981", "", ""):    `{"movie_results":[],"tv_results":[]}`,
					tmdbKey("/3/find/tt0099864", "", ""): `{"movie_results":[],"tv_results":[]}`,
				})

				if err := work.identityGap(t.Context(), client); err != nil {
					t.Fatal(err)
				}

				nfo := readFileString(t, filepath.Join(root, pineHollow, seriesNFOName))
				if got := strings.Contains(nfo, `<uniqueid type="tmdb">19614</uniqueid>`); got != test.wantTMDb {
					t.Errorf("the .nfo file holds the TMDb id = %v, want %v:\n%s", got, test.wantTMDb, nfo)
				}
				if got := fake.served[tmdbKey("/3/find/113981", "", "")]; got != test.wantFinds {
					t.Errorf("find calls = %d, want %d", got, test.wantFinds)
				}
			})
		})
	}
}
