package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedIdentityGap seeds one title with a path id, which is the shape of an
// identity gap.
func seedIdentityGap(t *testing.T, catalog *Catalog, kind, folder, released string, duration int64) {
	t.Helper()
	seed := &walkResult{}
	if kind == libraryKindSeries {
		seed.series = []seriesRow{{
			Id: "series:path:x", Library: "house/movies", Kind: kind,
			Path: folder, Title: "Twin Peaks", Released: released,
		}}
	} else {
		seed.movies = []movieRow{{
			Id: "movie:path:x", Library: "house/movies", Kind: kind,
			Path: folder, Title: "The Thing", Released: released, Duration: duration,
		}}
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

func TestTheIdentityConcernWritesTheIdIntoTheSidecar(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Thing (1982)"
	writeFile(t, filepath.Join(root, folder, "thing.mkv"), "video")
	seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[` + tmdbResultJSON(1091, "The Thing", "1982-06-25") + `]}`,
	})

	if err := work.identityGap(t.Context(), client); err != nil {
		t.Fatal(err)
	}

	sidecar := readFileString(t, filepath.Join(root, folder, movieSidecarName))
	if !strings.Contains(sidecar, `<uniqueid type="tmdb" default="true">1091</uniqueid>`) {
		t.Errorf("the sidecar holds no id:\n%s", sidecar)
	}
	ledger, err := readLikenLedger(filepath.Join(root, folder), concernIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 1 || ledger.Items[0].ID["tmdb"] != "1091" || ledger.Items[0].Reason != reasonFrom(testTitle, testYear) {
		t.Errorf("ledger items = %+v, want the id and the reason", ledger.Items)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("ledger attempts = %+v, want one that found the id", ledger.Attempts)
	}
	if !strings.Contains(log.String(), "identified") {
		t.Errorf("log = %q, want the line that names the id", log.String())
	}
}

func TestTheIdentityConcernRecordsWhatItLeftForAPerson(t *testing.T) {
	cases := []struct {
		name       string
		answers    map[string]string
		refuse     string
		wantResult string
		wantItems  int
	}{
		{
			name: "two results no rung parts",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[` +
					tmdbResultJSON(1091, "The Thing", "1982-06-25") + `,` +
					tmdbResultJSON(9999, "The Thing", "1982-01-01") + `]}`,
			},
			wantResult: attemptCandidates,
			wantItems:  1,
		},
		{
			name:       "no result at all",
			answers:    nil,
			wantResult: attemptNothing,
			wantItems:  0,
		},
		{
			name:       "a provider that refuses",
			refuse:     tmdbKey("/3/search/movie", "The Thing", "1982"),
			wantResult: attemptError,
			wantItems:  0,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			folder := "The Thing (1982)"
			writeFile(t, filepath.Join(root, folder, "thing.mkv"), "video")
			seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			client, fake := newFakeTMDb(t, test.answers)
			if test.refuse != "" {
				fake.statuses[test.refuse] = http.StatusUnauthorized
			}

			if err := work.identityGap(t.Context(), client); err != nil {
				t.Fatal(err)
			}

			ledger, err := readLikenLedger(filepath.Join(root, folder), concernIdentity)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != test.wantResult {
				t.Fatalf("attempts = %+v, want one %s", ledger.Attempts, test.wantResult)
			}
			if len(ledger.Items) != test.wantItems {
				t.Errorf("items = %+v, want %d", ledger.Items, test.wantItems)
			}
			if _, err := os.Stat(filepath.Join(root, folder, movieSidecarName)); err == nil {
				t.Error("the concern wrote a sidecar for an answer it was not sure of")
			}
		})
	}
}

func TestASeriesTakesItsIdFromTheSeriesSidecar(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Twin Peaks (1990)"
	writeFile(t, filepath.Join(root, folder, "Season 01", "s01e01.mkv"), "video")
	seedIdentityGap(t, catalog, libraryKindSeries, folder, "1990", 0)
	work, _ := testEnricher(t, libraryKindSeries, root, catalog)
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/tv", "Twin Peaks", "1990"): `{"results":[{"id":1920,"name":"Twin Peaks","first_air_date":"1990-04-08"}]}`,
	})

	if err := work.identityGap(t.Context(), client); err != nil {
		t.Fatal(err)
	}

	sidecar := readFileString(t, filepath.Join(root, folder, seriesSidecarName))
	if !strings.Contains(sidecar, "<tvshow>") || !strings.Contains(sidecar, ">1920<") {
		t.Errorf("the series sidecar reads:\n%s", sidecar)
	}
}

