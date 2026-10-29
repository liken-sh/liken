package main

// markestimaterows_test.go proves the close container's step that keeps the
// estimates in the marks table: it reads its inputs from the catalog, writes
// only what changed, and runs after every phase of a library Job.

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"testing/synctest"
)

// The episodes of the series library these tests seed, each a primary video
// of 3,000 seconds.
const (
	estimateLibrary = "house/series"
	firstEpisode    = "Show/Season 01/Show - S01E01.mkv"
	secondEpisode   = "Show/Season 01/Show - S01E02.mkv"
)

// A series library of two episodes in the catalog. The first has the credits
// chapter given, and the second has no clue.
func seedEstimateSeries(t *testing.T, catalog *Catalog, chapter int64) {
	t.Helper()
	result := &walkResult{
		series: []seriesRow{{Id: "series:tvdb:1", Library: estimateLibrary, Kind: libraryKindSeries, Path: "Show"}},
		episodes: []episodeRow{
			{Id: "episode:tvdb:1:1:1", Library: estimateLibrary, Path: firstEpisode, Series: "series:tvdb:1"},
			{Id: "episode:tvdb:1:1:2", Library: estimateLibrary, Path: secondEpisode, Series: "series:tvdb:1"},
		},
		files: []fileRow{
			{Path: firstEpisode, Library: estimateLibrary, Present: true, Type: fileTypeVideo,
				Role: fileRolePrimary, DurationMs: 3000000, CreditsChapterMs: chapter, Items: []string{"episode:tvdb:1:1:1"}},
			{Path: secondEpisode, Library: estimateLibrary, Present: true, Type: fileTypeVideo,
				Role: fileRolePrimary, DurationMs: 3000000, Items: []string{"episode:tvdb:1:1:2"}},
		},
	}
	if err := upsertWalk(t.Context(), catalog, result); err != nil {
		t.Fatal(err)
	}
}

// The rows of one kind of one file, as the readers select them.
func markRowsOf(t *testing.T, catalog *Catalog, path, kind string) []string {
	t.Helper()
	rows, err := catalog.queryStrings(t.Context(),
		`SELECT ordinal || ' ' || IFNULL(start_ms, '') || ' ' || IFNULL(end_ms, '') || ' ' || source `+
			`FROM marks WHERE library = ? AND path = ? AND kind = ? ORDER BY ordinal`,
		[]any{estimateLibrary, path, kind})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// The provider marks of one file land the way the marks fact writes them:
// the walk's write of the file's whole set.
func landProviderMarks(t *testing.T, catalog *Catalog, path string, marks ...markRow) {
	t.Helper()
	for ordinal := range marks {
		marks[ordinal].Library, marks[ordinal].Path, marks[ordinal].Ordinal = estimateLibrary, path, ordinal
	}
	files := []fileRow{{Library: estimateLibrary, Path: path, Type: fileTypeVideo}}
	if _, err := catalog.UpsertMarks(t.Context(), files, marks); err != nil {
		t.Fatal(err)
	}
}

func deriveEstimates(t *testing.T, catalog *Catalog, languages ...string) {
	t.Helper()
	if err := deriveMarkEstimates(t.Context(), catalog, estimateLibrary, languages); err != nil {
		t.Fatal(err)
	}
}

// Every estimate follows its inputs: a chapter gives the first episode its
// own, its sibling takes the series' median, and provider marks that arrive
// later move both. A walk's write of the provider marks leaves the estimate
// row alone.
func TestTheEstimatesFollowTheProviderMarksThatArrive(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedEstimateSeries(t, catalog, 2900000)

	deriveEstimates(t, catalog)
	if got, want := markRowsOf(t, catalog, firstEpisode, markKindCredits), []string{"-1 2900000  estimate"}; !reflect.DeepEqual(got, want) {
		t.Errorf("first episode = %v, want %v", got, want)
	}
	if got, want := markRowsOf(t, catalog, secondEpisode, markKindCredits), []string{"-1 2900000  estimate"}; !reflect.DeepEqual(got, want) {
		t.Errorf("second episode = %v, want %v", got, want)
	}

	// TheIntroDB answers for the first episode, earlier than its chapter,
	// and for the second episode, which now has a clue of its own.
	landProviderMarks(t, catalog, firstEpisode, providerCredits(ms(2850000), ms(2990000)))
	landProviderMarks(t, catalog, secondEpisode, providerPreview(ms(2950000), nil))
	deriveEstimates(t, catalog)

	if got, want := markRowsOf(t, catalog, firstEpisode, markKindCredits), []string{"0 2850000 2990000 theintrodb"}; !reflect.DeepEqual(got, want) {
		t.Errorf("first episode = %v, want the provider's candidate alone", got)
	}
	if got, want := markRowsOf(t, catalog, secondEpisode, markKindCredits), []string{"-1 2950000  estimate"}; !reflect.DeepEqual(got, want) {
		t.Errorf("second episode = %v, want %v", got, want)
	}
}

// The db version of the agent's last write that changed a row, which moves
// with each such write.
func (a *sqliteAgent) transactionCount() int64 {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.version
}

// A derivation that changes nothing writes nothing, and a file whose inputs
// are gone loses its estimates.
func TestAnEstimateWithNoInputLeftIsDeleted(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	seedEstimateSeries(t, catalog, 2900000)
	if _, err := agent.db.Exec(`UPDATE files SET intro_chapter_end_ms = 90000 WHERE path = ?`, firstEpisode); err != nil {
		t.Fatal(err)
	}
	deriveEstimates(t, catalog)
	writes := agent.transactionCount()
	deriveEstimates(t, catalog)
	if again := agent.transactionCount(); again != writes {
		t.Errorf("transactions = %d after a derivation with nothing new, want %d", again, writes)
	}
	if _, err := agent.db.Exec(`UPDATE files SET credits_chapter_ms = 0, intro_chapter_end_ms = 0 WHERE library = ?`,
		estimateLibrary); err != nil {
		t.Fatal(err)
	}

	deriveEstimates(t, catalog)

	if got := agent.rowCount(t, "marks"); got != 0 {
		t.Errorf("marks = %d, want every estimate gone with the chapters", got)
	}
}

// A read or a write the catalog refuses ends the derivation with the
// refusal, which the close container writes into the run's failure.
func TestADerivationTheCatalogRefusesIsAnError(t *testing.T) {
	cases := []struct {
		name         string
		queries      int
		transactions int
		chapterGone  bool
	}{
		{name: "the files", queries: 1},
		{name: "the marks", queries: 2},
		{name: "the subtitle files", queries: 3},
		{name: "the write", transactions: 1},
		{name: "the delete", transactions: 1, chapterGone: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			seedEstimateSeries(t, catalog, 2900000)
			if test.chapterGone {
				deriveEstimates(t, catalog)
				if _, err := agent.db.Exec(`UPDATE files SET credits_chapter_ms = 0`); err != nil {
					t.Fatal(err)
				}
			}
			agent.queriesLeft, agent.transactionsLeft = test.queries, test.transactions

			if err := deriveMarkEstimates(t.Context(), catalog, estimateLibrary, nil); err == nil {
				t.Error("the derivation reported no error, want the refusal")
			}
		})
	}
}

