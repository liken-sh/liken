package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

func TestTheIdentityGapHoldsEveryTitleMissingAnID(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		aliases []string
		want    int
	}{
		{name: "a movie with a path id", id: "movie:path:one", want: 1},
		{name: "a movie with an imdb id alone", id: "movie:imdb:tt1", aliases: []string{"movie:imdb:tt1"}, want: 1},
		{name: "a movie with a tmdb id alone", id: "movie:tmdb:1", aliases: []string{"movie:tmdb:1"}, want: 1},
		{name: "a movie with both", id: "movie:tmdb:1", aliases: []string{"movie:tmdb:1", "movie:imdb:tt1"}, want: 0},
		{name: "a series with a tvdb and an imdb id", id: "series:tvdb:7", aliases: []string{"series:tvdb:7", "series:imdb:tt7"}, want: 1},
		{name: "a series with all three", id: "series:tvdb:7", aliases: []string{"series:tvdb:7", "series:tmdb:3", "series:imdb:tt7"}, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seed := &walkResult{}
			if isMovieID(test.id) {
				seed.movies = []movieRow{{Id: test.id, Library: "house/movies", Kind: libraryKindMovies, Path: "One", Title: "One"}}
			} else {
				seed.series = []seriesRow{{Id: test.id, Library: "house/movies", Kind: libraryKindSeries, Path: "One", Title: "One"}}
			}
			for _, alias := range test.aliases {
				seed.aliases = append(seed.aliases, aliasRow{Alias: alias, Library: "house/movies", Item: test.id, Source: aliasSourceProvider})
			}
			if err := upsertWalk(t.Context(), catalog, seed); err != nil {
				t.Fatal(err)
			}

			gaps, err := catalog.gapCounts(t.Context(), "house/movies", ledgerTime, reopenTimes{})
			if err != nil {
				t.Fatal(err)
			}

			if gaps[factIdentity] != test.want {
				t.Errorf("identity gap = %d, want %d", gaps[factIdentity], test.want)
			}
		})
	}
}

// seedTitleWithNFO writes one title folder with its .nfo file and walks it
// into the catalog, so the title holds the ids that .nfo file names. An empty
// .nfo leaves the folder without one, so the title has a path id.
func seedTitleWithNFO(t *testing.T, catalog *Catalog, kind, root, folder, nfo string) {
	t.Helper()
	nfoName := movieNFOName
	video := filepath.Join(root, folder, "survey.mkv")
	if kind == libraryKindSeries {
		nfoName = seriesNFOName
		video = filepath.Join(root, folder, "Season 01", "Pine Hollow - S01E01.mkv")
	}
	writeFile(t, video, "video")
	if nfo != "" {
		writeFile(t, filepath.Join(root, folder, nfoName), nfo)
	}
	walk := walkMovies(root, "house/movies", nil)
	if kind == libraryKindSeries {
		walk = walkSeries(root, "house/movies", nil)
	}
	if err := upsertWalk(t.Context(), catalog, walk); err != nil {
		t.Fatal(err)
	}
}

