package main

// What these tests read: the trailers table against the shipped schema in a
// real SQLite, and the name parse and the score the fact ranks a video by.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One trailer of one title, as a provider named it.
func trailerOf(provider, key string, score int) trailerRow {
	return trailerRow{
		Library: "house/movies", Item: "movie:tmdb:1", Provider: provider, Key: key,
		Site: trailerSiteYouTube, URL: "https://www.youtube.com/watch?v=" + key,
		Name: "Official Trailer", Kind: trailerKindTrailer, Language: "en",
		Official: true, Published: "2026-08-01", Resolution: 1080,
		Score: score, Reason: "official trailer",
	}
}

// One read of the trailers table as one line per row.
func trailerLines(t *testing.T, catalog *Catalog) []string {
	t.Helper()
	lines, err := catalog.queryStrings(t.Context(),
		`SELECT provider || '|' || key || '|' || site || '|' || url || '|' || name || '|' ||`+
			` kind || '|' || language || '|' || official || '|' || published || '|' ||`+
			` resolution || '|' || score || '|' || reason FROM trailers WHERE library = ? AND item = ?`+
			` ORDER BY provider, key`,
		[]any{"house/movies", "movie:tmdb:1"})
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// Every column of a trailer reaches the table.
func TestTheCatalogHoldsTheTrailersOfATitle(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)

	written, err := catalog.UpsertTrailers(t.Context(),
		[]trailerRow{trailerOf(providerBlockTMDb, "sJ9mvBJ1aTI", 90)})
	if err != nil {
		t.Fatal(err)
	}

	if written != 1 {
		t.Errorf("the write reported %d rows, want 1", written)
	}
	want := "tmdb|sJ9mvBJ1aTI|youtube|https://www.youtube.com/watch?v=sJ9mvBJ1aTI|" +
		"Official Trailer|trailer|en|1|2026-08-01|1080|90|official trailer"
	if got := strings.Join(trailerLines(t, catalog), ","); got != want {
		t.Errorf("the table holds\n%s\nwant\n%s", got, want)
	}
}

// The provider and its own key name the row beside the title, so a second
// answer updates the row and a second provider adds one.
func TestATrailerIsKeyedByItsProviderAndKey(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)

	if _, err := catalog.UpsertTrailers(t.Context(), []trailerRow{
		trailerOf(providerBlockTMDb, "sJ9mvBJ1aTI", 90),
		trailerOf(providerBlockPeerTube, "e6b1-4c2f", 40),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertTrailers(t.Context(), []trailerRow{
		trailerOf(providerBlockTMDb, "sJ9mvBJ1aTI", 75),
	}); err != nil {
		t.Fatal(err)
	}

	scores, err := catalog.queryStrings(t.Context(),
		`SELECT provider || '=' || score FROM trailers WHERE library = ? AND item = ?`+
			` ORDER BY provider`,
		[]any{"house/movies", "movie:tmdb:1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(scores, ","); got != "peertube=40,tmdb=75" {
		t.Errorf("the table holds %s, want peertube=40,tmdb=75", got)
	}
}

// One recorded provider answer, read from testdata.
func trailerFixture(t *testing.T, name string) string {
	t.Helper()
	held, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(held)
}

// The title, the year, and the kind a trailer's own name states.
func TestWhatATrailerNameStates(t *testing.T) {
	cases := []struct {
		name string
		want trailerName
	}{
		{name: "DUNE: PART THREE (2026) - IMAX Trailer [4K Ultra HD]",
			want: trailerName{title: "DUNE: PART THREE", year: 2026, kind: trailerKindTrailer}},
		{name: "DUNE - PART TWO (2024) - Trailer #3 [4K Ultra HD]",
			want: trailerName{title: "DUNE - PART TWO", year: 2024, kind: trailerKindTrailer}},
		{name: `DON'T MOVE (2026) - "Horror" TV Spot [Original 4K Ultra HD]`,
			want: trailerName{title: "DON'T MOVE", year: 2026, kind: trailerKindSpot}},
		{name: "LOVE HURTS (2025) Trailer Song",
			want: trailerName{title: "LOVE HURTS", year: 2025, kind: trailerKindOther}},
		{name: "WONKA (2023) - Trailer #2 [4K Ultra HD]",
			want: trailerName{title: "WONKA", year: 2023, kind: trailerKindTrailer}},
		{name: "DUNE: PART THREE (2026) - Teaser Trailer [4K Ultra HD]",
			want: trailerName{title: "DUNE: PART THREE", year: 2026, kind: trailerKindTeaser}},
		{name: "THE MATRIX (1999) - Lobby Clip [4K Ultra HD]",
			want: trailerName{title: "THE MATRIX", year: 1999, kind: trailerKindClip}},
		{name: "THE MATRIX (1999) - Behind The Scenes",
			want: trailerName{title: "THE MATRIX", year: 1999, kind: trailerKindOther}},
		{name: "THE THING (1982) (2011) - Trailer",
			want: trailerName{title: "THE THING (1982)", year: 2011, kind: trailerKindTrailer}},
		{name: "An Interview With The Director",
			want: trailerName{title: "An Interview With The Director", year: 0, kind: trailerKindOther}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := parseTrailerName(test.name); got != test.want {
				t.Errorf("the name states %+v, want %+v", got, test.want)
			}
		})
	}
}

// Two spellings of one title fold to one word.
func TestHowATrailerTitleFolds(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "DON'T MOVE", want: "dontmove"},
		{name: "Don't Move", want: "dontmove"},
		{name: "DUNE: PART THREE", want: "dunepartthree"},
		{name: "Dune - Part Three", want: "dunepartthree"},
		{name: "Dune: Part Three", want: "dunepartthree"},
		{name: "Fast & Furious 6", want: "fastandfurious6"},
		{name: "Fast and Furious 6", want: "fastandfurious6"},
		{name: "WALL·E", want: "walle"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := foldTitle(test.name); got != test.want {
				t.Errorf("the title folds to %q, want %q", got, test.want)
			}
		})
	}
}

