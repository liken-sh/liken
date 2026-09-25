package main

// what these tests read: the rating and credits reads leave a gap for the
// next run when a file, the volume, or the catalog refuses them, and log the
// refusal once.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// An episode .nfo file the container cannot read records an error.
func TestAnEpisodeNFOThatWillNotReadIsAnError(t *testing.T) {
	work, catalog, _, root := imdbEnricher(t, libraryKindSeries)
	nfoPath, _ := seedIMDbEpisode(t, catalog, root, "tt9000003")
	if err := os.Remove(nfoPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nfoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	runIMDbRating(t, work)

	ledger, err := readLikenLedger(filepath.Dir(nfoPath), factRatingIMDb)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want one error", ledger.Attempts)
	}
}

// An episode the container leaves records nothing: one outside the Job's
// folder, and one in a container that runs no dataset reads.
func TestAnEpisodeTheRunLeavesRecordsNothing(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
		facts  []string
	}{
		{name: "outside the Job's folder", scopes: []string{"Another Series (2020)"}, facts: []string{factRatingIMDb}},
		{name: "no dataset reads"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			work, catalog, _, root := imdbEnricher(t, libraryKindSeries)
			nfoPath, id := seedIMDbEpisode(t, catalog, root, "tt9000003")
			work.scopes = one.scopes
			work.startDatasetReads(t.Context(), one.facts)

			if got := work.fillEpisodeRating(t.Context(), id); got != "" {
				t.Errorf("result = %q, want none", got)
			}
			if names := namesIn(t, filepath.Dir(nfoPath)); slices.Contains(names, likenDirectory) {
				t.Errorf("folder = %v, want no ledger", names)
			}
		})
	}
}

// The answerer answers only the rating, and only once a container has reads.
func TestTheIMDbAnswererAnswersOnlyTheRatingItRead(t *testing.T) {
	done := make(chan struct{})
	close(done)
	read := &datasetReads{done: done, ratings: map[string]titleRating{"tt9000001": {Value: 7.9}}}
	cases := []struct {
		name  string
		fact  string
		reads *datasetReads
		held  bool
	}{
		{name: "the rating", fact: factRatingIMDb, reads: read, held: true},
		{name: "another fact", fact: factOverview, reads: read},
		{name: "no reads", fact: factRatingIMDb},
		{name: "a title the file does not hold", fact: factRatingIMDb,
			reads: &datasetReads{done: done, ratings: map[string]titleRating{}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			answerer := imdbAnswerer{reads: func() *datasetReads { return one.reads }}

			_, held, err := answerer.answer(t.Context(), one.fact, titleRef{ids: providerIDs{"imdb": "tt9000001"}})

			if held != one.held || err != nil {
				t.Errorf("answer = %v, %v, want %v", held, err, one.held)
			}
		})
	}
}

// An attempt records the dataset time only once the reads have ended.
func TestAnAttemptBeforeTheReadsEndRecordsNoTime(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)
	work.datasets = &datasetReads{done: make(chan struct{}), modified: datasetsModified}

	if got := work.datasetTimeOf(factRatingIMDb, nil); !got.IsZero() {
		t.Errorf("time = %v, want none", got)
	}
}

// A ledger the container cannot read is an error for the episode, and the
// log names the ledger's own error.
func TestAnEpisodeLedgerThatWillNotReadIsAnError(t *testing.T) {
	work, catalog, _, root := imdbEnricher(t, libraryKindSeries)
	nfoPath, id := seedIMDbEpisode(t, catalog, root, "tt9000003")
	writeFile(t, filepath.Join(filepath.Dir(nfoPath), likenDirectory, likenLedgerName(factRatingIMDb)), "items: [")
	work.startDatasetReads(t.Context(), []string{factRatingIMDb})

	if got := work.fillEpisodeRating(t.Context(), id); got != attemptError {
		t.Errorf("result = %q, want an error", got)
	}
	if log := work.log.(*bytes.Buffer).String(); !strings.Contains(log, "could not record the rating.imdb attempt") {
		t.Errorf("log = %q, want the ledger's error", log)
	}
}

// A title that enters the gap after the container's first read, as one the
// identity phase names during the Job does, is read on the pass that finds
// it, and the first read is not repeated for the titles it covered.
func TestALaterPassReadsTheTitlesTheFirstReadMissed(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	work.startDatasetReads(t.Context(), []string{factRatingIMDb})
	nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")

	if err := work.nfoGap(t.Context(), factRatingIMDb, work.nfoAnswerLine()); err != nil {
		t.Fatal(err)
	}

	if written := readFileString(t, nfoPath); !strings.Contains(written, "<value>7.9</value>") {
		t.Errorf(".nfo = %s, want the rating", written)
	}
	if got := server.log(); !slices.Equal(got, []string{"GET title.ratings 200"}) {
		t.Errorf("requests = %v, want one read, on the pass", got)
	}
}

