package main

// What these tests read: which files the marks gap holds, what one file's
// ask leaves in its folder's ledger and in the marks table, how a miss, a
// failure, and a spent allowance are recorded, and which blocks the line
// asks.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// One provider block that answers a marks ask with what a test names, and
// counts how often it was asked.
type scriptedMarks struct {
	block   string
	entries []markEntry
	err     error
	asked   *int
}

func (s scriptedMarks) providerBlock() string { return s.block }

func (s scriptedMarks) marks(context.Context, markFile) ([]markEntry, error) {
	if s.asked != nil {
		*s.asked++
	}
	return s.entries, s.err
}

func markLineOf(answerers ...markAnswerer) *markLine {
	return &markLine{answerers: answerers}
}

// One span of one kind with two ends, as a provider answers it.
func answeredSpan(block, kind string, start, end int64) markEntry {
	return markEntry{Kind: kind, Start: milliseconds(start), End: milliseconds(end), Source: block}
}

const marksLibrary = "house/movies"

// One identified movie on the volume and in the catalog: its folder, its
// .nfo file, its main video with the length the probe measured, and its ids.
func seedMarkedMovie(t *testing.T, catalog *Catalog, root, folder, id string, duration int64) string {
	t.Helper()
	video := filepath.Join(folder, folder+".mkv")
	writeFile(t, filepath.Join(root, video), "video")
	writeFile(t, filepath.Join(root, folder, movieNFOName),
		`<movie><title>The Matrix</title><year>1999</year><uniqueid type="tmdb">603</uniqueid></movie>`)
	seed := &walkResult{
		movies: []movieRow{{Id: id, Library: marksLibrary, Kind: libraryKindMovies, Path: folder, Title: "The Matrix"}},
		files: []fileRow{{Library: marksLibrary, Path: video, Type: fileTypeVideo, Role: fileRolePrimary,
			Present: true, DurationMs: duration, Items: []string{id}}},
		aliases: []aliasRow{
			{Alias: id, Library: marksLibrary, Item: id, Source: aliasSourceProvider},
			{Alias: "movie:imdb:tt0133093", Library: marksLibrary, Item: id, Source: aliasSourceProvider},
		},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	return video
}

// The whole run of one file: the ledger holds every span of every block in
// the line's order under the file's entry, the attempt names both blocks, and
// the marks table holds the same spans.
func TestTheMarksFactWritesTheLedgerAndTheRowsOfOneFile(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	video := seedMarkedMovie(t, catalog, root, "The Matrix (1999)", "movie:tmdb:603", 8160000)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	work.library = marksLibrary
	opening := markEntry{Kind: markKindIntro, End: milliseconds(23000), Source: providerBlockTheIntroDB}
	line := markLineOf(
		scriptedMarks{block: providerBlockTheIntroDB, entries: []markEntry{
			opening, answeredSpan(providerBlockTheIntroDB, markKindCredits, 7800000, 8100000),
		}},
		scriptedMarks{block: providerBlockIntroDB, entries: []markEntry{
			answeredSpan(providerBlockIntroDB, markKindCredits, 7790000, 8100000),
		}},
	)

	if err := work.marksGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	ledger := artLedger(t, filepath.Join(root, "The Matrix (1999)"), factMarks)
	entry := "The Matrix (1999).mkv"
	want := []markEntry{
		{Path: entry, Kind: markKindIntro, End: milliseconds(23000), Source: providerBlockTheIntroDB},
		{Path: entry, Kind: markKindCredits, Start: milliseconds(7800000), End: milliseconds(8100000), Source: providerBlockTheIntroDB},
		{Path: entry, Kind: markKindCredits, Start: milliseconds(7790000), End: milliseconds(8100000), Source: providerBlockIntroDB},
	}
	if !slices.EqualFunc(ledger.Marks, want, sameMark) {
		t.Errorf("the ledger holds %v, want %v", markText(ledger.Marks), markText(want))
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound || ledger.Attempts[0].Path != entry {
		t.Fatalf("attempts = %+v, want the one that found the spans of the file", ledger.Attempts)
	}
	if got := strings.Join(ledger.Attempts[0].Provider, ","); got != "theintrodb,introdb" {
		t.Errorf("the attempt names %q, want both blocks that answered", got)
	}
	rows := marksOfFile(t, agent, marksLibrary, video)
	wantRows := []string{"intro null-23s theintrodb", "credits 2h10m0s-2h15m0s theintrodb", "credits 2h9m50s-2h15m0s introdb"}
	if !slices.Equal(rows, wantRows) {
		t.Errorf("the table holds %v, want %v", rows, wantRows)
	}
	if !strings.Contains(log.String(), "found the marks of 1 of the 1 files") {
		t.Errorf("log = %q, want the line that counts the run", log.String())
	}
}

// The gap holds the main video of an identified work with a measured length,
// and nothing else: no unidentified work, no file the probe has not measured,
// no extra, and no file whose last attempt is inside its window.
func TestTheMarksGapHoldsTheMainVideosOfIdentifiedWorks(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		duration int64
		role     string
		attempt  *attemptRow
		want     bool
	}{
		{name: "an identified movie", id: "movie:tmdb:603", duration: 8160000, role: fileRolePrimary, want: true},
		{name: "an unidentified movie", id: "movie:path:the-matrix-1999", duration: 8160000, role: fileRolePrimary},
		{name: "a file with no length", id: "movie:tmdb:603", role: fileRolePrimary},
		{name: "an extra", id: "movie:tmdb:603", duration: 60000, role: "extra"},
		{
			name: "a file attempted this week", id: "movie:tmdb:603", duration: 8160000, role: fileRolePrimary,
			attempt: &attemptRow{Result: attemptNothing, At: time.Now().Add(-7 * 24 * time.Hour).Unix()},
		},
		{
			name: "a file attempted two months ago", id: "movie:tmdb:603", duration: 8160000, role: fileRolePrimary,
			attempt: &attemptRow{Result: attemptNothing, At: time.Now().Add(-60 * 24 * time.Hour).Unix()}, want: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			path := "The Matrix (1999)/The Matrix (1999).mkv"
			seed := &walkResult{
				movies: []movieRow{{Id: test.id, Library: marksLibrary, Kind: libraryKindMovies,
					Path: "The Matrix (1999)", Title: "The Matrix"}},
				files: []fileRow{{Library: marksLibrary, Path: path, Type: fileTypeVideo, Role: test.role,
					Present: true, DurationMs: test.duration, Items: []string{test.id}}},
			}
			if test.attempt != nil {
				attempt := *test.attempt
				attempt.Library, attempt.Item, attempt.Fact = marksLibrary, path, factMarks
				seed.attempts = []attemptRow{attempt}
			}
			if err := upsertWalk(t.Context(), catalog, seed); err != nil {
				t.Fatal(err)
			}

			paths, err := catalog.queryStrings(t.Context(), gapQueries[factMarks],
				gapParams(factMarks, marksLibrary, time.Now().UTC(), time.Time{}))

			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(paths, path); got != test.want {
				t.Errorf("the gap holds %v, want the file in it: %v", paths, test.want)
			}
		})
	}
}