// The languages one household asked for, most preferred first.
var householdEnglish = []string{"en-US", "en"}

// What one trailer scores, and why.
func TestWhatATrailerScores(t *testing.T) {
	cases := []struct {
		name   string
		entry  trailerEntry
		match  trailerMatch
		want   int
		reason string
	}{
		{name: "a keyed official trailer in a preferred language",
			entry: trailerEntry{Provider: providerBlockTMDb, Kind: trailerKindTrailer,
				Language: "en", Official: true},
			match: trailerMatch{keyed: true},
			want:  100, reason: "keyed by the tmdb id; trailer; en"},
		{name: "a keyed unofficial teaser in another language",
			entry: trailerEntry{Provider: providerBlockTMDb, Kind: trailerKindTeaser, Language: "fr"},
			match: trailerMatch{keyed: true},
			want:  50, reason: "keyed by the tmdb id; teaser; language fr not preferred; unofficial"},
		{name: "a keyed video with no language at all",
			entry: trailerEntry{Provider: providerBlockTMDb, Kind: trailerKindOther, Official: true},
			match: trailerMatch{keyed: true},
			want:  45, reason: "keyed by the tmdb id; other; no language"},
		{name: "a search trailer whose name carries the title and the year",
			entry: trailerEntry{Provider: providerBlockPeerTube, Kind: trailerKindTrailer, Language: "en"},
			match: trailerMatch{title: true, year: true, yearKnown: true},
			want:  90, reason: "title and year match; trailer; en"},
		{name: "a search spot with no language",
			entry: trailerEntry{Provider: providerBlockPeerTube, Kind: trailerKindSpot},
			match: trailerMatch{title: true, year: true, yearKnown: true},
			want:  55, reason: "title and year match; spot; no language"},
		{name: "a search clip whose name states no year",
			entry: trailerEntry{Provider: providerBlockPeerTube, Kind: trailerKindClip, Language: "en"},
			match: trailerMatch{title: true},
			want:  20, reason: "title matches, no year known; clip; en"},
		{name: "the floor a matched video never falls below",
			entry: trailerEntry{Provider: providerBlockPeerTube, Kind: trailerKindOther, Language: "ko"},
			match: trailerMatch{title: true},
			want:  1, reason: "title matches, no year known; other; language ko not preferred"},
		{name: "a search provider states no official mark of its own",
			entry: trailerEntry{Provider: providerBlockPeerTube, Kind: trailerKindTrailer, Language: "en"},
			match: trailerMatch{title: true, year: true, yearKnown: true},
			want:  90, reason: "title and year match; trailer; en"},
		{name: "a name of another title",
			entry: trailerEntry{Provider: providerBlockPeerTube, Kind: trailerKindTrailer, Language: "en"},
			match: trailerMatch{year: true, yearKnown: true}},
		{name: "a name of another year",
			entry: trailerEntry{Provider: providerBlockPeerTube, Kind: trailerKindTrailer, Language: "en"},
			match: trailerMatch{title: true, yearKnown: true}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			score, reason := scoreTrailer(test.entry, test.match, householdEnglish)
			if score != test.want || reason != test.reason {
				t.Errorf("the trailer scores %d, %q, want %d, %q",
					score, reason, test.want, test.reason)
			}
		})
	}
}

// Which video languages a household's own list prefers. The primary subtag
// before the hyphen is what the two tags are compared on.
func TestWhichLanguagesATrailerScorePrefers(t *testing.T) {
	cases := []struct {
		name      string
		languages []string
		language  string
		want      bool
	}{
		{name: "the same tag", languages: []string{"en-US"}, language: "en-US", want: true},
		{name: "a region the video states none of", languages: []string{"en-US"},
			language: "en", want: true},
		{name: "a region the preference states none of", languages: []string{"en"},
			language: "en-US", want: true},
		{name: "another case", languages: []string{"EN"}, language: "en-us", want: true},
		{name: "the second preference", languages: []string{"ko", "en"},
			language: "en", want: true},
		{name: "another language", languages: []string{"en-US"}, language: "ko"},
		{name: "no preference at all", language: "en"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			entry := trailerEntry{Provider: providerBlockPeerTube,
				Kind: trailerKindTrailer, Language: test.language}
			score, reason := scoreTrailer(entry, trailerMatch{title: true, year: true, yearKnown: true},
				test.languages)

			preferred := score == trailerScoreTitleYear
			if preferred != test.want {
				t.Errorf("%s against %v scores %d (%q), want preferred = %v",
					test.language, test.languages, score, reason, test.want)
			}
		})
	}
}
