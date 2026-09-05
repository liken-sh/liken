package main

import (
	"path/filepath"
	"testing"
	"time"
)

// The ledger a folder carries in these tests, in the shape the identity
// fact writes when no rung parted two results.
const candidatesLedger = `items:
    - path: .
      candidates:
        - id: {tmdb: 11}
          title: Star Wars
          year: 1977
          receipt: {title: match, year: match}
attempts:
    - path: .
      at: 2026-09-02T14:00:00Z
      result: candidates
`

func TestAFolderWithAnIdentityLedgerYieldsAnAttemptsRow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"), candidatesLedger)

	result := walkMovies(root, "house/movies", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want one", result.attempts)
	}
	got := result.attempts[0]
	if got.Item != "movie:path:star-wars-1977" || got.Fact != factIdentity || got.Result != attemptCandidates {
		t.Errorf("attempt = %+v, want the item's own id under the identity fact", got)
	}
	if got.Library != "house/movies" || got.At != ledgerTime.Unix() {
		t.Errorf("attempt = %+v, want the library and the time the ledger records", got)
	}
}

func TestAProbeAttemptKeysOnTheFilePath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "probe.yaml"),
		"attempts:\n    - path: Star Wars (1977).mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n")

	result := walkMovies(root, "house/movies", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want one", result.attempts)
	}
	want := filepath.Join("Star Wars (1977)", "Star Wars (1977).mkv")
	if got := result.attempts[0]; got.Item != want || got.Fact != factProbe {
		t.Errorf("attempt = %+v, want the file path under the probe fact", got)
	}
}

func TestTheLikenDirectoryIsReadAsASidecarAndNeverAsATitle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"), candidatesLedger)

	result := walkMovies(root, "house/movies", nil)

	if result.titles != 1 {
		t.Errorf("titles = %d, want the one title folder", result.titles)
	}
	for _, movie := range result.movies {
		if movie.Path == filepath.Join("Star Wars (1977)", likenDirectory) {
			t.Errorf("the walk read %s as a title", movie.Path)
		}
	}
	if len(result.attempts) != 1 {
		t.Errorf("attempts = %+v, want the sidecar read", result.attempts)
	}
}

func TestASeasonFolderLedgerKeysOnTheEpisodeItem(t *testing.T) {
	root := t.TempDir()
	season := filepath.Join(root, "Twin Peaks (1990)", "Season 01")
	writeFile(t, filepath.Join(season, "Twin Peaks - S01E01.mkv"), "video")
	writeFile(t, filepath.Join(season, likenDirectory, "probe.yaml"),
		"attempts:\n    - path: Twin Peaks - S01E01.mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n")
	writeFile(t, filepath.Join(season, likenDirectory, "identity.yaml"),
		"attempts:\n    - path: Twin Peaks - S01E01.mkv\n      at: 2026-09-02T14:00:00Z\n      result: nothing\n")

	result := walkSeries(root, "house/series", nil)

	items := map[string]string{}
	for _, attempt := range result.attempts {
		items[attempt.Fact] = attempt.Item
	}
	wantFile := filepath.Join("Twin Peaks (1990)", "Season 01", "Twin Peaks - S01E01.mkv")
	if items[factProbe] != wantFile {
		t.Errorf("the probe attempt names %q, want the file path", items[factProbe])
	}
	if items[factIdentity] != "episode:path:twin-peaks-1990:s01e01" {
		t.Errorf("the identity attempt names %q, want the episode item", items[factIdentity])
	}
}

func TestASeriesFolderLedgerNamesTheSeries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Twin Peaks (1990)")
	writeFile(t, filepath.Join(dir, "Season 01", "Twin Peaks - S01E01.mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"), candidatesLedger)

	result := walkSeries(root, "house/series", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want one", result.attempts)
	}
	if got := result.attempts[0]; got.Item != "series:path:twin-peaks-1990" {
		t.Errorf("attempt = %+v, want the series item", got)
	}
}

