package main

// what these tests read: which titles each nfo fact's gap holds, against the
// shipped schema in a real database, and what a sidecar says it already
// answers.

import (
	"slices"
	"testing"
	"time"
)

// the four titles a gap test reads: one with nothing, one with the overview and
// the credits, one no provider has named, and one series.
func seedNFOFactRows(t *testing.T, catalog *Catalog) {
	t.Helper()
	seed := &walkResult{
		movies: []movieRow{
			{Id: "movie:tmdb:1", Library: "house/movies", Path: "One (2001)", Title: "One"},
			{
				Id: "movie:tmdb:2", Library: "house/movies", Path: "Two (2002)", Title: "Two",
				NFOFacts: nfoFactList([]string{factOverview, factRatingIMDb, factCredits}),
			},
			{Id: "movie:path:three-2003", Library: "house/movies", Path: "Three (2003)", Title: "Three"},
		},
		series: []seriesRow{
			{Id: "series:tvdb:9", Library: "house/movies", Path: "Nine (2009)", Title: "Nine"},
		},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

func TestAnNFOGapHoldsTheTitlesWhoseSidecarLacksTheFactAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedNFOFactRows(t, catalog)
	now := time.Now().UTC()

	cases := []struct {
		fact string
		want []string
	}{
		{fact: factOverview, want: []string{"movie:tmdb:1", "series:tvdb:9"}},
		{fact: factCertification, want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
		{fact: factRatingTMDb, want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
		{fact: factRatingIMDb, want: []string{"movie:tmdb:1", "series:tvdb:9"}},
		{fact: factRatingRottenTomatoes, want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
		{fact: factRatingMetacritic, want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
		{fact: factCredits, want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			ids, err := catalog.queryStrings(t.Context(), gapQueries[test.fact],
				gapParams("house/movies", now))
			if err != nil {
				t.Fatal(err)
			}

			slices.Sort(ids)
			if !slices.Equal(ids, test.want) {
				t.Errorf("gap = %v, want %v", ids, test.want)
			}
		})
	}
}

// The credits gap is credits.yaml and never the sidecar's actors: a title
// Jellyfin gave a cast is a gap until the fact wrote the file, and a title
// whose providers named no cast holds it through the attempt window.
func TestTheCreditsGapIsATitleWithNoCreditsFileAgainstTheRealSchema(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name     string
		credits  []creditRow
		attempts []attemptRow
		want     []string
	}{
		{
			name: "every title with an id, the one Jellyfin gave a cast included",
			want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"},
		},
		{
			name: "a title whose credits.yaml names a person",
			credits: []creditRow{{
				Library: "house/movies", Item: "movie:tmdb:2", Billing: 0,
				Name: "Nora Vance", Contributor: ".contributors/n/nora-vance",
			}},
			want: []string{"movie:tmdb:1", "series:tvdb:9"},
		},
		{
			name: "a title whose providers named no cast, inside the window",
			attempts: []attemptRow{{
				Library: "house/movies", Item: "movie:tmdb:1", Fact: factCredits,
				At: now.Unix(), Result: attemptFound, Provider: "tmdb",
			}},
			want: []string{"movie:tmdb:2", "series:tvdb:9"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedNFOFactRows(t, catalog)
			if _, err := catalog.UpsertCredits(t.Context(), test.credits); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.UpsertAttempts(t.Context(), test.attempts); err != nil {
				t.Fatal(err)
			}

			ids, err := catalog.queryStrings(t.Context(), gapQueries[factCredits],
				gapParams("house/movies", now))
			if err != nil {
				t.Fatal(err)
			}

			slices.Sort(ids)
			if !slices.Equal(ids, test.want) {
				t.Errorf("gap = %v, want %v", ids, test.want)
			}
		})
	}
}

// An nfo gap holds a title again only after that title's last attempt has
// passed the window its own kind carries.
func TestEveryAttemptKindGatesTheNFOGap(t *testing.T) {
	for _, test := range attemptWindowCases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			now := time.Now().UTC()
			seed := &walkResult{movies: []movieRow{
				{Id: "movie:tmdb:1", Library: "house/movies", Path: "One (2001)", Title: "One"},
			}}
			if err := upsertWalk(t.Context(), catalog, seed); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{{
				Library: "house/movies", Item: "movie:tmdb:1", Fact: factOverview,
				At: now.Add(-test.age).Unix(), Result: test.result, Provider: "tmdb",
			}}); err != nil {
				t.Fatal(err)
			}

			ids, err := catalog.queryStrings(t.Context(), gapQueries[factOverview],
				gapParams("house/movies", now))
			if err != nil {
				t.Fatal(err)
			}

			if len(ids) != test.wantGap {
				t.Errorf("gap = %v, want %d", ids, test.wantGap)
			}
		})
	}
}

