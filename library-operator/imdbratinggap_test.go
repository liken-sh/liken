package main

// what these tests read: the rating.imdb gap opens a rating the datasets
// wrote after 30 days when a newer title.ratings exists, opens once in the
// container a rating another tool wrote, holds episodes only when the
// container's scope says so, and leaves out a file of two episodes; the
// operator schedules on that gap by the answerer of the Library.

import (
	"maps"
	"slices"
	"testing"
	"time"
)

// the gap of the house/movies library with the scope the test names.
func ratingGap(t *testing.T, catalog *Catalog, scope ratingGapScope) []string {
	t.Helper()
	params := gapParams(factRatingIMDb, "house/movies", testNow, time.Time{})
	copy(params[len(params)-2:], imdbRatingGapParams(scope))
	ids, err := catalog.queryStrings(t.Context(), gapQueries[factRatingIMDb], params)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// one movie whose .nfo file holds its IMDb rating, with an attempt of the
// age and the dataset time the test names.
func seedRatedMovie(t *testing.T, catalog *Catalog, age time.Duration, dataset time.Time) {
	t.Helper()
	id := "movie:imdb:tt9000001"
	seed := &walkResult{
		movies: []movieRow{{Id: id, Library: "house/movies", Kind: libraryKindMovies, Path: "Winter Harbour (2011)",
			Title: "Winter Harbour", Released: "2011", NFOFacts: nfoFactList([]string{factRatingIMDb})}},
		attempts: []attemptRow{{Library: "house/movies", Item: id, Fact: factRatingIMDb,
			At: testNow.Add(-age).Unix(), Result: attemptFound, Provider: providerBlockIMDb,
			DatasetModified: unixOrZero(dataset)}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

func TestARatingOpensAgainAfterThirtyDaysWhenANewerFileExists(t *testing.T) {
	newest := testNow.Add(-time.Hour)
	cases := []struct {
		name    string
		age     time.Duration
		dataset time.Time
		reopen  int64
		open    bool
	}{
		{name: "31 days old, read from an older file", age: 31 * 24 * time.Hour,
			dataset: testNow.Add(-32 * 24 * time.Hour), reopen: newest.Unix(), open: true},
		{name: "31 days old, answered by another block", age: 31 * 24 * time.Hour,
			reopen: newest.Unix(), open: true},
		{name: "31 days old, read from the newest file", age: 31 * 24 * time.Hour,
			dataset: newest, reopen: newest.Unix(), open: false},
		{name: "a day old", age: 24 * time.Hour,
			dataset: testNow.Add(-2 * 24 * time.Hour), reopen: newest.Unix(), open: false},
		{name: "31 days old, with no reopen in the scope", age: 31 * 24 * time.Hour,
			dataset: testNow.Add(-32 * 24 * time.Hour), reopen: 0, open: false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedRatedMovie(t, catalog, one.age, one.dataset)

			got := ratingGap(t, catalog, ratingGapScope{reopen: one.reopen})

			if (len(got) == 1) != one.open {
				t.Errorf("gap = %v, want open %v", got, one.open)
			}
		})
	}
}

// one movie whose .nfo file holds an IMDb rating that another tool wrote, so
// no attempt of the fact exists.
func seedNFORatedMovie(t *testing.T, catalog *Catalog) {
	t.Helper()
	seed := &walkResult{movies: []movieRow{{Id: "movie:imdb:tt9000001", Library: "house/movies",
		Kind: libraryKindMovies, Path: "Winter Harbour (2011)", Title: "Winter Harbour", Released: "2011",
		NFOFacts: nfoFactList([]string{factRatingIMDb})}}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// the gap the reporter counts, which binds no dataset time.
func reportedRatingGap(t *testing.T, catalog *Catalog) []string {
	t.Helper()
	ids, err := catalog.queryStrings(t.Context(), gapQueries[factRatingIMDb],
		gapParams(factRatingIMDb, "house/movies", testNow, time.Time{}))
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// A rating another tool wrote has no attempt, so the datasets read it once
// in a Job whose imdb provider answers the rating. The reporter does not
// count it, or a Library whose rating another block answers would start a
// Job on every pass.
func TestARatingAnotherToolWroteIsReadOnceFromTheDatasets(t *testing.T) {
	newest := testNow.Add(-time.Hour)
	cases := []struct {
		name string
		seed func(*testing.T, *Catalog)
		job  int
	}{
		{name: "no attempt", seed: seedNFORatedMovie, job: 1},
		{name: "a fresh dataset attempt", seed: func(t *testing.T, catalog *Catalog) {
			seedRatedMovie(t, catalog, 24*time.Hour, newest)
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			one.seed(t, catalog)

			if got := ratingGap(t, catalog, ratingGapScope{reopen: newest.Unix(), episodes: true}); len(got) != one.job {
				t.Errorf("Job gap = %v, want %d items", got, one.job)
			}
			if got := reportedRatingGap(t, catalog); len(got) != 0 {
				t.Errorf("reported gap = %v, want none", got)
			}
		})
	}
}

// Two episode files of one series: one episode alone, and one file that
// holds two episodes.
func seedGapEpisodes(t *testing.T, catalog *Catalog) string {
	t.Helper()
	series := "series:imdb:tt9000003"
	single := episodeID(series, 1, 1)
	seed := &walkResult{
		series: []seriesRow{{Id: series, Library: "house/movies", Kind: libraryKindSeries, Path: "Harbour Watch (2019)",
			Title: "Harbour Watch", NFOFacts: nfoFactList([]string{factRatingIMDb})}},
		episodes: []episodeRow{
			{Id: single, Library: "house/movies", Path: "Harbour Watch (2019)/S01E01.mkv",
				Series: series, Season: 1, Episode: 1},
			{Id: episodeID(series, 1, 2), Library: "house/movies", Path: "Harbour Watch (2019)/S01E02-E03.mkv",
				Series: series, Season: 1, Episode: 2},
			{Id: episodeID(series, 1, 3), Library: "house/movies", Path: "Harbour Watch (2019)/S01E02-E03.mkv",
				Series: series, Season: 1, Episode: 3},
		},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	return single
}

func TestTheGapHoldsEpisodesOnlyWhereTheScopeSaysSo(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	single := seedGapEpisodes(t, catalog)

	if got := ratingGap(t, catalog, ratingGapScope{episodes: true}); !slices.Equal(got, []string{single}) {
		t.Errorf("gap = %v, want the episode that has a file of its own", got)
	}
	if got := ratingGap(t, catalog, ratingGapScope{}); len(got) != 0 {
		t.Errorf("gap = %v, want no episode", got)
	}
}

// The reporter counts the episodes of every Library, so the operator takes
// them out where another block answers the rating.
func TestTheScheduleCountsEpisodesOnlyForAnIMDbAnswerer(t *testing.T) {
	report := &libraryReport{Gaps: map[string]int{factRatingIMDb: 5}, EpisodeGaps: map[string]int{factRatingIMDb: 5}}
	cases := []struct {
		name  string
		block string
		open  bool
	}{
		{name: "imdb answers", block: providerBlockIMDb, open: true},
		{name: "omdb answers", block: providerBlockOMDb, open: false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library, providers := ratedLibrary(one.block, false)

			if got := phaseGapOpen(library, report, providers, []string{factRatingIMDb}, time.Now()); got != one.open {
				t.Errorf("phaseGapOpen = %v, want %v", got, one.open)
			}
		})
	}
}

// a Library whose one source is a Ready provider of the block the test names.
func ratedLibrary(block string, stale bool) (*Library, providerSet) {
	provider := &MetadataProvider{Metadata: ObjectMeta{Name: block, Namespace: "house"}}
	switch block {
	case providerBlockIMDb:
		provider.Spec.IMDb = &ProviderIMDb{}
	case providerBlockOMDb:
		provider.Spec.OMDb = &ProviderOMDb{SecretRef: SecretKeyRef{Name: "omdb-key"}}
	}
	provider.Status.Conditions = []Condition{{Type: conditionReady, Status: ConditionTrue}}
	if stale {
		provider.Status.Conditions = append(provider.Status.Conditions,
			Condition{Type: conditionStale, Status: ConditionTrue})
	}
	library := &Library{Metadata: ObjectMeta{Name: "movies", Namespace: "house"},
		Spec: LibrarySpec{Sources: []string{block}}}
	return library, providerSet{libraryKey("house", block): provider}
}

// A rating older than 30 days is work for an imdb answerer, unless IMDb has
// stopped replacing the file.
func TestAnOldRatingStartsAJobUnlessTheProviderIsStale(t *testing.T) {
	report := &libraryReport{Gaps: map[string]int{factRatingIMDb: 0},
		OldestAttempts: map[string]time.Time{factRatingIMDb: time.Now().Add(-31 * 24 * time.Hour)}}
	cases := []struct {
		name  string
		block string
		stale bool
		open  bool
	}{
		{name: "imdb answers", block: providerBlockIMDb, open: true},
		{name: "imdb answers and is stale", block: providerBlockIMDb, stale: true, open: false},
		{name: "omdb answers", block: providerBlockOMDb, open: false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library, providers := ratedLibrary(one.block, one.stale)

			if got := phaseGapOpen(library, report, providers, []string{factRatingIMDb}, time.Now()); got != one.open {
				t.Errorf("phaseGapOpen = %v, want %v", got, one.open)
			}
		})
	}
}

// The operator writes the time of title.ratings into the Job only where an
// imdb provider answers the rating.
func TestTheRatingsTimeReachesTheJobOnlyForAnIMDbAnswerer(t *testing.T) {
	modified := testNow.Add(-time.Hour)
	cases := []struct {
		name  string
		block string
		want  []EnvVar
	}{
		{name: "imdb answers", block: providerBlockIMDb,
			want: []EnvVar{{Name: imdbRatingsModifiedVariable, Value: modified.Format(time.RFC3339)}}},
		{name: "omdb answers", block: providerBlockOMDb},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library, providers := ratedLibrary(one.block, false)
			providers[libraryKey("house", one.block)].Status.IMDb = &IMDbStatus{
				Datasets: []IMDbDataset{{Name: datasetTitleRatings, LastModified: modified}}}

			if got := imdbRatingEnv(library, providers); !slices.Equal(got, one.want) {
				t.Errorf("env = %+v, want %+v", got, one.want)
			}
		})
	}
}

// The container reads its scope from that variable.
func TestTheContainerReadsItsScopeFromTheEnvironment(t *testing.T) {
	modified := testNow.Add(-time.Hour)
	cases := []struct {
		name  string
		value *string
		want  ratingGapScope
	}{
		{name: "no variable", want: ratingGapScope{}},
		{name: "a time", value: new(modified.Format(time.RFC3339)),
			want: ratingGapScope{reopen: modified.Unix(), episodes: true}},
		{name: "no time yet", value: new(""),
			want: ratingGapScope{reopen: testNow.Unix(), episodes: true}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if one.value != nil {
				t.Setenv(imdbRatingsModifiedVariable, *one.value)
			}

			if got := ratingScopeFromEnvironment(testNow); got != one.want {
				t.Errorf("scope = %+v, want %+v", got, one.want)
			}
		})
	}
}

// The day a rating turns 30 days old is a cause of its own: a Job that
// started before it has not answered it, and a Job that started after it
// has.
func TestAnAgedRatingIsACauseForAJobThatFillsGaps(t *testing.T) {
	now := time.Now().UTC()
	oldest := now.Add(-31 * 24 * time.Hour)
	cases := []struct {
		name    string
		started time.Time
		phases  []string
	}{
		{name: "the last Job started before the rating aged", started: now.Add(-2 * 24 * time.Hour),
			phases: []string{nfoContainerName}},
		{name: "the last Job started after the rating aged", started: now.Add(-time.Hour)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library, providers := ratedLibrary(providerBlockIMDb, false)
			report := &libraryReport{
				Gaps:           map[string]int{factRatingIMDb: 0},
				OldestAttempts: map[string]time.Time{factRatingIMDb: oldest},
				Runs: []libraryRun{{Worker: workerEnrich, Job: "last", Started: one.started,
					Finished: one.started.Add(time.Minute)}},
			}

			var names []string
			for _, phase := range gapPhases(library, report, providers, servedPhases(library, providers), now) {
				names = append(names, phase.name)
			}
			if !slices.Equal(names, one.phases) {
				t.Errorf("phases = %v, want %v", names, one.phases)
			}
		})
	}
}

// The reporter counts the episodes of the rating gap apart, and a read the
// catalog refuses is an error.
func TestTheReporterCountsTheEpisodesOfTheRatingGap(t *testing.T) {
	cases := []struct {
		name    string
		refused int
		want    map[string]int
		failed  bool
	}{
		{name: "the catalog answers", want: map[string]int{factRatingIMDb: 1}},
		{name: "the catalog refuses the read", refused: 1, failed: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			seedGapEpisodes(t, catalog)
			agent.queriesLeft = one.refused

			got, err := catalog.episodeGapCounts(t.Context(), "house/movies", testNow)

			if (err != nil) != one.failed || !maps.Equal(got, one.want) {
				t.Errorf("counts = %v, %v, want %v", got, err, one.want)
			}
		})
	}
}

// A rating opens again only once its 30 days have passed.
func TestAReopenWaitsForTheThirtyDays(t *testing.T) {
	cases := []struct {
		name   string
		oldest map[string]time.Time
		open   bool
	}{
		{name: "no attempt yet", oldest: map[string]time.Time{}},
		{name: "ten days old", oldest: map[string]time.Time{factRatingIMDb: testNow.Add(-10 * 24 * time.Hour)}},
		{name: "31 days old", oldest: map[string]time.Time{factRatingIMDb: testNow.Add(-31 * 24 * time.Hour)},
			open: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library, providers := ratedLibrary(providerBlockIMDb, false)
			report := &libraryReport{OldestAttempts: one.oldest}

			if got := datasetRefreshHasWork(library, report, providers, factRatingIMDb, testNow); got != one.open {
				t.Errorf("work = %v, want %v", got, one.open)
			}
		})
	}
}
