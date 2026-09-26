package main

// what these tests read: the credits fact reads title.principals and then
// name.basics once for its whole gap, turns the rows into credits as TMDb's
// credits give them, skips name.basics when every person has an entry, links
// each person to the one entry plan 66's join finds, and answers only where
// no source before it in the Library's order answered.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The credits fact the way the container runs it.
func runIMDbCredits(t *testing.T, work *enricher) {
	t.Helper()
	work.startDatasetReads(t.Context(), []string{factCredits})
	if err := work.nfoGap(t.Context(), factCredits, work.nfoAnswerLine()); err != nil {
		t.Fatal(err)
	}
}

func TestTheDatasetsWriteAMoviesPrincipalCredits(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")

	runIMDbCredits(t, work)

	cast := nfoCast([]byte(readFileString(t, nfoPath)))
	wantCast := []creditedActor{
		{Name: "Nora Vance", Role: "Captain Vance", Order: 0},
		{Name: "Ada Ferris", Role: "Keeper / Narrator", Order: 1},
	}
	if !sameCast(cast, wantCast) {
		t.Errorf("cast = %+v, want %+v", cast, wantCast)
	}
	directors, writers := nfoCrew([]byte(readFileString(t, nfoPath)))
	if len(directors) != 1 || directors[0].Name != "Ilse Marr" || len(writers) != 1 || writers[0].Name != "Tom Reeve" {
		t.Errorf("crew = %+v and %+v, want the director and the writer alone", directors, writers)
	}
	want := []string{"GET title.principals 200", "GET name.basics 200"}
	if got := server.log(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	held, _, err := readContributorFile(filepath.Join(root, contributorDirectory("nora-vance"), contributorFileName))
	if err != nil || held.IDs[contributorIMDbScheme] != "nm9000001" {
		t.Errorf("entry = %+v, %v, want Nora Vance under her IMDb id", held, err)
	}
}

// A run whose people all have entries takes their names from the entries and
// reads no name.basics.
func TestAKnownCastReadsNoNameBasics(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
	people := map[string]string{"nm9000001": "Nora Vance", "nm9000002": "Ada Ferris",
		"nm9000003": "Ilse Marr", "nm9000004": "Tom Reeve"}
	var ids []contributorAliasRow
	for person, name := range people {
		slug := contributorSlug(name, nil)
		writeContributorEntry(t, root, slug, "name: "+name+"\nids: {imdb: "+person+"}\n")
		ids = append(ids, contributorAliasRow{Library: "house/movies", Scheme: contributorIMDbScheme,
			ID: person, Path: contributorDirectory(slug)})
	}
	if _, err := catalog.UpsertContributorIDs(t.Context(), ids); err != nil {
		t.Fatal(err)
	}

	runIMDbCredits(t, work)

	if got := server.log(); !slices.Equal(got, []string{"GET title.principals 200"}) {
		t.Errorf("requests = %v, want title.principals alone", got)
	}
}

// With tmdb before imdb, the datasets answer only a title TMDb did not.
func TestTheDatasetsAreAFallbackForCredits(t *testing.T) {
	tmdb := providerAnswer{block: providerBlockTMDb, answer: factAnswer{
		Cast: []creditedActor{{Name: "Nora Vance", Role: "Captain"}, {Name: "Lena Holt", Role: "Mate"}}}}
	imdb := providerAnswer{block: providerBlockIMDb, answer: factAnswer{
		Cast: []creditedActor{{Name: "Ada Ferris", Role: "Keeper"}}}}
	cases := []struct {
		name    string
		answers []providerAnswer
		cast    []string
	}{
		{name: "TMDb holds the title", answers: []providerAnswer{tmdb, imdb},
			cast: []string{"Nora Vance", "Lena Holt"}},
		{name: "TMDb holds nothing", answers: []providerAnswer{{block: providerBlockTMDb}, imdb},
			cast: []string{"Ada Ferris"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			merged, _ := mergeAnswers(factCredits, one.answers)

			var cast []string
			for _, actor := range merged.Cast {
				cast = append(cast, actor.Name)
			}
			if !slices.Equal(cast, one.cast) {
				t.Errorf("cast = %v, want %v", cast, one.cast)
			}
		})
	}
}

// The categories that become credits, and the roles of the characters list.
func TestAPrincipalRowBecomesACreditAsTMDbsDo(t *testing.T) {
	rows := []principal{
		{ordering: 3, person: "nm3", part: creditPartDirector},
		{ordering: 1, person: "nm1", part: creditPartActor, characters: `["Self"]`},
		{ordering: 2, person: "nm2", part: creditPartActor, characters: `not a list`},
		{ordering: 4, person: "nm1", part: creditPartActor, characters: `["Again"]`},
		{ordering: 5, person: "nm9", part: creditPartWriter},
	}
	names := map[string]string{"nm1": "One", "nm2": "Two", "nm3": "Three"}

	credits := principalCredits(rows, names)

	var cast []string
	for _, actor := range credits.Cast {
		cast = append(cast, actor.Name+":"+actor.Role)
	}
	if want := []string{"One:Self", "Two:"}; !slices.Equal(cast, want) {
		t.Errorf("cast = %v, want %v", cast, want)
	}
	if len(credits.Directors) != 1 || len(credits.Writers) != 0 {
		t.Errorf("crew = %+v and %+v, want the named director alone", credits.Directors, credits.Writers)
	}
}

// A title that enters the gap after the first read is read on the pass that
// finds it, and a failed read leaves the gap to the other sources.
func TestALaterPassReadsTheCreditsTheFirstReadMissed(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	work.startDatasetReads(t.Context(), []string{factCredits})
	nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")

	if err := work.nfoGap(t.Context(), factCredits, work.nfoAnswerLine()); err != nil {
		t.Fatal(err)
	}

	if written := readFileString(t, nfoPath); !strings.Contains(written, "Nora Vance") {
		t.Errorf(".nfo = %s, want the cast", written)
	}
	if got := server.log(); len(got) != 2 {
		t.Errorf("requests = %v, want one read of each file", got)
	}
}

func TestACreditsFileIMDbWillNotServeLeavesTheGap(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
	server.statuses[datasetTitlePrincipals] = 500

	runIMDbCredits(t, work)

	ledger, err := readLikenLedger(filepath.Dir(nfoPath), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 0 {
		t.Errorf("attempts = %+v, want none", ledger.Attempts)
	}
}

// The two seeds the failure tests take: a movie with an IMDb id, and an
// episode under a series with one. Each returns the .nfo file's path.
func seedFailingMovie(t *testing.T, catalog *Catalog, root string) string {
	return seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
}

func seedFailingEpisode(t *testing.T, catalog *Catalog, root string) string {
	nfoPath, _ := seedIMDbEpisode(t, catalog, root, "tt9000003")
	return nfoPath
}

// the attempts one fact recorded beside a .nfo file.
func attemptsBeside(t *testing.T, nfoPath, fact string) []likenAttempt {
	t.Helper()
	ledger, err := readLikenLedger(filepath.Dir(nfoPath), fact)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Attempts
}

// A file IMDb will not serve on the container's first read leaves the gap
// and records no attempt.
func TestADatasetIMDbWillNotServeLeavesEachGap(t *testing.T) {
	cases := []struct {
		name   string
		fact   string
		kind   string
		broken string
		seed   func(*testing.T, *Catalog, string) string
	}{
		{name: "name.basics", fact: factCredits, kind: libraryKindMovies, broken: datasetNameBasics,
			seed: seedFailingMovie},
		{name: "title.episode", fact: factRatingIMDb, kind: libraryKindSeries, broken: datasetTitleEpisode,
			seed: seedFailingEpisode},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			work, catalog, server, root := imdbEnricher(t, one.kind)
			server.statuses[one.broken] = 500
			nfoPath := one.seed(t, catalog, root)
			work.startDatasetReads(t.Context(), []string{one.fact})

			if err := work.nfoGap(t.Context(), one.fact, work.nfoAnswerLine()); err != nil {
				t.Fatal(err)
			}

			if got := attemptsBeside(t, nfoPath, one.fact); len(got) != 0 {
				t.Errorf("attempts = %+v, want none", got)
			}
		})
	}
}