// An episode names the series' ids, its two aired numbers, and its length.
// A file of two episodes names both, so the fact can refuse it.
func TestAMarkFileNamesTheWorkItsProviderKeysOn(t *testing.T) {
	cases := []struct {
		name         string
		episodes     []int
		wantEpisodes int
	}{
		{name: "one episode", episodes: []int{2}, wantEpisodes: 1},
		{name: "a file of two episodes", episodes: []int{2, 3}, wantEpisodes: 2},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			series, path := "series:tvdb:121361", "Game of Thrones (2011)/Season 01/Game of Thrones - S01E02.mkv"
			seed := &walkResult{
				series: []seriesRow{{Id: series, Library: marksLibrary, Kind: libraryKindSeries,
					Path: "Game of Thrones (2011)", Title: "Game of Thrones"}},
				aliases: []aliasRow{
					{Alias: series, Library: marksLibrary, Item: series, Source: aliasSourceProvider},
					{Alias: "series:tmdb:1399", Library: marksLibrary, Item: series, Source: aliasSourceProvider},
					{Alias: "series:imdb:tt0944947", Library: marksLibrary, Item: series, Source: aliasSourceProvider},
					{Alias: "series:path:game-of-thrones-2011", Library: marksLibrary, Item: series, Source: aliasSourceFolder},
				},
			}
			file := fileRow{Library: marksLibrary, Path: path, Type: fileTypeVideo, Role: fileRolePrimary,
				Present: true, DurationMs: 3318000}
			for _, number := range test.episodes {
				id := episodeID(series, 1, number)
				seed.episodes = append(seed.episodes, episodeRow{Id: id, Library: marksLibrary,
					Kind: libraryKindSeries, Path: path, Series: series, Season: 1, Episode: number})
				file.Items = append(file.Items, id)
			}
			seed.files = []fileRow{file}
			if err := upsertWalk(t.Context(), catalog, seed); err != nil {
				t.Fatal(err)
			}

			got, held, err := catalog.markFile(t.Context(), marksLibrary, path)

			if err != nil || !held {
				t.Fatalf("markFile = %+v, %v, %v, want the file", got, held, err)
			}
			if got.movie || got.season != 1 || got.episode != 2 || got.duration != 3318000 {
				t.Errorf("file = %+v, want season 1, episode 2, and the length", got)
			}
			if got.episodes != test.wantEpisodes {
				t.Errorf("episodes = %d, want %d", got.episodes, test.wantEpisodes)
			}
			want := providerIDs{"tvdb": "121361", "tmdb": "1399", "imdb": "tt0944947"}
			if len(got.ids) != len(want) || got.ids["tmdb"] != "1399" || got.ids["imdb"] != "tt0944947" {
				t.Errorf("ids = %v, want %v with no folder key", got.ids, want)
			}
		})
	}
}