func TestTheRuntimeRungReadsTheSidecarTheProbeJustWrote(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Thing (1982)"
	writeFile(t, filepath.Join(root, folder, "thing.mkv"), "video")
	writeFile(t, filepath.Join(root, folder, movieSidecarName),
		"<movie>\n  <title>The Thing</title>\n  <fileinfo><streamdetails><video>"+
			"<durationinseconds>6540</durationinseconds></video></streamdetails></fileinfo>\n</movie>\n")
	seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[` +
			tmdbResultJSON(1091, "The Thing", "1982-06-25") + `,` +
			tmdbResultJSON(9999, "The Thing", "1982-01-01") + `]}`,
		tmdbKey("/3/movie/1091", "", ""): `{"runtime":109}`,
		tmdbKey("/3/movie/9999", "", ""): `{"runtime":42}`,
	})

	if err := work.identityGap(t.Context(), client); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), concernIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 1 || ledger.Items[0].ID["tmdb"] != "1091" {
		t.Fatalf("items = %+v, want the runtime rung's answer", ledger.Items)
	}
	if ledger.Items[0].Reason != reasonFrom(testTitle, testYear, testRuntime) {
		t.Errorf("reason = %q, want the runtime rung's", ledger.Items[0].Reason)
	}
}

func TestTheRuntimeComesOffTheCatalogWhereItHoldsOne(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		duration int64
		sidecar  string
		want     time.Duration
	}{
		{name: "the catalog holds it", kind: libraryKindMovies, duration: 6540, want: 6540 * time.Second},
		{name: "no sidecar and no catalog duration", kind: libraryKindMovies, want: 0},
		{name: "a sidecar that is not XML", kind: libraryKindMovies, sidecar: "<<<", want: 0},
		{name: "a series states none at the title", kind: libraryKindSeries, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.sidecar != "" {
				writeFile(t, filepath.Join(root, movieSidecarName), test.sidecar)
			}
			work, _ := testEnricher(t, test.kind, root, nil)

			got := work.runtimeOf(identityItem{duration: test.duration}, root)

			if got != test.want {
				t.Errorf("runtime = %s, want %s", got, test.want)
			}
		})
	}
}

func TestTheIdentityConcernWorksOverTheFolderItsJobNames(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Thing (1982)", "thing.mkv"), "video")
	writeFile(t, filepath.Join(root, "Alien (1979)", "alien.mkv"), "video")
	seedIdentityGap(t, catalog, libraryKindMovies, "The Thing (1982)", "1982", 0)
	if err := upsertWalk(t.Context(), catalog, &walkResult{movies: []movieRow{{
		Id: "movie:path:alien", Library: "house/movies", Kind: libraryKindMovies,
		Path: "Alien (1979)", Title: "Alien", Released: "1979",
	}}}); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scanPath = "The Thing (1982)"
	work.scope = work.narrowedScope()
	client, fake := newFakeTMDb(t, nil)

	if err := work.identityGap(t.Context(), client); err != nil {
		t.Fatal(err)
	}

	if fake.served[tmdbKey("/3/search/movie", "Alien", "1979")] != 0 {
		t.Error("the concern asked about a title outside the folder its Job named")
	}
	if fake.served[tmdbKey("/3/search/movie", "The Thing", "1982")] == 0 {
		t.Error("the concern asked nothing about the folder its Job named")
	}
}

func TestAnItemThatLeftBetweenTheGapAndTheReadIsSkipped(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()

	item, held, err := catalog.identityItem(ctx, "house/movies", "movie:path:gone")
	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Errorf("read %+v, want no row", item)
	}
}

func TestAnIdentityItemReadsTheTableItsScopeNames(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	seed := &walkResult{
		movies: []movieRow{{Id: "movie:path:x", Library: "house/movies", Path: "M", Title: "M", Released: "1982-06-25", Duration: 99}},
		series: []seriesRow{{Id: "series:path:y", Library: "house/movies", Path: "S", Title: "S", Released: "1990"}},
	}
	if err := upsertWalk(ctx, catalog, seed); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		id   string
		want identityItem
	}{
		{name: "a movie", id: "movie:path:x", want: identityItem{id: "movie:path:x", path: "M", title: "M", year: 1982, duration: 99}},
		{name: "a series", id: "series:path:y", want: identityItem{id: "series:path:y", path: "S", title: "S", year: 1990}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, held, err := catalog.identityItem(ctx, "house/movies", test.id)
			if err != nil || !held {
				t.Fatalf("read held = %v, err = %v", held, err)
			}
			if got != test.want {
				t.Errorf("item = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestTheIdentityConcernFailsWhereItCannotReadItsGap(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(),
		NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))
	client, _ := newFakeTMDb(t, nil)

	if err := work.identityGap(t.Context(), client); err == nil {
		t.Error("the concern reported no error, want the unreachable sidecar's")
	}
}

func TestASidecarThatWillNotTakeTheIdRecordsAnError(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Thing (1982)"
	writeFile(t, filepath.Join(root, folder, movieSidecarName), "this is not xml <<<")
	seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[` + tmdbResultJSON(1091, "The Thing", "1982-06-25") + `]}`,
	})

	if err := work.identityGap(t.Context(), client); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), concernIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want one error", ledger.Attempts)
	}
}