func TestALedgerTheScannerCannotReadMarksThePassIncomplete(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"), "items: [oh: {: no\n")

	result := walkMovies(root, "house/movies", nil)

	if !result.readError {
		t.Error("the walk read as complete, want the incomplete mark")
	}
}

func TestAnAttemptWithNoResolvableItemIsLeftOut(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"),
		"attempts:\n    - path: a-file-that-left.mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n"+
			"    - path: .\n      at: 2026-09-02T14:00:00Z\n      result: \n")

	result := walkMovies(root, "house/movies", nil)

	if len(result.attempts) != 0 {
		t.Errorf("attempts = %+v, want none", result.attempts)
	}
}

func TestAttemptKeysSplitBackIntoTheirColumns(t *testing.T) {
	keys := attemptKeys([]string{"movie:path:x" + linkKeySeparator + factIdentity, "no-separator"})

	if keys[0] != (attemptKey{Item: "movie:path:x", Fact: factIdentity}) {
		t.Errorf("key = %+v, want the item and the fact", keys[0])
	}
	if keys[1] != (attemptKey{Item: "no-separator"}) {
		t.Errorf("key = %+v, want the item alone", keys[1])
	}
}

// The probe records its attempt in the folder that holds the file it opened,
// so an extras folder under a series holds a ledger of its own.
func TestAnExtrasFolderLedgerKeysOnTheFilePath(t *testing.T) {
	root := t.TempDir()
	extras := filepath.Join(root, "Twin Peaks (1990)", "Extras")
	writeFile(t, filepath.Join(extras, "Deleted Scene.mkv"), "video")
	writeFile(t, filepath.Join(extras, likenDirectory, "probe.yaml"),
		"attempts:\n    - path: Deleted Scene.mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n")

	result := walkSeries(root, "house/series", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want the extras folder's own", result.attempts)
	}
	want := filepath.Join("Twin Peaks (1990)", "Extras", "Deleted Scene.mkv")
	if got := result.attempts[0]; got.Item != want || got.Fact != factProbe {
		t.Errorf("attempt = %+v, want the file path under the probe fact", got)
	}
}

// The next walk lifts the attempt into the catalog, and the gap query the
// container works from no longer names the file, so the probe opens it once.
func TestAnExtrasFileTheProbeOpenedIsNoLongerAGap(t *testing.T) {
	root := t.TempDir()
	extras := filepath.Join(root, "Twin Peaks (1990)", "Extras")
	writeFile(t, filepath.Join(extras, "Deleted Scene.mkv"), "video")
	writeFile(t, filepath.Join(extras, likenDirectory, "probe.yaml"),
		"attempts:\n    - path: Deleted Scene.mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n")
	catalog, _ := newSQLiteCatalog(t)
	if err := upsertWalk(t.Context(), catalog, walkSeries(root, "house/series", nil)); err != nil {
		t.Fatal(err)
	}

	gaps, err := catalog.queryStrings(t.Context(), gapQueries[factProbe],
		gapParams(factProbe, "house/series", ledgerTime, time.Time{}))

	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %v, want none for a file the probe already opened", gaps)
	}
}

// The cases every gap query answers the same way: one dated fact and one
// error on either side of its own window. Each case is the last attempt at
// one item, its age, and whether the item is still a gap.
type attemptWindowCase struct {
	name    string
	result  string
	age     time.Duration
	wantGap int
}

var attemptWindowCases = []attemptWindowCase{
	{name: "an error an hour old", result: attemptError, age: time.Hour, wantGap: 0},
	{name: "an error two days old", result: attemptError, age: 48 * time.Hour, wantGap: 1},
	{name: "a miss ten days old", result: attemptNothing, age: 10 * 24 * time.Hour, wantGap: 0},
	{name: "a miss forty days old", result: attemptNothing, age: 40 * 24 * time.Hour, wantGap: 1},
}

