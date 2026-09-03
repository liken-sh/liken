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
				NFOFacts: nfoFactList([]string{factOverview, factCredits}),
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
	cutoff := time.Now().UTC().Add(-defaultRetryInterval).Unix()

	cases := []struct {
		fact string
		want []string
	}{
		{fact: factOverview, want: []string{"movie:tmdb:1", "series:tvdb:9"}},
		{fact: factCertification, want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
		{fact: factRatingTMDb, want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
		{fact: factCredits, want: []string{"movie:tmdb:1", "series:tvdb:9"}},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			ids, err := catalog.queryStrings(t.Context(), gapQueries[test.fact],
				[]any{"house/movies", cutoff})
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

// A title tried inside the retry window is out of the gap, and a title whose
// try ended in an error is back in it.
func TestAnNFOGapExcludesATitleTriedInsideTheWindow(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedNFOFactRows(t, catalog)
	now := time.Now().UTC()
	if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{
		{
			Library: "house/movies", Item: "movie:tmdb:1", Fact: factOverview,
			At: now.Unix(), Result: attemptNothing, Provider: "tmdb",
		},
		{
			Library: "house/movies", Item: "series:tvdb:9", Fact: factOverview,
			At: now.Unix(), Result: attemptError, Provider: "tmdb",
		},
	}); err != nil {
		t.Fatal(err)
	}

	ids, err := catalog.queryStrings(t.Context(), gapQueries[factOverview],
		[]any{"house/movies", now.Add(-defaultRetryInterval).Unix()})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(ids, []string{"series:tvdb:9"}) {
		t.Errorf("gap = %v, want the title whose try ended in an error", ids)
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
			want: []string{factOverview, factCertification, factRatingTMDb, factCredits},
		},
		{
			name: "a sidecar with another site's rating alone",
			sidecar: `<movie><title>One</title>` +
				`<ratings><rating name="imdb" max="10"><value>7</value></rating></ratings></movie>`,
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