// One subtitle file of the second episode, with the dialogue the probe read
// from it.
func seedSubtitle(t *testing.T, catalog *Catalog, name, language, role string, dialogue subtitleDialogue) {
	t.Helper()
	row := fileRow{Path: "Show/Season 01/" + name, Library: estimateLibrary, Present: true,
		Type: fileTypeSubtitle, Role: role, Language: language, Dialogue: dialogue.column(),
		Items: []string{"episode:tvdb:1:1:2"}}
	if err := upsertWalk(t.Context(), catalog, &walkResult{files: []fileRow{row}}); err != nil {
		t.Fatal(err)
	}
}

// The subtitle file that gives the estimate is the household's language
// first, and the full track before a forced or a hearing-impaired one.
func TestTheEstimateReadsTheSubtitleFileTheHouseholdReads(t *testing.T) {
	cases := []struct {
		name      string
		languages []string
		want      []string
	}{
		{name: "the household's language", languages: []string{"de", "en"}, want: []string{"-1 2600000  estimate"}},
		{name: "the full track before the hearing-impaired one", languages: []string{"en"},
			want: []string{"-1 2700000  estimate"}},
		{name: "a full track where no language matches", languages: []string{"fr"},
			want: []string{"-1 2600000  estimate"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedEstimateSeries(t, catalog, 0)
			seedSubtitle(t, catalog, "Show - S01E02.de.forced.srt", "de", fileRoleForced,
				subtitleDialogue{First: 1000, Last: 2500000})
			seedSubtitle(t, catalog, "Show - S01E02.de.srt", "de", fileRoleFull,
				subtitleDialogue{First: 1000, Last: 2600000})
			seedSubtitle(t, catalog, "Show - S01E02.en.sdh.srt", "en", fileRoleSDH,
				subtitleDialogue{First: 1000, Last: 2650000})
			seedSubtitle(t, catalog, "Show - S01E02.en.srt", "en", fileRoleFull,
				subtitleDialogue{First: 1000, Last: 2700000})

			deriveEstimates(t, catalog, test.languages...)

			if got := markRowsOf(t, catalog, secondEpisode, markKindCredits); !reflect.DeepEqual(got, test.want) {
				t.Errorf("second episode = %v, want %v", got, test.want)
			}
		})
	}
}

// The household's subtitle languages travel from MediaPreferences to the
// close container, which reads them to choose the subtitle file.
func TestTheSubtitleLanguagesReachTheCloseContainer(t *testing.T) {
	preferences := &MediaPreferencesList{Items: []MediaPreferences{{
		Metadata: ObjectMeta{Name: mediaPreferencesName},
		Spec:     MediaPreferencesSpec{SubtitleLanguages: []string{"de", "en"}},
	}}}
	plan := libraryJob{mode: jobModeGaps, subtitleLanguages: householdSubtitleLanguages(preferences)}
	job := buildLibraryJob(studioMovies(), providerSet{}, nil, plan, testJobImages, testJobBus, testNow)
	t.Setenv(librarySubtitleLanguagesVariable, containerEnvironment(*jobContainer(job, closeMode))[librarySubtitleLanguagesVariable])
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(libraryContainerVariable, closeMode)
	t.Setenv(libraryWorkerVariable, workerEnrich)

	run, err := newCloseRun(&bytes.Buffer{})

	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.subtitleLanguages, []string{"de", "en"}) {
		t.Errorf("languages = %v, want the household's", run.subtitleLanguages)
	}
}

// The close container derives the estimates after every phase has ended, so
// the marks and the lengths that the Job's phases wrote are its inputs.
func TestTheCloseContainerDerivesTheMarkEstimates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		seedEstimateSeries(t, catalog, 2900000)
		run, _ := closingJob(t, catalog)
		run.library, run.kind = estimateLibrary, libraryKindSeries
		done := make(chan error, 1)
		go func() { done <- run.runJob(context.Background()) }()
		confirmTheRunOf(t, catalog, estimateLibrary, workerEnrich, run.job)
		if err := <-done; err != nil {
			t.Fatalf("the job failed: %v", err)
		}

		if got := markRowsOf(t, catalog, secondEpisode, markKindCredits); len(got) != 1 {
			t.Errorf("second episode = %v, want the series' estimate", got)
		}
	})
}
