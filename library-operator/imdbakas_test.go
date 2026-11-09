package main

// what these tests read: the alternate-titles fact reads title.akas once for
// its whole gap, keeps a title's original title and its English, US, and
// British titles, drops the ones the catalog's own title already says,
// records them in the fact's ledger, and puts them in the title's body, where
// the media browser's search reads them.

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// The fact the way the container runs it.
func runAlternateTitles(t *testing.T, work *enricher) {
	t.Helper()
	if err := work.alternateTitlesFact(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// The alternate titles the catalog's body holds for one movie.
func bodyAlternates(t *testing.T, catalog *Catalog, id string) []string {
	t.Helper()
	bodies, err := catalog.queryStrings(t.Context(), `SELECT body FROM movies WHERE id = ?`, []any{id})
	if err != nil || len(bodies) != 1 {
		t.Fatalf("bodies = %v, %v", bodies, err)
	}
	var body movieBody
	if err := json.Unmarshal([]byte(bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	return body.AlternateTitles
}

func TestTheDatasetsWriteAMoviesAlternateTitles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
		nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")

		runAlternateTitles(t, work)

		ledger, err := readLikenLedger(filepath.Dir(nfoPath), factAlternateTitles)
		if err != nil {
			t.Fatal(err)
		}
		want := []alternateTitle{
			{Title: "Port d'hiver", Original: true},
			{Title: "Harbour of Winter", Region: "GB"},
			{Title: "Winter Harbor", Region: "XWW", Language: "en"},
		}
		if !slices.Equal(ledger.AlternateTitles, want) {
			t.Errorf("ledger = %+v, want %+v", ledger.AlternateTitles, want)
		}
		if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound ||
			!ledger.Attempts[0].DatasetModified.Equal(datasetsModified) {
			t.Errorf("attempts = %+v, want one found attempt with the time of title.akas", ledger.Attempts)
		}
		if got := server.log(); !slices.Equal(got, []string{"GET title.akas 200"}) {
			t.Errorf("requests = %v, want one read of title.akas", got)
		}
		wantBody := []string{"Port d'hiver", "Harbour of Winter", "Winter Harbor"}
		if got := bodyAlternates(t, catalog, "movie:imdb:tt9000001"); !slices.Equal(got, wantBody) {
			t.Errorf("body alternates = %v, want %v", got, wantBody)
		}
	})
}

// A title title.akas does not hold records nothing, and the attempt holds it
// out of the gap for the attempt window.
func TestATitleTheAkasDoNotHoldRecordsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		work, catalog, _, root := imdbEnricher(t, libraryKindMovies)
		nfoPath := seedIMDbMovie(t, catalog, root, "tt9000099", "", "")

		runAlternateTitles(t, work)

		ledger, err := readLikenLedger(filepath.Dir(nfoPath), factAlternateTitles)
		if err != nil {
			t.Fatal(err)
		}
		if len(ledger.AlternateTitles) != 0 || len(ledger.Attempts) != 1 ||
			ledger.Attempts[0].Result != attemptNothing {
			t.Errorf("ledger = %+v, want no titles and one nothing attempt", ledger)
		}
		ids, err := work.gaps(t.Context(), factAlternateTitles, time.Now().UTC())
		if err != nil || len(ids) != 0 {
			t.Errorf("gap = %v, %v, want empty after the attempt", ids, err)
		}
	})
}

// A run whose gap holds no title with an IMDb id sends no request.
func TestAnEmptyAlternateTitlesGapReadsNoFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		work, _, server, _ := imdbEnricher(t, libraryKindMovies)

		runAlternateTitles(t, work)

		if got := server.log(); len(got) != 0 {
			t.Errorf("requests = %v, want none", got)
		}
	})
}

// A file IMDb will not serve leaves the gap, records no attempt, and the
// container does not ask for the file again in a later pass.
func TestAnAkasFileIMDbWillNotServeLeavesTheGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
		nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
		server.statuses[datasetTitleAkas] = 503

		runAlternateTitles(t, work)
		runAlternateTitles(t, work)

		if got := server.log(); !slices.Equal(got, []string{"GET title.akas 503"}) {
			t.Errorf("requests = %v, want one refused read", got)
		}
		if attempts := attemptsBeside(t, nfoPath, factAlternateTitles); len(attempts) != 0 {
			t.Errorf("attempts = %+v, want none", attempts)
		}
	})
}