// The reporter counts every gap with the same query the container works from,
// so the nfo facts arrive in the report with the two facts of plan 29.
func TestTheGapCountsHoldEveryNFOFact(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedNFOFactRows(t, catalog)

	counts, err := catalog.gapCounts(t.Context(), "house/movies", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	for _, fact := range nfoFacts {
		if _, held := counts[fact]; !held {
			t.Errorf("counts = %v, want a count for %s", counts, fact)
		}
	}
	if counts[factOverview] != 2 || counts[factCertification] != 3 {
		t.Errorf("counts = %v, want two titles with no overview and three with no certification", counts)
	}
}

// The fights a person reads on the Library are the attempts every fact left
// because another writer holds the group.
func TestTheFightCountReadsEveryFactsFights(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	now := time.Now().UTC().Unix()
	if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{
		{Library: "house/movies", Item: "movie:tmdb:1", Fact: factOverview, At: now, Result: attemptFight},
		{Library: "house/movies", Item: "movie:tmdb:1", Fact: factCredits, At: now, Result: attemptFight},
		{Library: "house/movies", Item: "movie:tmdb:2", Fact: factOverview, At: now, Result: attemptFound},
		{Library: "house/series", Item: "series:tvdb:9", Fact: factOverview, At: now, Result: attemptFight},
	}); err != nil {
		t.Fatal(err)
	}

	fights, err := catalog.fightCount(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	if fights != 2 {
		t.Errorf("fights = %d, want the two this library left", fights)
	}
}

// The scanner reads which facts a sidecar answers off the elements it holds, so
// a title Jellyfin filled is no gap at all.
func TestASidecarSaysWhichFactsItAnswers(t *testing.T) {
	cases := []struct {
		name    string
		sidecar string
		want    []string
	}{
		{
			name:    "a sidecar with the title alone",
			sidecar: "<movie><title>One</title></movie>",
			want:    nil,
		},
		{
			name: "a sidecar Jellyfin filled",
			sidecar: `<movie><title>One</title><plot>A plot.</plot><mpaa>PG</mpaa>` +
				`<ratings><rating name="themoviedb" max="10"><value>8</value></rating></ratings>` +
				`<actor><name>Nora Vance</name></actor></movie>`,
			want: []string{factOverview, factCertification, factRatingTMDb},
		},
		{
			name: "a sidecar with the rating of each site",
			sidecar: `<movie><title>One</title><ratings>` +
				`<rating name="imdb" max="10"><value>7</value></rating>` +
				`<rating name="tomatometerallcritics" max="100"><value>91</value></rating>` +
				`<rating name="metacritic" max="100"><value>76</value></rating>` +
				`</ratings></movie>`,
			want: []string{factRatingIMDb, factRatingRottenTomatoes, factRatingMetacritic},
		},
		{
			name: "a sidecar with a site no fact holds",
			sidecar: `<movie><title>One</title>` +
				`<ratings><rating name="trakt" max="10"><value>7</value></rating></ratings></movie>`,
			want: nil,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			meta, err := parseMovieNFO([]byte(test.sidecar))
			if err != nil {
				t.Fatal(err)
			}

			if meta.NFOFacts != nfoFactList(test.want) {
				t.Errorf("answers = %q, want %q", meta.NFOFacts, nfoFactList(test.want))
			}
		})
	}
}