// A title that came with one id from another tool or a person gains the ids
// it lacks, and the ids it held stay as they were.
func TestATitleWithOneIDGainsTheOthers(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		folder   string
		nfo      string
		answers  map[string]string
		wantNFO  []string
		wantID   string
		wantFrom string
		// The id the title held before, which a play recorded under it
		// still names, so it has to resolve to the title's id now.
		heldAlias string
	}{
		{
			name:   "a movie with an imdb id alone",
			kind:   libraryKindMovies,
			folder: "The Long Survey (1982)",
			nfo:    "<movie>\n  <title>The Long Survey</title>\n  <uniqueid type=\"imdb\" default=\"true\">tt9000011</uniqueid>\n</movie>\n",
			answers: map[string]string{
				tmdbKey("/3/find/tt9000011", "", ""):          `{"movie_results":[{"id":1101}],"tv_results":[]}`,
				tmdbKey("/3/movie/1101/external_ids", "", ""): `{"imdb_id":"tt9000011","tvdb_id":12345}`,
			},
			wantNFO: []string{
				`<uniqueid type="imdb" default="true">tt9000011</uniqueid>`,
				`<uniqueid type="tmdb">1101</uniqueid>`,
				`<uniqueid type="tvdb">12345</uniqueid>`,
			},
			wantID:    "movie:tmdb:1101",
			wantFrom:  "find by imdb tt9000011",
			heldAlias: "movie:imdb:tt9000011",
		},
		{
			name:   "a movie whose ids carry no default mark",
			kind:   libraryKindMovies,
			folder: "The Long Survey (1982)",
			nfo:    "<movie>\n  <title>The Long Survey</title>\n  <uniqueid type=\"imdb\">tt9000011</uniqueid>\n</movie>\n",
			answers: map[string]string{
				tmdbKey("/3/find/tt9000011", "", ""):          `{"movie_results":[{"id":1101}],"tv_results":[]}`,
				tmdbKey("/3/movie/1101/external_ids", "", ""): `{"imdb_id":"tt9000011","tvdb_id":0}`,
			},
			wantNFO: []string{
				`<uniqueid type="imdb">tt9000011</uniqueid>`,
				`<uniqueid type="tmdb" default="true">1101</uniqueid>`,
			},
			wantID:    "movie:tmdb:1101",
			wantFrom:  "find by imdb tt9000011",
			heldAlias: "movie:imdb:tt9000011",
		},
		{
			name:   "a series with a tvdb and an imdb id",
			kind:   libraryKindSeries,
			folder: "Pine Hollow (1990)",
			nfo: "<tvshow>\n  <title>Pine Hollow</title>\n  <uniqueid type=\"tvdb\" default=\"true\">389492</uniqueid>\n" +
				"  <uniqueid type=\"imdb\">tt7000007</uniqueid>\n</tvshow>\n",
			answers: map[string]string{
				tmdbKey("/3/find/389492", "", ""):          `{"movie_results":[],"tv_results":[{"id":2103}]}`,
				tmdbKey("/3/find/tt7000007", "", ""):       `{"movie_results":[],"tv_results":[{"id":2103}]}`,
				tmdbKey("/3/tv/2103/external_ids", "", ""): `{"imdb_id":"tt7000007","tvdb_id":389492}`,
			},
			wantNFO: []string{
				`<uniqueid type="tvdb" default="true">389492</uniqueid>`,
				`<uniqueid type="imdb">tt7000007</uniqueid>`,
				`<uniqueid type="tmdb">2103</uniqueid>`,
			},
			wantID:    "series:tvdb:389492",
			wantFrom:  "find by tvdb 389492 and imdb tt7000007",
			heldAlias: "series:tvdb:389492",
		},
		{
			name:   "a movie a person named by its tmdb id",
			kind:   libraryKindMovies,
			folder: "The Long Survey (1982)",
			nfo:    "<movie>\n  <title>The Long Survey</title>\n  <uniqueid type=\"tmdb\">1101</uniqueid>\n</movie>\n",
			answers: map[string]string{
				tmdbKey("/3/movie/1101/external_ids", "", ""): `{"imdb_id":"tt9000011","tvdb_id":0}`,
			},
			wantNFO: []string{
				`<uniqueid type="tmdb">1101</uniqueid>`,
				`<uniqueid type="imdb">tt9000011</uniqueid>`,
			},
			wantID:    "movie:tmdb:1101",
			wantFrom:  "its tmdb id",
			heldAlias: "movie:tmdb:1101",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				root := t.TempDir()
				seedTitleWithNFO(t, catalog, test.kind, root, test.folder, test.nfo)
				work, _ := testEnricher(t, test.kind, root, catalog)
				client, fake := newFakeTMDb(t, test.answers)

				if err := work.identityGap(t.Context(), client); err != nil {
					t.Fatal(err)
				}

				nfoName := movieNFOName
				if test.kind == libraryKindSeries {
					nfoName = seriesNFOName
				}
				nfo := readFileString(t, filepath.Join(root, test.folder, nfoName))
				for _, want := range test.wantNFO {
					if !strings.Contains(nfo, want) {
						t.Errorf("the .nfo file holds no %s:\n%s", want, nfo)
					}
				}
				if strings.Count(nfo, `default="true"`) > 1 {
					t.Errorf("the .nfo file marks more than one default:\n%s", nfo)
				}
				for key := range fake.served {
					if strings.Contains(key, "/search/") {
						t.Errorf("the fact searched by title, %s, for a title that held an id", key)
					}
				}
				ledger, err := readLikenLedger(filepath.Join(root, test.folder), factIdentity)
				if err != nil {
					t.Fatal(err)
				}
				if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
					t.Errorf("attempts = %+v, want one found", ledger.Attempts)
				}
				if len(ledger.Items) != 1 || ledger.Items[0].Reason != test.wantFrom {
					t.Errorf("items = %+v, want the reason %q", ledger.Items, test.wantFrom)
				}
				// Past the dated window, the attempt holds nothing, so only the
				// ids the title now holds keep it out of the gap.
				gaps, err := catalog.gapCounts(t.Context(), "house/movies", ledger.Attempts[0].At.Add(2*defaultRetryInterval), reopenTimes{})
				if err != nil {
					t.Fatal(err)
				}
				if gaps[factIdentity] != 0 {
					t.Errorf("identity gap = %d after the fact ran, want none", gaps[factIdentity])
				}
				ids, err := catalog.titleIDs(t.Context(), "house/movies", test.wantID)
				if err != nil {
					t.Fatal(err)
				}
				if ids["tmdb"] == "" || ids["imdb"] == "" {
					t.Errorf("the catalog holds %v for %s, want its tmdb and imdb ids", ids, test.wantID)
				}
				items, err := catalog.queryStrings(t.Context(), `SELECT item FROM aliases WHERE library = ? AND alias = ?`,
					[]any{"house/movies", test.heldAlias})
				if err != nil {
					t.Fatal(err)
				}
				if len(items) != 1 || items[0] != test.wantID {
					t.Errorf("the alias %s names %v, want %s", test.heldAlias, items, test.wantID)
				}
			})
		})
	}
}

