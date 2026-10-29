package main

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// spec.retry: the misses of one fact before a time stop counting, and the
// titles that fact found stay closed.

// Four features with no tiles, each with one trickplay attempt: one the fact
// found, a miss and an error before the retry time, and a miss after it.
func seedRetriedTrickplay(t *testing.T, catalog *Catalog, retry time.Time) {
	t.Helper()
	video := func(path string) fileRow {
		return fileRow{Path: path, Library: trickplayLibrary, Present: true, Type: fileTypeVideo,
			Role: fileRolePrimary, DurationMs: 6540000, VideoCodec: "av1"}
	}
	attempt := func(path, result string, at time.Time) attemptRow {
		return attemptRow{Library: trickplayLibrary, Item: path, Fact: factTrickplay, At: at.Unix(), Result: result}
	}
	before := retry.Add(-time.Hour)
	seed := &walkResult{
		files: []fileRow{video("A/a.mkv"), video("B/b.mkv"), video("C/c.mkv"), video("D/d.mkv")},
		attempts: []attemptRow{
			attempt("A/a.mkv", attemptFound, before),
			attempt("B/b.mkv", attemptNothing, before),
			attempt("C/c.mkv", attemptError, before),
			attempt("D/d.mkv", attemptNothing, retry.Add(time.Minute)),
		},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// A retry reopens the miss and the error made before its time. The found
// attempt and the miss made after the retry still hold their files out of
// the gap, and a retry still to come reopens nothing.
func TestARetryReopensOnlyTheMissesBeforeItsTimeAgainstTheRealSchema(t *testing.T) {
	retry := ledgerTime
	now := retry.Add(2 * time.Hour)
	cases := []struct {
		name  string
		times reopenTimes
		want  []string
	}{
		{name: "no retry"},
		{name: "a retry of trickplay", times: reopenTimes{Retry: refreshTimes{factTrickplay: retry}},
			want: []string{"B/b.mkv", "C/c.mkv"}},
		{name: "a retry of another fact", times: reopenTimes{Retry: refreshTimes{factCredits: retry}}},
		{name: "a retry still to come", times: reopenTimes{Retry: refreshTimes{factTrickplay: now.Add(time.Hour)}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedRetriedTrickplay(t, catalog, retry)

			items, err := catalog.workItems(t.Context(), factTrickplay, trickplayLibrary, now, test.times.of(factTrickplay))
			if err != nil {
				t.Fatal(err)
			}

			var paths []string
			for _, item := range items {
				paths = append(paths, item.Path)
			}
			slices.Sort(paths)
			if !slices.Equal(paths, test.want) {
				t.Errorf("gap = %v, want %v", paths, test.want)
			}
		})
	}
}

// A title whose files and rows are there, and whose last attempt was a miss,
// is in the gap again after a retry, the way a refresh reopens it. A title
// whose last attempt found its answer is not.
func TestARetryReopensAMissWhoseRowsAreThereAgainstTheRealSchema(t *testing.T) {
	now := time.Now().UTC()
	attempted := now.Add(-time.Hour)
	cases := []struct {
		name   string
		result string
		want   []string
	}{
		{name: "a miss", result: attemptNothing, want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"}},
		{name: "a found attempt", result: attemptFound, want: []string{"movie:tmdb:1", "series:tvdb:9"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedNFOFactRows(t, catalog)
			if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{{
				Library: "house/movies", Item: "movie:tmdb:2", Fact: factOverview,
				At: attempted.Unix(), Result: test.result, Provider: "tmdb",
			}}); err != nil {
				t.Fatal(err)
			}

			ids, err := catalog.queryStrings(t.Context(), gapQueries[factOverview],
				gapParams(factOverview, "house/movies", now, reopenTime{retry: attempted.Add(time.Minute)}))
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

// The reporter's count follows a retry too, because it binds the same
// parameters the worker's list does.
func TestTheReporterCountsTheMissesARetryReopens(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedRetriedTrickplay(t, catalog, ledgerTime)

	gaps, err := catalog.gapCounts(t.Context(), trickplayLibrary, ledgerTime.Add(2*time.Hour),
		reopenTimes{Retry: refreshTimes{factTrickplay: ledgerTime}})
	if err != nil {
		t.Fatal(err)
	}
	if gaps[factTrickplay] != 2 {
		t.Errorf("trickplay gap = %d, want the miss and the error before the retry", gaps[factTrickplay])
	}
}

// The report holds the oldest attempt of each fact that found nothing, which
// is what says whether a retry has work left. A fact whose attempts all
// found their answer has no entry.
func TestTheReportHoldsTheOldestMissOfEachFact(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedRetriedTrickplay(t, catalog, ledgerTime)
	if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{{Library: trickplayLibrary,
		Item: "A/a.mkv", Fact: factAppearances, At: ledgerTime.Unix(), Result: attemptFound}}); err != nil {
		t.Fatal(err)
	}

	_, misses, err := catalog.oldestAttempts(t.Context(), trickplayLibrary)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]time.Time{factTrickplay: ledgerTime.Add(-time.Hour)}
	if len(misses) != 1 || !misses[factTrickplay].Equal(want[factTrickplay]) {
		t.Errorf("oldest misses = %v, want %v", misses, want)
	}
}

// A retry gives a fact work while its oldest miss is older than the retry and
// the retry time has come, and a phase with that work runs in a Job that
// fills gaps even with no gap counted.
func TestARetryGivesAPhaseWork(t *testing.T) {
	oldest := testNow.Add(-24 * time.Hour)
	cases := []struct {
		name  string
		retry map[string]time.Time
		want  bool
	}{
		{name: "no retry"},
		{name: "a retry after the oldest miss", retry: map[string]time.Time{factIdentity: oldest.Add(time.Hour)}, want: true},
		{name: "a retry before the oldest miss", retry: map[string]time.Time{factIdentity: oldest.Add(-time.Hour)}},
		{name: "a retry still to come", retry: map[string]time.Time{factIdentity: time.Now().Add(time.Hour)}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Retry = one.retry
			report := &libraryReport{
				OldestAttempts: map[string]time.Time{factIdentity: oldest.Add(-48 * time.Hour)},
				OldestMisses:   map[string]time.Time{factIdentity: oldest},
			}

			if got := phaseGapOpen(library, report, providerSet{}, []string{factIdentity}, time.Now()); got != one.want {
				t.Errorf("phaseGapOpen = %v, want %v", got, one.want)
			}
		})
	}
}

// A retry of a fact is the cause of the Job that fills its gap, and the line
// names the field and the fact.
func TestARetryOfAFactIsTheCauseOfItsJob(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	cluster.libraries["movies"] = library
	library.Spec.Scan.Schedule = "0 0 * * *"
	library.Spec.Retry = map[string]time.Time{factIdentity: testNow.Add(-time.Minute)}
	report := &libraryReport{Runs: walkedRuns(testNow.Add(-2 * time.Minute)),
		OldestMisses: map[string]time.Time{factIdentity: testNow.Add(-24 * time.Hour)}}
	operator, logged := loggingOperator(t, cluster)

	if err := operator.runLibrary(t.Context(), library, report, nil, providers, testNow); err != nil {
		t.Fatal(err)
	}

	wantOneLine(t, logged, "a job that fills gaps", "because spec.retry asked again for identity")
}

// The containers and the reporter read spec.retry from the same value as
// spec.refresh, and the walk travels in neither.
func TestTheTimesValueCarriesTheRetryTimes(t *testing.T) {
	library := studioMovies()
	library.Spec.Refresh = map[string]time.Time{factCredits: testNow, refreshWalk: testNow}
	library.Spec.Retry = map[string]time.Time{factTrickplay: testNow}

	got := parseReopenTimes(refreshValue(library))

	if !got.Refresh[factCredits].Equal(testNow) || !got.Retry[factTrickplay].Equal(testNow) {
		t.Errorf("the value read back as %+v, want the refresh and the retry", got)
	}
	if _, held := got.Refresh[refreshWalk]; held {
		t.Errorf("the value read back as %+v, want no walk", got)
	}
}

// The API server's JSON for spec.retry lands in the field the operator reads.
func TestTheOperatorReadsARetryTheSchemaAdmits(t *testing.T) {
	spec := LibrarySpec{}
	if err := json.Unmarshal([]byte(`{"retry":{"trickplay":"2026-10-29T12:00:00Z"}}`), &spec); err != nil {
		t.Fatal(err)
	}

	want := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	if at := spec.Retry[factTrickplay]; !at.Equal(want) {
		t.Errorf("retry[%s] = %v, want %v", factTrickplay, at, want)
	}
}
