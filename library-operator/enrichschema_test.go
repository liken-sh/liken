package main

import (
	"testing"
	"time"
)

// walkWithAttempts seeds one title, its file, its aliases, and one attempt
// per concern, so a prune and a gap query both read the shape a real walk
// leaves.
func walkWithAttempts(library, id, path, folderKey string, at time.Time) *walkResult {
	walk := walkOfOneTitle(library, id, path, folderKey)
	walk.attempts = []attemptRow{
		{Library: library, Item: id, Concern: concernIdentity, At: at.Unix(), Result: attemptCandidates},
		{Library: library, Item: path + "/movie.mkv", Concern: concernProbe, At: at.Unix(), Result: attemptFound},
	}
	return walk
}

func TestAPrunedFolderTakesItsAttemptsAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}

	first := int64(1000)
	for _, walk := range []*walkResult{
		walkWithAttempts("house/movies", "movie:path:one-2001", "One (2001)", "movie:path:one-2001", ledgerTime),
		walkWithAttempts("house/movies", "movie:path:two-2002", "Two (2002)", "movie:path:two-2002", ledgerTime),
	} {
		if err := flushWalk(ctx, catalog, walk, first); err != nil {
			t.Fatal(err)
		}
	}
	if got := agent.rowCount(t, "attempts"); got != 4 {
		t.Fatalf("attempts = %d, want two per title", got)
	}

	second := int64(2000)
	kept := walkWithAttempts("house/movies", "movie:path:one-2001", "One (2001)", "movie:path:one-2001", ledgerTime)
	if err := flushWalk(ctx, catalog, kept, second); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, "house/movies", second); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowCount(t, "attempts"); got != 2 {
		t.Errorf("attempts = %d, want the surviving title's two", got)
	}
}

func TestARescanTakesOnlyItsOwnFoldersAttemptsAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}

	first := int64(1000)
	for _, walk := range []*walkResult{
		walkWithAttempts("house/movies", "movie:path:one-2001", "One (2001)", "movie:path:one-2001", ledgerTime),
		walkWithAttempts("house/movies", "movie:path:two-2002", "Two (2002)", "movie:path:two-2002", ledgerTime),
	} {
		if err := flushWalk(ctx, catalog, walk, first); err != nil {
			t.Fatal(err)
		}
	}

	// The folder left the volume, so the rescan marks nothing under it.
	second := int64(2000)
	if err := flushWalk(ctx, catalog, &walkResult{}, second); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.markSeen(ctx, []string{seenItem + "keep"}, second); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneScope(ctx, catalog, "house/movies", "Two (2002)", second); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowCount(t, "attempts"); got != 2 {
		t.Errorf("attempts = %d, want the untouched folder's two", got)
	}
	if got := agent.rowCount(t, "movies"); got != 1 {
		t.Errorf("movies = %d, want the untouched folder's title", got)
	}
}

func TestALibrarySweepTakesItsAttemptsAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()

	for _, walk := range []*walkResult{
		walkWithAttempts("house/movies", "movie:path:one-2001", "One (2001)", "movie:path:one-2001", ledgerTime),
		walkWithAttempts("studio/films", "movie:path:nine", "Nine (2009)", "movie:path:nine", ledgerTime),
	} {
		if err := upsertWalk(ctx, catalog, walk); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := catalog.SweepLibrary(ctx, "house/movies"); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowCount(t, "attempts"); got != 2 {
		t.Errorf("attempts = %d, want the other library's two", got)
	}
}

func TestEveryGapQueryRunsAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()

	seed := &walkResult{
		movies: []movieRow{
			{Id: "movie:path:one-2001", Library: "house/movies", Kind: libraryKindMovies, Path: "One (2001)", Title: "One"},
			{Id: "movie:tmdb:2", Library: "house/movies", Kind: libraryKindMovies, Path: "Two (2002)", Title: "Two"},
		},
		series: []seriesRow{
			{Id: "series:path:show", Library: "house/movies", Kind: libraryKindSeries, Path: "Show", Title: "Show"},
		},
		files: []fileRow{
			{Path: "One (2001)/one.mkv", Library: "house/movies", Present: true, Type: fileTypeVideo, Items: []string{"movie:path:one-2001"}},
			{Path: "Two (2002)/two.mkv", Library: "house/movies", Present: true, Type: fileTypeVideo, DurationMs: 6540000, Items: []string{"movie:tmdb:2"}},
		},
	}
	if err := upsertWalk(ctx, catalog, seed); err != nil {
		t.Fatal(err)
	}

	gaps, err := catalog.gapCounts(ctx, "house/movies", ledgerTime)
	if err != nil {
		t.Fatal(err)
	}
	if gaps[concernProbe] != 1 {
		t.Errorf("probe gap = %d, want the one file with no duration", gaps[concernProbe])
	}
	if gaps[concernIdentity] != 2 {
		t.Errorf("identity gap = %d, want the movie and the series with a path id", gaps[concernIdentity])
	}
}

func TestAnAttemptClosesItsOwnGapAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()

	seed := &walkResult{
		movies: []movieRow{{Id: "movie:path:one-2001", Library: "house/movies", Kind: libraryKindMovies, Path: "One (2001)", Title: "One"}},
		files:  []fileRow{{Path: "One (2001)/one.mkv", Library: "house/movies", Present: true, Type: fileTypeVideo, Items: []string{"movie:path:one-2001"}}},
		attempts: []attemptRow{
			{Library: "house/movies", Item: "movie:path:one-2001", Concern: concernIdentity, At: ledgerTime.Unix(), Result: attemptCandidates},
			{Library: "house/movies", Item: "One (2001)/one.mkv", Concern: concernProbe, At: ledgerTime.Unix(), Result: attemptFound},
		},
	}
	if err := upsertWalk(ctx, catalog, seed); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		now  time.Time
		want int
	}{
		{name: "inside the retry window", now: ledgerTime.Add(time.Hour), want: 0},
		{name: "past the retry window", now: ledgerTime.Add(2 * defaultRetryInterval), want: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			gaps, err := catalog.gapCounts(ctx, "house/movies", test.now)
			if err != nil {
				t.Fatal(err)
			}
			if gaps[concernIdentity] != test.want {
				t.Errorf("identity gap = %d, want %d", gaps[concernIdentity], test.want)
			}
			if gaps[concernProbe] != test.want {
				t.Errorf("probe gap = %d, want %d", gaps[concernProbe], test.want)
			}
		})
	}
}

func TestTheWaitingAndUnresolvedCountsReadTheAttemptsTable(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()

	seed := &walkResult{attempts: []attemptRow{
		{Library: "house/movies", Item: "movie:path:one", Concern: concernIdentity, At: ledgerTime.Unix(), Result: attemptCandidates},
		{Library: "house/movies", Item: "series:path:show", Concern: concernIdentity, At: ledgerTime.Unix(), Result: attemptNothing},
		{Library: "house/movies", Item: "movie:tmdb:2", Concern: concernIdentity, At: ledgerTime.Unix(), Result: attemptCandidates},
	}}
	if err := upsertWalk(ctx, catalog, seed); err != nil {
		t.Fatal(err)
	}

	waiting, unresolved, err := catalog.identityCounts(ctx, "house/movies")
	if err != nil {
		t.Fatal(err)
	}
	if waiting != 1 {
		t.Errorf("waiting = %d, want the one unidentified title with candidates", waiting)
	}
	if unresolved != 1 {
		t.Errorf("unresolved = %d, want the one title no provider named", unresolved)
	}
}