// A file of two episodes is asked about neither, and records a miss with its
// date, so the gap does not name it on every run.
func TestAFileOfTwoEpisodesRecordsAMissWithoutAsking(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindSeries, root, nil)
	asked := 0
	line := markLineOf(scriptedMarks{block: providerBlockTheIntroDB, asked: &asked,
		entries: []markEntry{answeredSpan(providerBlockTheIntroDB, markKindIntro, 0, 60000)}})

	work.marksOne(t.Context(), line, "Show/Season 01/Show - S01E02-E03.mkv", markFile{episodes: 2})

	if asked != 0 {
		t.Errorf("the line was asked %d times, want none", asked)
	}
	ledger := artLedger(t, filepath.Join(root, "Show", "Season 01"), factMarks)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("attempts = %+v, want one miss", ledger.Attempts)
	}
}

// A miss clears the file's spans, because the providers hold none now. A
// failure keeps them, because a provider that was down says nothing about
// where the credits are. The other files of the folder keep theirs either
// way.
func TestAMissClearsTheSpansAndAFailureKeepsThem(t *testing.T) {
	cases := []struct {
		name       string
		answerer   scriptedMarks
		wantResult string
		wantSpans  int
	}{
		{name: "a miss", answerer: scriptedMarks{block: providerBlockTheIntroDB},
			wantResult: attemptNothing, wantSpans: 0},
		{name: "a failure", answerer: scriptedMarks{block: providerBlockTheIntroDB,
			err: errors.New(`theintrodb /v3/media: 500: {"error":"internal"}`)},
			wantResult: attemptError, wantSpans: 2},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			work, log := testEnricher(t, libraryKindSeries, root, nil)
			season := filepath.Join(root, "Show", "Season 01")
			writeFile(t, filepath.Join(season, likenDirectory, likenLedgerName(factMarks)), `marks:
  - {path: Show - S01E01.mkv, kind: intro, end: 50000, source: theintrodb}
  - {path: Show - S01E02.mkv, kind: intro, end: 60000, source: theintrodb}
  - {path: Show - S01E02.mkv, kind: credits, start: 2500000, source: theintrodb}
`)

			work.marksOne(t.Context(), markLineOf(test.answerer), "Show/Season 01/Show - S01E02.mkv", markFile{episodes: 1})

			ledger := artLedger(t, season, factMarks)
			held := 0
			for _, entry := range ledger.Marks {
				if entry.Path == "Show - S01E02.mkv" {
					held++
				}
			}
			if held != test.wantSpans {
				t.Errorf("the file holds %d spans, want %d", held, test.wantSpans)
			}
			if ledger.Marks[0].Path != "Show - S01E01.mkv" {
				t.Errorf("the ledger holds %v, want the other file's span kept", markText(ledger.Marks))
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != test.wantResult {
				t.Errorf("attempts = %+v, want %s", ledger.Attempts, test.wantResult)
			}
			if test.answerer.err != nil && !strings.Contains(log.String(), `{"error":"internal"}`) {
				t.Errorf("log = %q, want the provider's own words", log.String())
			}
		})
	}
}