// A file IMDb will not serve on a later pass's read leaves that pass's
// titles in the gap.
func TestADatasetIMDbWillNotServeOnALaterPassLeavesTheGap(t *testing.T) {
	cases := []struct {
		fact   string
		broken string
	}{
		{fact: factCredits, broken: datasetTitlePrincipals},
		{fact: factRatingIMDb, broken: datasetTitleRatings},
	}
	for _, one := range cases {
		t.Run(one.fact, func(t *testing.T) {
			work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
			server.statuses[one.broken] = 500
			work.startDatasetReads(t.Context(), []string{one.fact})
			nfoPath := seedFailingMovie(t, catalog, root)

			if err := work.nfoGap(t.Context(), one.fact, work.nfoAnswerLine()); err != nil {
				t.Fatal(err)
			}

			if got := attemptsBeside(t, nfoPath, one.fact); len(got) != 0 {
				t.Errorf("attempts = %+v, want none", got)
			}
		})
	}
}

// A fact that waits for reads that have not ended stops with its context.
func TestAWaitForTheReadsEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rating := &datasetReads{done: make(chan struct{})}
	credits := &creditReads{done: make(chan struct{})}

	if rating.wait(ctx) == nil || credits.wait(ctx) == nil {
		t.Error("wait = nil, want the context's error")
	}
}

// The answerer asks nothing of a container that runs no credits reads, and
// leaves the line when the reads failed.
func TestTheIMDbAnswererOfCredits(t *testing.T) {
	done := make(chan struct{})
	close(done)
	cases := []struct {
		name  string
		reads *creditReads
		held  bool
		err   error
	}{
		{name: "no reads"},
		{name: "failed reads", reads: &creditReads{done: done, err: errDatasetsUnread}, err: errDatasetsUnread},
		{name: "a title the file does not hold", reads: &creditReads{done: done}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			answerer := imdbAnswerer{credits: func() *creditReads { return one.reads }}

			_, held, err := answerer.answer(t.Context(), factCredits, titleRef{ids: providerIDs{"imdb": "tt1"}})

			if held != one.held || err != one.err {
				t.Errorf("answer = %v, %v, want %v, %v", held, err, one.held, one.err)
			}
		})
	}
}

// A gap whose titles have no IMDb id sends no request.
func TestCreditsForTitlesWithNoIMDbIDReadNoFile(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	seedNFOGap(t, catalog, root, "Winter Harbour (2011)", "movie:tmdb:4242")

	runIMDbCredits(t, work)

	if got := server.log(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}