// Each state of an episode's .nfo file, and the result its fill records.
func TestAnEpisodeFillRecordsWhatItsNFOAllowed(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, nfoPath string)
		result string
	}{
		{name: "no .nfo file, which the fill creates", result: attemptFound,
			setup: func(t *testing.T, nfoPath string) {
				if err := os.Remove(nfoPath); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "a folder the fill cannot write", result: attemptError,
			setup: func(t *testing.T, nfoPath string) {
				if os.Geteuid() == 0 {
					t.Skip("root writes into a read-only directory")
				}
				if err := os.Chmod(filepath.Dir(nfoPath), 0o555); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(filepath.Dir(nfoPath), 0o755) })
			}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			work, catalog, _, root := imdbEnricher(t, libraryKindSeries)
			nfoPath, id := seedIMDbEpisode(t, catalog, root, "tt9000003")
			one.setup(t, nfoPath)
			work.startDatasetReads(t.Context(), []string{factRatingIMDb})

			if got := work.fillEpisodeRating(t.Context(), id); got != one.result {
				t.Errorf("result = %q, want %q", got, one.result)
			}
		})
	}
}

// An episode's row write the catalog refuses is logged, and the fill still
// records what it found.
func TestARefusedEpisodeRowIsLogged(t *testing.T) {
	cases := []struct {
		name    string
		refused int
		log     string
	}{
		{name: "the body", refused: 1, log: "could not write the rating.imdb rows"},
		{name: "the attempt", refused: 2, log: "could not write the rating.imdb attempt row"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			work, catalog, agent, _, root := imdbEnricherOnAgent(t, libraryKindSeries)
			_, id := seedIMDbEpisode(t, catalog, root, "tt9000003")
			work.startDatasetReads(t.Context(), []string{factRatingIMDb})
			if err := work.datasets.wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			agent.transactionsLeft = one.refused

			if got := work.fillEpisodeRating(t.Context(), id); got != attemptFound {
				t.Errorf("result = %q, want found", got)
			}
			if log := work.log.(*bytes.Buffer).String(); !strings.Contains(log, one.log) {
				t.Errorf("log = %q, want %q", log, one.log)
			}
		})
	}
}

// A catalog that refuses the reads the dataset reads start from leaves the
// gap, and the fact records no attempt.
func TestACatalogThatRefusesTheTargetsLeavesTheGap(t *testing.T) {
	cases := []struct {
		fact    string
		refused int
	}{
		{fact: factRatingIMDb, refused: 1},
		{fact: factRatingIMDb, refused: 2},
		{fact: factRatingIMDb, refused: 3},
		{fact: factCredits, refused: 1},
		{fact: factCredits, refused: 2},
	}
	for _, one := range cases {
		t.Run(fmt.Sprintf("%s read %d", one.fact, one.refused), func(t *testing.T) {
			work, catalog, agent, server, root := imdbEnricherOnAgent(t, libraryKindMovies)
			seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
			agent.queriesLeft = one.refused

			work.startDatasetReads(t.Context(), []string{one.fact})

			if got := server.log(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

// A movie folder the fill cannot write keeps its .nfo file, and the log names
// the write's error.
func TestAMovieRatingTheFolderRefusesIsLogged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	work, catalog, _, root := imdbEnricher(t, libraryKindMovies)
	nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
	before := readFileString(t, nfoPath)
	if err := os.Chmod(filepath.Dir(nfoPath), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(nfoPath), 0o755) })

	runIMDbRating(t, work)

	if readFileString(t, nfoPath) != before {
		t.Error("the .nfo file changed, want every byte as it was")
	}
	if log := work.log.(*bytes.Buffer).String(); !strings.Contains(log, "could not write the rating.imdb of") {
		t.Errorf("log = %q, want the write's error", log)
	}
}

// A container with no IMDB_ENDPOINT reads from IMDb's own address.
func TestTheFetcherReadsFromIMDbWhereNoAddressIsSet(t *testing.T) {
	t.Setenv(imdbEndpointVariable, "")
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)

	if got := work.datasetFetcher().base; got != imdbDatasetsBase {
		t.Errorf("base = %q, want %q", got, imdbDatasetsBase)
	}
}

// An entry the credits join finds whose file will not read gives no name,
// so the read takes the name from name.basics.
func TestAnEntryThatWillNotReadGivesNoName(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
	writeContributorEntry(t, root, "nora-vance", "name: [not a name")
	ids := []contributorAliasRow{{Library: "house/movies", Scheme: contributorIMDbScheme, ID: "nm9000001",
		Path: contributorDirectory("nora-vance")}}
	if _, err := catalog.UpsertContributorIDs(t.Context(), ids); err != nil {
		t.Fatal(err)
	}

	runIMDbCredits(t, work)

	if got := server.log(); !slices.Contains(got, "GET name.basics 200") {
		t.Errorf("requests = %v, want a read of name.basics", got)
	}
}