// The probe and the identity gaps hold an item again only after that item's
// last attempt has passed the window its own kind carries.
func TestEveryAttemptKindGatesTheProbeAndIdentityGapsAgainstTheRealSchema(t *testing.T) {
	for _, test := range attemptWindowCases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			now := time.Now().UTC()
			seed := &walkResult{
				movies: []movieRow{{
					Id: "movie:path:one-2001", Library: "house/movies",
					Kind: libraryKindMovies, Path: "One (2001)", Title: "One",
				}},
				files: []fileRow{{
					Path: "One (2001)/one.mkv", Library: "house/movies", Present: true,
					Type: fileTypeVideo, Items: []string{"movie:path:one-2001"},
				}},
				attempts: []attemptRow{
					{
						Library: "house/movies", Item: "movie:path:one-2001", Fact: factIdentity,
						At: now.Add(-test.age).Unix(), Result: test.result,
					},
					{
						Library: "house/movies", Item: "One (2001)/one.mkv", Fact: factProbe,
						At: now.Add(-test.age).Unix(), Result: test.result,
					},
				},
			}
			if err := upsertWalk(t.Context(), catalog, seed); err != nil {
				t.Fatal(err)
			}

			gaps, err := catalog.gapCounts(t.Context(), "house/movies", now)
			if err != nil {
				t.Fatal(err)
			}
			if gaps[factIdentity] != test.wantGap {
				t.Errorf("identity gap = %d, want %d", gaps[factIdentity], test.wantGap)
			}
			if gaps[factProbe] != test.wantGap {
				t.Errorf("probe gap = %d, want %d", gaps[factProbe], test.wantGap)
			}
		})
	}
}

// The ledger an nfo fact writes, with the provider that answered and the hash
// of the group it left.
const overviewLedger = `items:
    - path: .
      provider: tmdb
      wrote: 3b1f
      written: 2026-09-02T14:00:00Z
attempts:
    - path: .
      at: 2026-09-02T14:00:00Z
      result: found
      provider: tmdb
`

func TestAnNFOLedgerYieldsAnAttemptsRowWithItsProvider(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, likenLedgerName(factOverview)), overviewLedger)

	result := walkMovies(root, "house/movies", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want one", result.attempts)
	}
	got := result.attempts[0]
	if got.Fact != factOverview || got.Item != "movie:path:star-wars-1977" {
		t.Errorf("attempt = %+v, want the title's own id under the overview fact", got)
	}
	if got.Provider != "tmdb" || got.Result != attemptFound {
		t.Errorf("attempt = %+v, want the provider that answered", got)
	}
}

// A set fact records every provider that answered, and the row joins them.
func TestALedgerOfSeveralProvidersJoinsThemInTheRow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, likenLedgerName(factCredits)),
		"attempts:\n    - path: .\n      at: 2026-09-02T14:00:00Z\n      result: found\n      provider: [tmdb, omdb]\n")

	result := walkMovies(root, "house/movies", nil)

	if len(result.attempts) != 1 || result.attempts[0].Provider != "tmdb,omdb" {
		t.Fatalf("attempts = %+v, want the two blocks joined", result.attempts)
	}
}

// The oldest attempt of each fact, which is what says whether a
// Library's refresh has titles left to ask about. A fact with no attempt
// has no entry, and another library's attempts are not in the answer.
func TestTheOldestAttemptPerFactAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	first := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	later := first.Add(time.Hour)
	if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{
		{Library: "house/movies", Item: "movie:tmdb:1", Fact: factCredits,
			At: later.Unix(), Result: attemptFound},
		{Library: "house/movies", Item: "movie:tmdb:2", Fact: factCredits,
			At: first.Unix(), Result: attemptFound},
		{Library: "house/movies", Item: "movie:tmdb:1", Fact: factPoster,
			At: later.Unix(), Result: attemptFound},
		{Library: "house/series", Item: "series:tvdb:9", Fact: factCredits,
			At: first.Add(-time.Hour).Unix(), Result: attemptFound},
	}); err != nil {
		t.Fatal(err)
	}

	oldest, err := catalog.oldestAttempts(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]time.Time{factCredits: first, factPoster: later}
	if len(oldest) != len(want) {
		t.Fatalf("oldest = %v, want one entry per fact this library attempted", oldest)
	}
	for fact, at := range want {
		if !oldest[fact].Equal(at) {
			t.Errorf("oldest[%s] = %v, want %v", fact, oldest[fact], at)
		}
	}
}
