package main

// What these tests read: the art vocabulary, the names the five facts write,
// and the gap queries, against the shipped schema in a real SQLite.

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

const artLibrary = "house/movies"

// One movie with a TMDb alias and no art beside it, which is the shape of
// every title art gap.
func seedArtMovie(t *testing.T, catalog *Catalog, path string, files ...fileRow) {
	t.Helper()
	seed := &walkResult{
		movies: []movieRow{{
			Id: "movie:tmdb:603", Library: artLibrary, Kind: libraryKindMovies,
			Path: path, Title: "The Signal",
		}},
		aliases: []aliasRow{
			{Alias: "movie:tmdb:603", Library: artLibrary, Item: "movie:tmdb:603", Source: aliasSourceProvider},
			{Alias: "movie:path:the-signal", Library: artLibrary, Item: "movie:tmdb:603", Source: aliasSourceFolder},
		},
		files: files,
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// One series with a TheTVDB id of its own and a TMDb alias, and one episode
// per season named, which is the shape of the season and episode gaps.
func seedArtSeries(t *testing.T, catalog *Catalog, path string, seasons []int, files ...fileRow) {
	t.Helper()
	seed := &walkResult{
		series: []seriesRow{{
			Id: "series:tvdb:81189", Library: artLibrary, Kind: libraryKindSeries,
			Path: path, Title: "Quiet Harbor",
		}},
		aliases: []aliasRow{
			{Alias: "series:tvdb:81189", Library: artLibrary, Item: "series:tvdb:81189", Source: aliasSourceProvider},
			{Alias: "series:tmdb:1396", Library: artLibrary, Item: "series:tvdb:81189", Source: aliasSourceProvider},
		},
		files: files,
	}
	for _, season := range seasons {
		seed.episodes = append(seed.episodes, episodeRow{
			Id:      episodeID("series:tvdb:81189", season, 5),
			Library: artLibrary, Kind: libraryKindSeries,
			Path: filepath.Join(path, fmt.Sprintf("Season %02d", season),
				fmt.Sprintf("Quiet Harbor - S%02dE05.mkv", season)),
			Title: "One More", Series: "series:tvdb:81189", Season: season, Episode: 5,
		})
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

func artGapsOf(t *testing.T, catalog *Catalog, fact string) []artGap {
	t.Helper()
	gaps, err := catalog.artGaps(t.Context(), artLibrary, fact, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return gaps
}

// A title with an id and no file of the name reads as one gap, named for the
// file the fact writes.
func TestTheTitleArtGapNamesTheFileTheLibraryHasNone(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedArtMovie(t, catalog, "The Signal (2014)")

	cases := []struct {
		fact string
		want string
	}{
		{fact: factPoster, want: "The Signal (2014)/poster.jpg"},
		{fact: factBackdrop, want: "The Signal (2014)/fanart.jpg"},
		{fact: factLogo, want: "The Signal (2014)/clearlogo.png"},
		{fact: factClearart, want: "The Signal (2014)/clearart.png"},
		{fact: factBanner, want: "The Signal (2014)/banner.jpg"},
		{fact: factLandscape, want: "The Signal (2014)/landscape.jpg"},
		{fact: factDiscart, want: "The Signal (2014)/disc.png"},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			gaps := artGapsOf(t, catalog, test.fact)
			if len(gaps) != 1 {
				t.Fatalf("gaps = %+v, want the one file the title has none of", gaps)
			}
			if gaps[0].key != test.want || gaps[0].tmdb != "603" {
				t.Errorf("gap = %+v, want %q from tmdb 603", gaps[0], test.want)
			}
		})
	}
}

// The file the fact would write is already a row, so the title is no gap.
func TestATitleWithTheArtFileHasNoGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedArtMovie(t, catalog, "The Signal (2014)", fileRow{
		Path: "The Signal (2014)/poster.jpg", Library: artLibrary, Present: true,
		Type: fileTypeImage, Role: fileRolePoster, Items: []string{"movie:tmdb:603"},
	})

	if gaps := artGapsOf(t, catalog, factPoster); len(gaps) != 0 {
		t.Errorf("poster gaps = %+v, want none, because the file is there", gaps)
	}
	if gaps := artGapsOf(t, catalog, factBackdrop); len(gaps) != 1 {
		t.Errorf("backdrop gaps = %+v, want the one file that is still missing", gaps)
	}
}

// A title no provider has named cannot be asked about, so it is no gap and
// the operator never runs a Job that has nothing to ask.
func TestATitleWithNoProviderIdHasNoArtGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seed := &walkResult{
		movies: []movieRow{{
			Id: "movie:path:the-signal", Library: artLibrary, Kind: libraryKindMovies,
			Path: "The Signal (2014)", Title: "The Signal",
		}},
		aliases: []aliasRow{{
			Alias: "movie:path:the-signal", Library: artLibrary,
			Item: "movie:path:the-signal", Source: aliasSourceFolder,
		}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	if gaps := artGapsOf(t, catalog, factPoster); len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none, because no provider has named the title", gaps)
	}
}

// An art gap holds a file again only after that file's last attempt has passed
// the window its own kind carries.
func TestEveryAttemptKindGatesTheArtGap(t *testing.T) {
	for _, test := range attemptWindowCases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedArtMovie(t, catalog, "The Signal (2014)")
			attempts := []attemptRow{{
				Library: artLibrary, Item: "The Signal (2014)/poster.jpg", Fact: factPoster,
				At: time.Now().UTC().Add(-test.age).Unix(), Result: test.result,
			}}
			if _, err := catalog.UpsertAttempts(t.Context(), attempts); err != nil {
				t.Fatal(err)
			}

			if gaps := artGapsOf(t, catalog, factPoster); len(gaps) != test.wantGap {
				t.Errorf("gaps = %+v, want %d", gaps, test.wantGap)
			}
		})
	}
}

