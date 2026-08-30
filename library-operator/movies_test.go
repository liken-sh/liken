package main

// These tests walk the testdata movies tree, so the
// grouping-folder descent, the sidecar and name identities, the file
// attributes, and the unidentified count are proved against real files.

import (
	"path/filepath"
	"testing"
)

// moviesByTitle indexes a walk's movie rows by title, so a test reads
// one title without depending on the order the walk read the volume in.
func moviesByTitle(result *walkResult) map[string]movieRow {
	index := map[string]movieRow{}
	for _, row := range result.movies {
		index[row.Title] = row
	}
	return index
}

// fileByItem finds the first file row linked to an item.
func fileByItem(result *walkResult, item string) (fileRow, bool) {
	for _, row := range result.files {
		for _, held := range row.Items {
			if held == item {
				return row, true
			}
		}
	}
	return fileRow{}, false
}

func TestWalkMoviesCountsAndIdentifies(t *testing.T) {
	result := walkMovies("testdata/movies", "house/movies", nil)

	if result.titles != 3 {
		t.Errorf("titles = %d, want 3", result.titles)
	}
	if result.unidentified != 1 {
		t.Errorf("unidentified = %d, want 1 (Mystery Folder)", result.unidentified)
	}

	movies := moviesByTitle(result)
	matrix, held := movies["The Matrix"]
	if !held {
		t.Fatal("the walk did not read The Matrix under the Action grouping folder")
	}
	if matrix.Id != "movie:tmdb:603" {
		t.Errorf("id = %q, want movie:tmdb:603", matrix.Id)
	}
	if matrix.Slug != "the-matrix-1999" || matrix.SortKey != "Matrix" {
		t.Errorf("slug/sort = %q %q", matrix.Slug, matrix.SortKey)
	}
	if matrix.Path != filepath.Join("Action", "The Matrix (1999)") {
		t.Errorf("path = %q, want the folder relative to the root", matrix.Path)
	}
	if matrix.Art != filepath.Join("Action", "The Matrix (1999)", "folder.jpg") {
		t.Errorf("art = %q, want folder.jpg relative to the root", matrix.Art)
	}
	if matrix.Duration != 8160 {
		t.Errorf("duration = %d, want the streamdetails runtime", matrix.Duration)
	}
	if matrix.Body.Collection != "The Matrix Collection" {
		t.Errorf("collection = %q", matrix.Body.Collection)
	}
}

func TestWalkMoviesReadsFileAttributesFromStreamdetails(t *testing.T) {
	result := walkMovies("testdata/movies", "house/movies", nil)
	file, held := fileByItem(result, "movie:tmdb:603")
	if !held {
		t.Fatal("The Matrix has no file linked")
	}
	if file.Width != 1920 || file.Height != 1080 || file.VideoCodec != "h264" || file.AudioCodec != "dts" {
		t.Errorf("attributes = %dx%d %s/%s, want the streamdetails values", file.Width, file.Height, file.VideoCodec, file.AudioCodec)
	}
	if file.Container != "mkv" {
		t.Errorf("container = %q, want mkv", file.Container)
	}
	if file.Trickplay == "" {
		t.Error("the file has no trickplay path")
	}
	if !file.Present {
		t.Error("the file is not marked present")
	}
}

func TestWalkMoviesReadsResolutionFromTheNameWithoutASidecar(t *testing.T) {
	result := walkMovies("testdata/movies", "house/movies", nil)
	movies := moviesByTitle(result)
	thing, held := movies["The Thing"]
	if !held {
		t.Fatal("the walk did not read The Thing from its release-name folder")
	}
	if thing.Id != "movie:path:the-thing-1982-1080p-bluray-x264-group" {
		t.Errorf("id = %q, want a path-scoped id", thing.Id)
	}
	file, _ := fileByItem(result, thing.Id)
	if file.Width != 1920 || file.Height != 1080 {
		t.Errorf("resolution = %dx%d, want the 1080p token from the name", file.Width, file.Height)
	}
}

func TestWalkMoviesCatalogsAnUnidentifiedFolderByName(t *testing.T) {
	result := walkMovies("testdata/movies", "house/movies", nil)
	movies := moviesByTitle(result)
	mystery, held := movies["Mystery Folder"]
	if !held {
		t.Fatal("the unidentified folder was not cataloged by its name")
	}
	if mystery.Id != "movie:path:mystery-folder" {
		t.Errorf("id = %q, want a path-scoped id", mystery.Id)
	}
}

func TestWalkMoviesEmitsEveryProviderAlias(t *testing.T) {
	result := walkMovies("testdata/movies", "house/movies", nil)
	aliases := map[string]string{}
	for _, row := range result.aliases {
		aliases[row.Alias] = row.Item
	}
	// The Matrix lists a tvdb id outside the canonical movie order, and
	// it still rolls onto the canonical id, so a lookup by it resolves.
	if aliases["movie:tvdb:12345"] != "movie:tmdb:603" {
		t.Errorf("tvdb alias = %q, want it to resolve The Matrix", aliases["movie:tvdb:12345"])
	}
	if aliases["movie:imdb:tt0133093"] != "movie:tmdb:603" {
		t.Errorf("imdb alias = %q", aliases["movie:imdb:tt0133093"])
	}
	if aliases["movie:path:the-matrix-1999"] != "movie:tmdb:603" {
		t.Errorf("folder alias = %q", aliases["movie:path:the-matrix-1999"])
	}
}

func TestWalkMoviesOnAMissingRoot(t *testing.T) {
	result := walkMovies("testdata/does-not-exist", "house/movies", nil)
	if result.titles != 0 || len(result.movies) != 0 {
		t.Errorf("result = %+v, want an empty walk", result)
	}
}

// A loose file at the root is skipped, and a root-level title folder
// with a movie.nfo is read as a title, so the walk reads both shapes
// the root can hold.
func TestWalkMoviesReadsARootLevelSidecarTitle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "readme.txt"), "not a title")
	writeFile(t, filepath.Join(root, "Solaris (1972)", "movie.nfo"), `<movie><title>Solaris</title><year>1972</year><uniqueid type="tmdb">7451</uniqueid></movie>`)
	writeFile(t, filepath.Join(root, "Solaris (1972)", "Solaris.mkv"), "video")

	result := walkMovies(root, "house/movies", nil)
	if result.titles != 1 {
		t.Fatalf("titles = %d, want the one sidecar title and not the loose file", result.titles)
	}
	if result.movies[0].Id != "movie:tmdb:7451" {
		t.Errorf("id = %q, want the sidecar id", result.movies[0].Id)
	}
}

// A folder named in the ignore list, and everything under it, is left
// out of the walk, while a nil set catalogs it, so the ignore list is
// what keeps a recycle bin off the catalog.
func TestWalkMoviesSkipsIgnoredFolders(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.nfo"), `<movie><title>The Matrix</title><year>1999</year></movie>`)
	writeFile(t, filepath.Join(root, "#recycle", "Old Movie (2001)", "old.mkv"), "video")

	kept := walkMovies(root, "house/movies", ignoreSet{"#recycle": true})
	if kept.titles != 1 {
		t.Fatalf("titles = %d, want only the title outside the recycle bin", kept.titles)
	}
	if kept.movies[0].Title != "The Matrix" {
		t.Errorf("title = %q, want The Matrix", kept.movies[0].Title)
	}

	if all := walkMovies(root, "house/movies", nil); all.titles != 2 {
		t.Errorf("titles with no ignore = %d, want both, including the recycled folder", all.titles)
	}
}