// An id TMDb holds no title for leaves the title as it was. The ladder does
// not search by name for it, because the id came from a person or a tool
// and a name search could contradict it.
func TestAnIDTMDbCannotFindRecordsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		root := t.TempDir()
		folder := "The Long Survey (1982)"
		seedTitleWithNFO(t, catalog, libraryKindMovies, root, folder,
			"<movie>\n  <title>The Long Survey</title>\n  <uniqueid type=\"imdb\">tt9000011</uniqueid>\n</movie>\n")
		work, _ := testEnricher(t, libraryKindMovies, root, catalog)
		client, fake := newFakeTMDb(t, map[string]string{
			tmdbKey("/3/find/tt9000011", "", ""): `{"movie_results":[],"tv_results":[]}`,
		})

		if err := work.identityGap(t.Context(), client); err != nil {
			t.Fatal(err)
		}

		ledger, err := readLikenLedger(filepath.Join(root, folder), factIdentity)
		if err != nil {
			t.Fatal(err)
		}
		if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
			t.Errorf("attempts = %+v, want one that found nothing", ledger.Attempts)
		}
		if fake.served[tmdbKey("/3/search/movie", "The Long Survey", "1982")] != 0 {
			t.Error("the fact searched by title for a title that held an id")
		}
	})
}

// A failed call leaves an error or a partial attempt, which the error window
// opens again, so a later run asks TMDb for the ids this one did not get.
func TestAFailedCallIsAskedAgainAfterTheErrorWindow(t *testing.T) {
	cases := []struct {
		name   string
		nfo    string
		refuse string
		want   string
	}{
		{
			name:   "the find call",
			nfo:    "<movie>\n  <title>The Long Survey</title>\n  <uniqueid type=\"imdb\">tt9000011</uniqueid>\n</movie>\n",
			refuse: tmdbKey("/3/find/tt9000011", "", ""),
			want:   attemptError,
		},
		{
			name:   "the external ids call for a title that held its tmdb id",
			nfo:    "<movie>\n  <title>The Long Survey</title>\n  <uniqueid type=\"tmdb\">1101</uniqueid>\n</movie>\n",
			refuse: tmdbKey("/3/movie/1101/external_ids", "", ""),
			want:   attemptError,
		},
		{
			name:   "the external ids call after the ladder wrote the tmdb id",
			refuse: tmdbKey("/3/movie/1101/external_ids", "", ""),
			want:   attemptPartial,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				root := t.TempDir()
				folder := "The Long Survey (1982)"
				seedTitleWithNFO(t, catalog, libraryKindMovies, root, folder, test.nfo)
				work, _ := testEnricher(t, libraryKindMovies, root, catalog)
				client, fake := newFakeTMDb(t, map[string]string{
					tmdbKey("/3/find/tt9000011", "", ""): `{"movie_results":[{"id":1101}],"tv_results":[]}`,
					tmdbKey("/3/search/movie", "The Long Survey", "1982"): `{"results":[` +
						tmdbResultJSON(1101, "The Long Survey", "1982-05-14") + `]}`,
				})
				fake.statuses[test.refuse] = http.StatusUnauthorized

				if err := work.identityGap(t.Context(), client); err != nil {
					t.Fatal(err)
				}

				ledger, err := readLikenLedger(filepath.Join(root, folder), factIdentity)
				if err != nil {
					t.Fatal(err)
				}
				if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != test.want {
					t.Fatalf("attempts = %+v, want one %s", ledger.Attempts, test.want)
				}
				if err := upsertWalk(t.Context(), catalog, walkMovies(root, "house/movies", nil)); err != nil {
					t.Fatal(err)
				}
				gaps, err := catalog.gapCounts(t.Context(), "house/movies", ledger.Attempts[0].At.Add(2*errorRetryInterval), reopenTimes{})
				if err != nil {
					t.Fatal(err)
				}
				if gaps[factIdentity] != 1 {
					t.Errorf("identity gap past the error window = %d, want the title", gaps[factIdentity])
				}
			})
		})
	}
}
