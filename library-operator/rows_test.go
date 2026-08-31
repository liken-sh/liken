package main

// These tests prove the identity helpers off small tables,
// so the provider order, the path fallback, the episode ids, the sort
// key, and the slug are fixed without a volume or a catalog.

import (
	"reflect"
	"testing"
)

func TestItemID(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		providers map[string]string
		folderKey string
		want      string
	}{
		{name: "movie tmdb", kind: scopeMovie, providers: map[string]string{"tmdb": "603"}, folderKey: "the-matrix", want: "movie:tmdb:603"},
		{name: "movie prefers tmdb over imdb", kind: scopeMovie, providers: map[string]string{"imdb": "tt0133093", "tmdb": "603"}, folderKey: "the-matrix", want: "movie:tmdb:603"},
		{name: "movie falls to imdb", kind: scopeMovie, providers: map[string]string{"imdb": "tt0133093"}, folderKey: "the-matrix", want: "movie:imdb:tt0133093"},
		{name: "series prefers tvdb", kind: scopeSeries, providers: map[string]string{"tvdb": "81189", "tmdb": "1396"}, folderKey: "breaking-bad", want: "series:tvdb:81189"},
		{name: "series falls to tmdb", kind: scopeSeries, providers: map[string]string{"tmdb": "1396", "imdb": "tt0903747"}, folderKey: "breaking-bad", want: "series:tmdb:1396"},
		{name: "no provider falls to path", kind: scopeMovie, providers: nil, folderKey: "unknown-2001", want: "movie:path:unknown-2001"},
		{name: "empty provider value is absent", kind: scopeMovie, providers: map[string]string{"tmdb": ""}, folderKey: "unknown-2001", want: "movie:path:unknown-2001"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := itemID(testCase.kind, testCase.providers, testCase.folderKey)
			if got != testCase.want {
				t.Errorf("itemID = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestEpisodeID(t *testing.T) {
	cases := []struct {
		name     string
		seriesID string
		season   int
		episode  int
		want     string
	}{
		{name: "provider series", seriesID: "series:tvdb:81189", season: 2, episode: 5, want: "episode:tvdb:81189:s02e05"},
		{name: "double digit", seriesID: "series:tvdb:81189", season: 12, episode: 10, want: "episode:tvdb:81189:s12e10"},
		{name: "path fallback series", seriesID: "series:path:home-movies", season: 1, episode: 1, want: "episode:path:home-movies:s01e01"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := episodeID(testCase.seriesID, testCase.season, testCase.episode)
			if got != testCase.want {
				t.Errorf("episodeID = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestAliasesFor(t *testing.T) {
	cases := []struct {
		name        string
		library     string
		kind        string
		providers   map[string]string
		folderKey   string
		canonicalID string
		want        []aliasRow
	}{
		{
			name:        "provider ids and folder key roll onto the canonical",
			library:     "movies",
			kind:        scopeMovie,
			providers:   map[string]string{"tmdb": "603", "imdb": "tt0133093"},
			folderKey:   "the-matrix",
			canonicalID: "movie:tmdb:603",
			want: []aliasRow{
				{Library: "movies", Alias: "movie:tmdb:603", Item: "movie:tmdb:603", Source: aliasSourceProvider},
				{Library: "movies", Alias: "movie:imdb:tt0133093", Item: "movie:tmdb:603", Source: aliasSourceProvider},
				{Library: "movies", Alias: "movie:path:the-matrix", Item: "movie:tmdb:603", Source: aliasSourceFolder},
			},
		},
		{
			name:        "the folder is the only name when no provider is present",
			library:     "movies",
			kind:        scopeMovie,
			providers:   nil,
			folderKey:   "unknown-2001",
			canonicalID: "movie:path:unknown-2001",
			want: []aliasRow{
				{Library: "movies", Alias: "movie:path:unknown-2001", Item: "movie:path:unknown-2001", Source: aliasSourceFolder},
			},
		},
		{
			name:        "the canonical is added when neither provider nor folder names it",
			library:     "films",
			kind:        scopeMovie,
			providers:   nil,
			folderKey:   "",
			canonicalID: "movie:path:orphan",
			want: []aliasRow{
				{Library: "films", Alias: "movie:path:orphan", Item: "movie:path:orphan", Source: aliasSourceProvider},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := aliasesFor(testCase.library, testCase.kind, testCase.providers, testCase.folderKey, testCase.canonicalID)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("aliasesFor = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

func TestSortKey(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{title: "The Matrix", want: "Matrix"},
		{title: "the matrix", want: "matrix"},
		{title: "A Bug's Life", want: "Bug's Life"},
		{title: "An American Tail", want: "American Tail"},
		{title: "Matrix", want: "Matrix"},
		{title: "Theodore", want: "Theodore"},
		{title: "A", want: "A"},
	}
	for _, testCase := range cases {
		t.Run(testCase.title, func(t *testing.T) {
			got := sortKey(testCase.title)
			if got != testCase.want {
				t.Errorf("sortKey(%q) = %q, want %q", testCase.title, got, testCase.want)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	cases := []struct {
		name  string
		title string
		year  int
		want  string
	}{
		{name: "title and year", title: "The Matrix", year: 1999, want: "the-matrix-1999"},
		{name: "accents fold to ascii", title: "Amélie", year: 2001, want: "amelie-2001"},
		{name: "eszett folds to ss", title: "Straße", year: 0, want: "strasse"},
		{name: "missing year drops the suffix", title: "The Matrix", year: 0, want: "the-matrix"},
		{name: "punctuation collapses to one hyphen", title: "Wall-E: The Movie!", year: 2008, want: "wall-e-the-movie-2008"},
		{name: "leading and trailing punctuation is trimmed", title: "  Up  ", year: 2009, want: "up-2009"},
		{name: "all punctuation with a year is just the year", title: "!!!", year: 2020, want: "2020"},
		{name: "all punctuation without a year is empty", title: "***", year: 0, want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := slug(testCase.title, testCase.year)
			if got != testCase.want {
				t.Errorf("slug(%q, %d) = %q, want %q", testCase.title, testCase.year, got, testCase.want)
			}
		})
	}
}