// One gap per season the library holds episodes of, named the way Kodi reads
// it in the series folder, and season zero named for the specials.
func TestTheSeasonArtGapNamesOneFilePerSeason(t *testing.T) {
	cases := []struct {
		fact string
		want []string
	}{
		{fact: factSeasonPoster, want: []string{
			"Quiet Harbor (2008)/season-specials-poster.jpg",
			"Quiet Harbor (2008)/season01-poster.jpg",
			"Quiet Harbor (2008)/season02-poster.jpg",
		}},
		{fact: factSeasonBanner, want: []string{
			"Quiet Harbor (2008)/season-specials-banner.jpg",
			"Quiet Harbor (2008)/season01-banner.jpg",
			"Quiet Harbor (2008)/season02-banner.jpg",
		}},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedArtSeries(t, catalog, "Quiet Harbor (2008)", []int{0, 1, 2})

			gaps := artGapsOf(t, catalog, test.fact)
			keys := []string{}
			for _, gap := range gaps {
				keys = append(keys, gap.key)
				if gap.tmdb != "1396" {
					t.Errorf("gap = %+v, want the TMDb id the alias holds", gap)
				}
			}
			slices.Sort(keys)
			if !slices.Equal(keys, test.want) {
				t.Errorf("gaps = %v, want %v", keys, test.want)
			}
		})
	}
}

// The episode gap names the episode file, because the thumbnail is named for
// that file, and an episode that already holds an image is no gap.
func TestTheEpisodeThumbGapNamesTheEpisodeFile(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedArtSeries(t, catalog, "Quiet Harbor (2008)", []int{1})

	gaps := artGapsOf(t, catalog, factEpisodeThumb)
	want := filepath.Join("Quiet Harbor (2008)", "Season 01", "Quiet Harbor - S01E05.mkv")
	if len(gaps) != 1 || gaps[0].key != want {
		t.Fatalf("gaps = %+v, want %q", gaps, want)
	}
	if gaps[0].season != 1 || gaps[0].episode != 5 {
		t.Errorf("gap = %+v, want season 1 episode 5", gaps[0])
	}
}

func TestAnEpisodeWithAnImageOfItsOwnHasNoThumbGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	thumb := filepath.Join("Quiet Harbor (2008)", "Season 01", "Quiet Harbor - S01E05-thumb.jpg")
	seedArtSeries(t, catalog, "Quiet Harbor (2008)", []int{1}, fileRow{
		Path: thumb, Library: artLibrary, Present: true, Type: fileTypeImage,
		Role: fileRoleThumb, Items: []string{episodeID("series:tvdb:81189", 1, 5)},
	})

	if gaps := artGapsOf(t, catalog, factEpisodeThumb); len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none, because the episode holds its image", gaps)
	}
}

