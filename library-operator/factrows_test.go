package main

// What these tests read: a fact writes the rows for what it wrote, only the
// columns it owns, the moment it writes the files, and the sweep spares what
// a run wrote while a walk was under way.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTheSweepSparesRowsARunWroteAfterTheWalkBegan(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := context.Background()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	epoch := time.Now().UnixNano()
	later := walkStart(epoch) + 3600
	seed := &walkResult{
		files: []fileRow{
			{Library: contributorLibrary, Path: "Old (2001)/old.mkv", Present: true, Modified: 1},
			{Library: contributorLibrary, Path: "New (2002)/poster.jpg", Present: true, Modified: later},
		},
		attempts: []attemptRow{
			{Library: contributorLibrary, Item: "movie:tmdb:1", Fact: factOverview, At: 1, Result: attemptFound},
			{Library: contributorLibrary, Item: "movie:tmdb:2", Fact: factOverview, At: later, Result: attemptFound},
		},
	}
	if err := upsertWalk(ctx, catalog, seed); err != nil {
		t.Fatal(err)
	}
	// The walk marked something else, so the sweep runs, and neither row above.
	if _, err := catalog.markSeen(ctx, []string{seenItem + "movie:tmdb:9"}, epoch); err != nil {
		t.Fatal(err)
	}

	if _, err := pruneLibrary(ctx, catalog, contributorLibrary, epoch); err != nil {
		t.Fatal(err)
	}

	files := catalogLines(t, catalog, `SELECT path FROM files WHERE library = ?`)
	if strings.Join(files, ",") != "New (2002)/poster.jpg" {
		t.Errorf("files = %v, want only the row written after the walk began", files)
	}
	attempts := catalogLines(t, catalog, `SELECT item FROM attempts WHERE library = ?`)
	if strings.Join(attempts, ",") != "movie:tmdb:2" {
		t.Errorf("attempts = %v, want only the row written after the walk began", attempts)
	}
}

// The people the credits fact creates are in the contributor gap of the same
// catalog at once, which is what lets a contributors phase beside the nfo
// phase fill them in the same run.
func TestTheCreditsFactMakesItsPeopleVisibleToTheContributorGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	work.writeCredits(titleFolder(t, root, "One Film (1999)"), factAnswer{Cast: []creditedActor{
		{Name: "Tom Hanks", Role: "The Captain", IDs: providerIDs{"tmdb": "31"}},
	}})

	gaps, err := catalog.contributorGaps(t.Context(), contributorLibrary, factContributorIDs,
		time.Now(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].path != ".contributors/to/tom-hanks" || gaps[0].tmdb != "31" {
		t.Errorf("gaps = %+v, want the person the credits fact just created", gaps)
	}
}

func TestAnNFOFactWritesOnlyTheColumnsItOwns(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	// The catalog's title differs from the sidecar's, so a whole-row write
	// would show as a changed title.
	if _, err := catalog.updateItems(t.Context(), "movies", []string{"title"},
		[]itemUpdate{{Library: contributorLibrary, Id: "movie:tmdb:4242", Values: []any{"Seeded Title"}}}); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	rows := catalogLines(t, catalog, `SELECT title || '|' || nfo_facts || '|' || json_extract(body, '$.plot') FROM movies WHERE library = ?`)
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "Seeded Title|") ||
		!strings.Contains(rows[0], ",overview,") || !strings.HasSuffix(rows[0], "|A keeper watches the ice.") {
		t.Errorf("row = %v, want the seeded title with the body and nfo_facts the fact wrote", rows)
	}
	attempts := catalogLines(t, catalog, `SELECT concern || '|' || result FROM attempts WHERE library = ?`)
	if strings.Join(attempts, ",") != "overview|found" {
		t.Errorf("attempts = %v, want the fact's own attempt row", attempts)
	}
}

func TestTheProbeWritesTheStreamColumnsOfTheFile(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	rows := catalogLines(t, catalog, `SELECT video_codec || '|' || width FROM files WHERE library = ? AND type = 'video'`)
	if strings.Join(rows, ",") != "h264|1920" {
		t.Errorf("files = %v, want the streams the probe read", rows)
	}
}

func TestAnArtFactWritesTheImagesRowAndTheItemsArt(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	writeFile(t, filepath.Join(root, folder, "The Signal (2014).mkv"), "video")
	// The identity fact has written the id into the sidecar by the time art
	// runs, and that id is what keys the title's row.
	writeFile(t, filepath.Join(root, folder, movieSidecarName),
		"<movie><title>The Signal</title><uniqueid type=\"tmdb\" default=\"true\">603</uniqueid></movie>\n")
	seedArtMovie(t, catalog, folder)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newArtTMDb(t, map[string]string{
		tmdbKey("/3/movie/603/images", "", ""): imagesAnswer(tmdbPosters, "/quiet.jpg", artLanguage),
		tmdbKey("/t/p/w780/quiet.jpg", "", ""): testImage,
	})

	if err := work.artGap(t.Context(), factPoster, tmdbArtLine(client)); err != nil {
		t.Fatal(err)
	}

	poster := filepath.Join(folder, "poster.jpg")
	files := catalogLines(t, catalog, `SELECT path || '|' || role FROM files WHERE library = ? AND type = 'image'`)
	if strings.Join(files, ",") != poster+"|"+fileRolePoster {
		t.Errorf("files = %v, want the poster's own row", files)
	}
	items := catalogLines(t, catalog, `SELECT art || '|' || arts FROM movies WHERE library = ?`)
	if strings.Join(items, ",") != poster+`|["`+poster+`"]` {
		t.Errorf("movies = %v, want the poster as the art and the list", items)
	}
}