// The choice among one title's rows: the original first, then the English,
// US, and British titles in IMDb's order, none the catalog's title already
// says, none twice, and no more than the cap.
func TestTheAlternatesOfATitle(t *testing.T) {
	many := []akaRow{}
	for _, name := range []string{"One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight"} {
		many = append(many, akaRow{title: "Harbour " + name, region: "US"})
	}
	cases := []struct {
		name  string
		rows  []akaRow
		title string
		want  []string
	}{
		{name: "the original leads whatever its place",
			rows: []akaRow{{title: "Harbour", region: "GB"}, {title: "Hamn", original: true}},
			want: []string{"Hamn", "Harbour"}},
		{name: "another region in another language is left out",
			rows: []akaRow{{title: "Hafen", region: "DE", language: "de"}, {title: "Harbour", region: "XWW", language: "en"}},
			want: []string{"Harbour"}},
		{name: "a US title in another language is left out",
			rows: []akaRow{{title: "Puerto de invierno", region: "US", language: "es"}, {title: "Harbour", region: "US"}},
			want: []string{"Harbour"}},
		{name: "the catalog's own title is left out in any case and spacing",
			rows:  []akaRow{{title: "  winter HARBOUR ", region: "US"}, {title: "Harbour", region: "US"}},
			title: "Winter Harbour", want: []string{"Harbour"}},
		{name: "a title that differs only in its marks is left out",
			rows:  []akaRow{{title: "Winter Harbour!", region: "US"}},
			title: "Winter Harbour", want: nil},
		{name: "a repeat is kept once",
			rows: []akaRow{{title: "Harbour", region: "US"}, {title: "Harbour", region: "GB"}},
			want: []string{"Harbour"}},
		{name: "the cap", rows: many,
			want: []string{"Harbour One", "Harbour Two", "Harbour Three", "Harbour Four", "Harbour Five"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			var got []string
			for _, kept := range chooseAlternates(one.rows, one.title) {
				got = append(got, kept.Title)
			}
			if !slices.Equal(got, one.want) {
				t.Errorf("alternates = %v, want %v", got, one.want)
			}
		})
	}
}

// The gap is every movie and series with an IMDb id and no found attempt. A
// refresh opens a found title again.
func TestTheAlternateTitlesGapAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	now := time.Now().UTC()
	seed := &walkResult{
		movies: []movieRow{
			{Id: "movie:tmdb:1", Library: "house/movies", Path: "One (2001)", Title: "One"},
			{Id: "movie:tmdb:2", Library: "house/movies", Path: "Two (2002)", Title: "Two"},
			{Id: "movie:tmdb:3", Library: "house/movies", Path: "Three (2003)", Title: "Three"},
		},
		series: []seriesRow{
			{Id: "series:tvdb:9", Library: "house/movies", Path: "Ninefold (2009)", Title: "Ninefold"},
		},
		aliases: []aliasRow{
			{Alias: "movie:imdb:tt1", Library: "house/movies", Item: "movie:tmdb:1"},
			{Alias: "movie:imdb:tt2", Library: "house/movies", Item: "movie:tmdb:2"},
			{Alias: "series:imdb:tt9", Library: "house/movies", Item: "series:tvdb:9"},
		},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{{Library: "house/movies",
		Item: "movie:tmdb:2", Fact: factAlternateTitles, At: now.Add(-60 * 24 * time.Hour).Unix(),
		Result: attemptFound}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		refresh time.Time
		want    []string
	}{
		{name: "a found title stays answered", want: []string{"movie:tmdb:1", "series:tvdb:9"}},
		{name: "a refresh opens it", refresh: now,
			want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			ids, err := catalog.queryStrings(t.Context(), gapQueries[factAlternateTitles],
				gapParams(factAlternateTitles, "house/movies", now, reopenTime{refresh: one.refresh}))
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, one.want) {
				t.Errorf("gap = %v, want %v", ids, one.want)
			}
		})
	}
}

// The gap finds the titles with an IMDb id through the aliases table's
// primary key, so the count the reporter takes for every library does not
// read every alias of the namespace.
func TestTheAlternateTitlesGapReadsTheAliasesByTheirKey(t *testing.T) {
	_, agent := newSQLiteCatalog(t)
	params := gapParams(factAlternateTitles, "house/movies", time.Now().UTC(), reopenTime{})
	rows, err := agent.db.Query(`EXPLAIN QUERY PLAN `+gapQueries[factAlternateTitles], params...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	for _, line := range plan {
		if strings.Contains(line, "SCAN aliases") {
			t.Errorf("the gap scans the aliases table: %v", plan)
		}
	}
	if !slices.ContainsFunc(plan, func(line string) bool { return strings.Contains(line, "SEARCH aliases") }) {
		t.Errorf("the gap does not search the aliases by their key: %v", plan)
	}
}