// The reporter counts a gap with the same query the container works from, so
// every art fact reaches the counts the operator schedules on.
func TestTheGapCountsHoldEveryArtFact(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedArtMovie(t, catalog, "The Signal (2014)")

	counts, err := catalog.gapCounts(t.Context(), artLibrary, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range artFactNames {
		if _, held := counts[fact]; !held {
			t.Errorf("counts = %v, want a count for %s", counts, fact)
		}
	}
	if counts[factPoster] != 1 {
		t.Errorf("poster count = %d, want the one title with no poster", counts[factPoster])
	}
}

// The art facts join the maps the rest of the enricher reads: the runs, the
// ledgers the scanner lifts, and the provider table.
func TestTheArtFactsJoinTheEnricherMaps(t *testing.T) {
	for _, fact := range artFactNames {
		t.Run(fact, func(t *testing.T) {
			if _, held := gapQueries[fact]; !held {
				t.Error("the fact has no gap query")
			}
			if _, held := factRuns[fact]; !held {
				t.Error("the fact has no run")
			}
			if !slices.Contains(likenFacts, fact) {
				t.Error("the scanner lifts no ledger for the fact")
			}
			if !servedByAnyProvider(fact) {
				t.Error("no row in the provider table holds the fact")
			}
		})
	}
}

// Whether any provider block can serve the fact, because an art fact no
// provider serves is a file nothing can write.
func servedByAnyProvider(fact string) bool {
	for _, facts := range providerFacts {
		if slices.Contains(facts, fact) {
			return true
		}
	}
	return false
}

// The name each fact writes, which is what Kodi and Jellyfin read.
func TestEachArtFactNamesItsFile(t *testing.T) {
	cases := []struct {
		fact string
		gap  artGap
		want string
	}{
		{fact: factPoster, want: "poster.jpg"},
		{fact: factBackdrop, want: "fanart.jpg"},
		{fact: factLogo, want: "clearlogo.png"},
		{fact: factClearart, want: "clearart.png"},
		{fact: factBanner, want: "banner.jpg"},
		{fact: factLandscape, want: "landscape.jpg"},
		{fact: factDiscart, want: "disc.png"},
		{fact: factSeasonPoster, gap: artGap{season: 2}, want: "season02-poster.jpg"},
		{fact: factSeasonPoster, gap: artGap{season: 0}, want: "season-specials-poster.jpg"},
		{fact: factSeasonBanner, gap: artGap{season: 2}, want: "season02-banner.jpg"},
		{fact: factSeasonBanner, gap: artGap{season: 0}, want: "season-specials-banner.jpg"},
		{fact: factEpisodeThumb, gap: artGap{key: "Season 01/Quiet Harbor - S01E05.mkv"},
			want: "Quiet Harbor - S01E05-thumb.jpg"},
	}
	for _, test := range cases {
		t.Run(test.want, func(t *testing.T) {
			if got := artTypes[test.fact].fileFor(test.gap); got != test.want {
				t.Errorf("file = %q, want %q", got, test.want)
			}
		})
	}
}

// The scanner lifts an art attempt onto the file it names, the way it lifts a
// probe attempt, so the gap query reads what the container wrote.
func TestTheScannerKeysAnArtAttemptOnTheFile(t *testing.T) {
	sidecar := likenSidecar{
		root: "/media", dir: "/media/The Signal (2014)", library: artLibrary,
		item: "movie:tmdb:603",
	}
	if got := sidecar.itemOf(factPoster, "poster.jpg"); got != "The Signal (2014)/poster.jpg" {
		t.Errorf("item = %q, want the file the fact writes", got)
	}
}

// A disc is a movie's, so a series is never a discart gap and a series library
// never names the fact in its art container.
func TestASeriesIsNeverADiscartGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedArtSeries(t, catalog, "Quiet Harbor (2008)", []int{1})
	seedArtMovie(t, catalog, "The Signal (2014)")

	gaps := artGapsOf(t, catalog, factDiscart)
	if len(gaps) != 1 || gaps[0].key != "The Signal (2014)/disc.png" {
		t.Errorf("gaps = %+v, want the movie alone", gaps)
	}

	shows := &Library{
		Metadata: ObjectMeta{Name: "shows", Namespace: "house"},
		Spec: LibrarySpec{
			Kind:    libraryKindSeries,
			Sources: []string{"fanart"},
		},
	}
	art := &MetadataProvider{
		Metadata: ObjectMeta{Name: "fanart", Namespace: "house"},
		Spec: MetadataProviderSpec{
			Fanart: &ProviderFanart{SecretRef: SecretKeyRef{Name: "fanart-key", Key: "token"}},
		},
		Status: MetadataProviderStatus{Conditions: []Condition{
			{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable},
		}},
	}
	served := servedArtFacts(shows, providerSet{"house/fanart": art})

	if slices.Contains(served, factDiscart) {
		t.Errorf("facts = %v, want no disc art in a series library", served)
	}
	if !slices.Contains(served, factSeasonBanner) {
		t.Errorf("facts = %v, want the season banner a series does carry", served)
	}
}