func TestTheIdentityFactRekeysTheTitle(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Thing (1982)"
	writeFile(t, filepath.Join(root, folder, "The Thing (1982).mkv"), "video")
	seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[` + tmdbResultJSON(1091, "The Thing", "1982-06-25") + `]}`,
	})

	if err := work.identityGap(t.Context(), client); err != nil {
		t.Fatal(err)
	}

	ids := catalogLines(t, catalog, `SELECT id FROM movies WHERE library = ?`)
	if strings.Join(ids, ",") != "movie:tmdb:1091" {
		t.Errorf("movies = %v, want the title under its provider id and no longer under its path", ids)
	}
}

func TestTheTrickplayFactWritesTheTrickplayColumn(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	if err := os.MkdirAll(filepath.Join(root, "The Thing (1982)", "The Thing (1982).trickplay"), 0o755); err != nil {
		t.Fatal(err)
	}

	work.recordArt(filepath.Join(root, "The Thing (1982)"), factTrickplay, "The Thing (1982).mkv", artProviderExisting, attemptFound)

	rows := catalogLines(t, catalog, `SELECT trickplay FROM files WHERE library = ? AND type = 'video'`)
	if len(rows) != 1 || rows[0] == "" {
		t.Errorf("files = %v, want the trickplay directory on the video's row", rows)
	}
}

func TestAContributorFactWritesTheColumnsItOwns(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	person := ".contributors/to/tom-hanks"
	writeContributorEntry(t, root, "tom-hanks", "name: Tom Hanks\nids: {tmdb: 31}\nborn: \"1956-07-09\"\n")
	seed := &walkResult{contributors: []contributorRow{{Library: contributorLibrary, Path: person, Name: "Tom Hanks"}}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	work.recordContributor(filepath.Join(root, person), factContributorIDs, providerBlockTMDb, attemptFound, "")

	rows := catalogLines(t, catalog, `SELECT born || '|' || headshot FROM contributors WHERE library = ?`)
	if strings.Join(rows, ",") != "1956-07-09|0" {
		t.Errorf("contributors = %v, want the born date the fact wrote", rows)
	}
	ids := catalogLines(t, catalog, `SELECT scheme || '|' || id FROM contributor_aliases WHERE library = ?`)
	if strings.Join(ids, ",") != "tmdb|31" {
		t.Errorf("contributor_aliases = %v, want the id the entry carries", ids)
	}
}

// The nfo fact's own row write carries the ratings block into the body,
// so a score reaches the catalog with the sidecar the fact just wrote.
func TestARatingFactWritesTheScoreIntoTheBody(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedNFOGap(t, catalog, root, "Winter Harbour (2011)", "movie:tmdb:4242")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factRatingIMDb, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	rows := catalogLines(t, catalog, `SELECT CAST(json_extract(body, '$.ratings.imdb') AS TEXT) FROM movies WHERE library = ?`)
	if strings.Join(rows, ",") != "7.9" {
		t.Errorf("ratings = %v, want the score the fact wrote", rows)
	}
}

// The volume holds the truth, so a fact whose catalog refuses its rows
// reports the refusal and the run goes on. Every fact answers the same way,
// whichever columns it owns and whichever of its writes is the one refused.
func TestAFactReportsTheRowsItsCatalogRefused(t *testing.T) {
	video := []fileRow{{
		Path: "One (2001)/one.mkv", Library: contributorLibrary, Type: fileTypeVideo,
		Role: fileRolePrimary, Present: true, Items: []string{"movie:tmdb:1"},
	}}
	title := []movieRow{{
		Id: "movie:tmdb:1", Library: contributorLibrary, Kind: libraryKindMovies,
		Path: "One (2001)", Title: "One",
	}}
	cases := []struct {
		name   string
		fact   string
		result *walkResult
	}{
		{"the probe's stream columns", factProbe, &walkResult{files: video, movies: title}},
		{"the trickplay column", factTrickplay, &walkResult{files: video}},
		{"the arrival columns", factArrival, &walkResult{files: video, movies: title}},
		{"an nfo fact's body", factOverview, &walkResult{movies: title}},
		{"the credits fact's body and credits", factCredits, &walkResult{
			movies: title,
			credits: []creditRow{{
				Library: contributorLibrary, Item: "movie:tmdb:1", Billing: 0,
				Contributor: ".contributors/person", Name: "A Person", Part: creditPartActor,
			}},
		}},
		{"an art fact's images and art column", factPoster, &walkResult{
			movies: title,
			files: []fileRow{{
				Path: "One (2001)/poster.jpg", Library: contributorLibrary,
				Type: fileTypeImage, Role: fileRolePoster, Present: true,
				Items: []string{"movie:tmdb:1"},
			}},
		}},
		{"a contributor fact's own columns", factContributorIDs, &walkResult{
			contributors: []contributorRow{{
				Library: contributorLibrary, Path: ".contributors/person", Name: "A Person",
			}},
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
			agent.transactionsLeft = 1

			err := work.writeOwnedRows(t.Context(), testCase.fact, testCase.result)

			if err == nil {
				t.Error("the fact reported no error for rows the catalog refused")
			}
		})
	}
}