// A provider whose allowance is spent leaves the line for the rest of the
// run. The file it failed on records an error, the other provider still
// answers, and once no provider is left the gap stops and names no attempt
// for the files it did not reach.
func TestASpentProviderLeavesTheLine(t *testing.T) {
	spent := providerStatusError{provider: providerBlockTheIntroDB, path: theintrodbMediaPath,
		status: http.StatusTooManyRequests, body: `{"error":"Usage limit exceeded"}`}
	cases := []struct {
		name      string
		answerers func(*int) []markAnswerer
		wantAsked int
		wantFiles int
	}{
		{
			name: "the only provider",
			answerers: func(asked *int) []markAnswerer {
				return []markAnswerer{scriptedMarks{block: providerBlockTheIntroDB, err: spent, asked: asked}}
			},
			wantAsked: 1, wantFiles: 1,
		},
		{
			name: "one of two providers",
			answerers: func(asked *int) []markAnswerer {
				return []markAnswerer{
					scriptedMarks{block: providerBlockTheIntroDB, err: spent, asked: asked},
					scriptedMarks{block: providerBlockIntroDB,
						entries: []markEntry{answeredSpan(providerBlockIntroDB, markKindIntro, 1000, 60000)}},
				}
			},
			wantAsked: 1, wantFiles: 2,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			seedMarkedMovie(t, catalog, root, "The Matrix (1999)", "movie:tmdb:603", 8160000)
			seedMarkedMovie(t, catalog, root, "The Matrix Reloaded (2003)", "movie:tmdb:604", 8280000)
			work, log := testEnricher(t, libraryKindMovies, root, catalog)
			work.library = marksLibrary
			asked := 0

			if err := work.marksGap(t.Context(), markLineOf(test.answerers(&asked)...)); err != nil {
				t.Fatal(err)
			}

			if asked != test.wantAsked {
				t.Errorf("the spent provider was asked %d times, want %d", asked, test.wantAsked)
			}
			files := 0
			for _, folder := range []string{"The Matrix (1999)", "The Matrix Reloaded (2003)"} {
				files += len(artLedger(t, filepath.Join(root, folder), factMarks).Attempts)
			}
			if files != test.wantFiles {
				t.Errorf("%d files hold an attempt, want %d", files, test.wantFiles)
			}
			if test.wantFiles == 1 && !strings.Contains(log.String(), "spent its allowance") {
				t.Errorf("log = %q, want the line that says why the run stopped", log.String())
			}
		})
	}
}

// The blocks a line reads out of the source order. TheIntroDB answers with or
// without its key, and IntroDB takes none.
func TestTheMarkLineTakesTheBlocksThatCanAnswer(t *testing.T) {
	cases := []struct {
		name   string
		blocks []string
		env    map[string]string
		want   []string
	}{
		{name: "theintrodb with no key", blocks: []string{providerBlockTheIntroDB},
			env: map[string]string{}, want: []string{providerBlockTheIntroDB}},
		{name: "theintrodb with its key", blocks: []string{providerBlockTheIntroDB},
			env:  map[string]string{providerTokenVariable(providerBlockTheIntroDB): "a-key"},
			want: []string{providerBlockTheIntroDB}},
		{name: "the source order is the order of the line",
			blocks: []string{providerBlockIntroDB, providerBlockTMDb, providerBlockTheIntroDB},
			env:    map[string]string{tmdbTokenVariable: "a-key"},
			want:   []string{providerBlockIntroDB, providerBlockTheIntroDB}},
		{name: "a block that serves no marks", blocks: []string{providerBlockTMDb},
			env: map[string]string{tmdbTokenVariable: "a-key"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			line := newMarkLine(test.blocks, func(name string) string { return test.env[name] }, nil)

			var blocks []string
			for _, one := range line.answerers {
				blocks = append(blocks, one.providerBlock())
			}
			if !slices.Equal(blocks, test.want) {
				t.Errorf("the line asks %v, want %v", blocks, test.want)
			}
		})
	}
}

// The key that reached the container travels on every ask of the line's
// TheIntroDB answerer.
func TestTheMarkLineCarriesTheTheIntroDBKey(t *testing.T) {
	fake := &fakeTheIntroDB{status: http.StatusOK, body: `{"tmdb_id":603,"type":"movie"}`}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	line := newMarkLine([]string{providerBlockTheIntroDB}, func(name string) string {
		return map[string]string{providerTokenVariable(providerBlockTheIntroDB): "a-key"}[name]
	}, nil)
	line.answerers[0].(theintrodbMarkAnswerer).client.base = server.URL

	if answer := line.ask(t.Context(), markFile{movie: true, ids: providerIDs{"tmdb": "603"}}); answer.failure != nil {
		t.Fatal(answer.failure)
	}
	if got := fake.requests[0].Header.Get("Authorization"); got != "Bearer a-key" {
		t.Errorf("Authorization = %q, want the key that reached the container", got)
	}
}

// A container whose line holds no answerer is a manifest to repair.
func TestAMarksContainerWithNoProviderFails(t *testing.T) {
	t.Setenv(librarySourcesVariable, providerBlockTMDb)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)

	if err := work.marksFact(t.Context()); err == nil || !strings.Contains(err.Error(), factMarks) {
		t.Errorf("err = %v, want the failure that names the fact", err)
	}
}
