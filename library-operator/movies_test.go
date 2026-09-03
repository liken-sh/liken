package main

// These tests walk the testdata movies tree, so the
// grouping-folder descent, the sidecar and name identities, the file
// attributes, and the unidentified count are proved against real files.

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// filesByPath indexes a walk's file rows by path, so a test reads one
// file without depending on the order the walk read the folder in.
func filesByPath(result *walkResult) map[string]fileRow {
	index := map[string]fileRow{}
	for _, row := range result.files {
		index[row.Path] = row
	}
	return index
}

// Every file a title folder holds is one classified row, and the junk a
// desktop or a storage appliance leaves is no row at all.
func TestWalkMoviesReadsEveryFileTheTitleCarries(t *testing.T) {
	result := walkMovies("testdata/movies", "house/movies", nil)
	files := filesByPath(result)
	matrix := filepath.Join("Action", "The Matrix (1999)")

	cases := []struct {
		path         string
		wantType     string
		wantRole     string
		wantLanguage string
	}{
		{path: "The Matrix (1999).mkv", wantType: fileTypeVideo, wantRole: fileRolePrimary},
		{path: "movie.nfo", wantType: fileTypeMetadata, wantRole: fileRoleMovie},
		{path: "folder.jpg", wantType: fileTypeImage, wantRole: fileRolePoster},
		{path: "backdrop.jpg", wantType: fileTypeImage, wantRole: fileRoleBackdrop},
		{path: "logo.png", wantType: fileTypeImage, wantRole: fileRoleLogo},
		{path: "The Matrix (1999).en.srt", wantType: fileTypeSubtitle, wantRole: fileRoleFull, wantLanguage: "en"},
		{path: "The Matrix (1999).fr.forced.srt", wantType: fileTypeSubtitle, wantRole: fileRoleForced, wantLanguage: "fr"},
		{path: "The Matrix (1999).trickplay", wantType: fileTypeTrickplay, wantRole: fileRoleTiles},
		{path: filepath.Join("Extras", "The Matrix (1999)-trailer.mkv"), wantType: fileTypeVideo, wantRole: fileRoleTrailer},
		{path: filepath.Join("Extras", "Making Of.mkv"), wantType: fileTypeVideo, wantRole: fileRoleExtra},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			row, held := files[filepath.Join(matrix, testCase.path)]
			if !held {
				t.Fatalf("the walk read no row for %s", testCase.path)
			}
			if row.Type != testCase.wantType || row.Role != testCase.wantRole || row.Language != testCase.wantLanguage {
				t.Errorf("class = %s/%s/%s, want %s/%s/%s",
					row.Type, row.Role, row.Language, testCase.wantType, testCase.wantRole, testCase.wantLanguage)
			}
			if row.Items[0] != "movie:tmdb:603" {
				t.Errorf("items = %v, want the movie the folder holds", row.Items)
			}
			if row.Library != "house/movies" || !row.Present {
				t.Errorf("row = %+v, want it in this library and present", row)
			}
			if row.SizeBytes == 0 || row.Modified == 0 {
				t.Errorf("size = %d, modified = %d, want both from the walk's own stat", row.SizeBytes, row.Modified)
			}
		})
	}

	for _, junk := range []string{"Thumbs.db", ".DS_Store", filepath.Join("The Matrix (1999).trickplay", "1.jpg")} {
		if _, held := files[filepath.Join(matrix, junk)]; held {
			t.Errorf("the walk cataloged %s, which is no part of the title", junk)
		}
	}
}

// A title folder whose only extra files are junk yields the title and its
// video, and no row for the junk.
func TestWalkMoviesReadsNoRowFromAFolderOfJunk(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Solaris (1972)")
	writeFile(t, filepath.Join(dir, "movie.mkv"), "video")
	writeFile(t, filepath.Join(dir, "Thumbs.db"), "junk")
	writeFile(t, filepath.Join(dir, "desktop.ini"), "junk")
	writeFile(t, filepath.Join(dir, ".DS_Store"), "junk")

	result := walkMovies(root, "house/movies", nil)
	if len(result.files) != 1 {
		t.Errorf("files = %v, want only the video", filesByPath(result))
	}
}

// The walk reads a title folder and the extras folders under it, and goes no
// deeper, so a folder whose name is outside the extras set yields no rows.
func TestWalkMoviesDescendsNoFurtherThanAnExtrasFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Solaris (1972)")
	writeFile(t, filepath.Join(dir, "Solaris.mkv"), "video")
	writeFile(t, filepath.Join(dir, "Subs", "Solaris.en.srt"), "subtitle")
	writeFile(t, filepath.Join(dir, "Extras", "Making Of.mkv"), "video")

	files := filesByPath(walkMovies(root, "house/movies", nil))
	if _, held := files[filepath.Join("Solaris (1972)", "Subs", "Solaris.en.srt")]; held {
		t.Errorf("files = %v, want nothing from a folder outside the extras set", files)
	}
	if _, held := files[filepath.Join("Solaris (1972)", "Extras", "Making Of.mkv")]; !held {
		t.Errorf("files = %v, want the extras folder read", files)
	}
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

// The art the movie row carries is the art the file rows of the same
// folder carry, so a folder whose only poster is name-prefixed is browsable.
func TestWalkMoviesReadsNamePrefixedArt(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Solaris (1972)")
	writeFile(t, filepath.Join(dir, "Solaris (1972).mkv"), "video")
	writeFile(t, filepath.Join(dir, "Solaris (1972)-poster.jpg"), "image")
	writeFile(t, filepath.Join(dir, "Solaris (1972)-fanart.jpg"), "image")

	result := walkMovies(root, "house/movies", nil)
	movies := moviesByTitle(result)
	solaris, held := movies["Solaris"]
	if !held {
		t.Fatal("the walk did not read Solaris")
	}
	poster := filepath.Join("Solaris (1972)", "Solaris (1972)-poster.jpg")
	if solaris.Art != poster {
		t.Errorf("art = %q, want %q", solaris.Art, poster)
	}
	want := []string{poster, filepath.Join("Solaris (1972)", "Solaris (1972)-fanart.jpg")}
	if !reflect.DeepEqual(solaris.Body.Art, want) {
		t.Errorf("body art = %v, want %v", solaris.Body.Art, want)
	}
	files := filesByPath(result)
	if row := files[poster]; row.Role != fileRolePoster {
		t.Errorf("the poster's file row is %q, and the item's art is the same file", row.Role)
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
// out of the walk, while a nil set catalogs it. The ignore list is what a
// Library declares about its own volume. The service directories are skipped
// by name whatever the ignore list holds, so a recycle bin stays off the
// catalog with no configuration.
func TestWalkMoviesSkipsIgnoredFolders(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.nfo"), `<movie><title>The Matrix</title><year>1999</year></movie>`)
	writeFile(t, filepath.Join(root, "Archive", "Old Movie (2001)", "old.mkv"), "video")
	writeFile(t, filepath.Join(root, "#recycle", "Deleted Movie (2002)", "gone.mkv"), "video")

	kept := walkMovies(root, "house/movies", ignoreSet{"Archive": true})
	if kept.titles != 1 {
		t.Fatalf("titles = %d, want only the title outside the ignored folder", kept.titles)
	}
	if kept.movies[0].Title != "The Matrix" {
		t.Errorf("title = %q, want The Matrix", kept.movies[0].Title)
	}

	all := walkMovies(root, "house/movies", nil)
	if all.titles != 2 {
		t.Errorf("titles with no ignore = %d, want both, including the archived folder", all.titles)
	}
	for _, movie := range all.movies {
		if movie.Title == "Deleted Movie" {
			t.Errorf("the walk read %q from the recycle bin", movie.Title)
		}
	}
}

// A title nested under two grouping folders, genre then studio, is still
// found. The walk descends through a folder that has no movie.nfo and no
// video rather than cataloging it as a title, so a studio folder is a path
// on the way to a film, not a film itself.
func TestWalkMoviesDescendsThroughNestedGroupingFolders(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Genre", "Studio", "The Signal (2024)", "movie.nfo"),
		`<movie><title>The Signal</title><year>2024</year><uniqueid type="tmdb">424242</uniqueid></movie>`)
	writeFile(t, filepath.Join(root, "Genre", "Studio", "The Signal (2024)", "The Signal.mkv"), "video")
	writeFile(t, filepath.Join(root, "Genre", "The Beacon (2019)", "The Beacon.mkv"), "video")

	result := walkMovies(root, "house/movies", nil)
	if result.titles != 2 {
		t.Fatalf("titles = %d, want the deep title and the shallow one, not the grouping folders", result.titles)
	}
	titles := moviesByTitle(result)
	if _, grouped := titles["Studio"]; grouped {
		t.Errorf("the studio grouping folder was cataloged as a title")
	}
	nested, found := titles["The Signal"]
	if !found {
		t.Fatalf("the title two grouping folders down was not found")
	}
	if nested.Path != filepath.Join("Genre", "Studio", "The Signal (2024)") {
		t.Errorf("path = %q, want the folder relative to the root", nested.Path)
	}
}

// A sidecar that is not there is an ordinary title with no provider id:
// the walk falls back to the folder name and reads the volume in full.
func TestAMovieWithNoSidecarKeepsThePathIdentity(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Solaris (1972)", "movie.mkv"), "video")

	result := walkMovies(root, "house/movies", nil)

	if result.readError {
		t.Error("a title with no sidecar marked the walk incomplete")
	}
	if len(result.movies) != 1 || result.movies[0].Id != "movie:path:solaris-1972" {
		t.Errorf("movies = %+v, want the path-derived id", result.movies)
	}
}

// A sidecar the scanner cannot read is not a sidecar that is not there.
// The fall-back would mint a path-derived id for a title the catalog
// holds under its provider id, and the sweep would then delete the
// provider-derived rows. The walk marks itself incomplete instead, and
// the prune stands down.
func TestAnUnreadableMovieSidecarMarksTheWalkIncomplete(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Solaris (1972)")
	writeFile(t, filepath.Join(dir, "movie.mkv"), "video")
	// A directory in the sidecar's place is a read that fails for a
	// reason other than an absent file, on every filesystem and for any
	// user.
	if err := os.MkdirAll(filepath.Join(dir, "movie.nfo"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := walkMovies(root, "house/movies", nil)

	if !result.readError {
		t.Error("a sidecar that could not be read left the walk complete")
	}
}

// A hidden AppleDouble stub is a resource fork a storage appliance
// leaves beside a video, and never a video the catalog holds. It sorts
// before the video it shadows, so a walk that read it would make it the
// primary file.
func TestTheWalkSkipsAppleDoubleStubs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Solaris (1972)")
	writeFile(t, filepath.Join(dir, "._Solaris.mkv"), "resource fork")
	writeFile(t, filepath.Join(dir, "Solaris.mkv"), "video")

	files := filesByPath(walkMovies(root, "house/movies", nil))

	if _, held := files[filepath.Join("Solaris (1972)", "._Solaris.mkv")]; held {
		t.Errorf("files = %v, want no row for the AppleDouble stub", files)
	}
	if _, held := files[filepath.Join("Solaris (1972)", "Solaris.mkv")]; !held {
		t.Errorf("files = %v, want the video itself", files)
	}
}

// A folder holding nothing but AppleDouble stubs is no title folder, so
// the walk reads it as a grouping folder and mints no title.
func TestAFolderOfAppleDoubleStubsIsNoTitle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Solaris (1972)", "._Solaris.mkv"), "resource fork")

	result := walkMovies(root, "house/movies", nil)

	if len(result.movies) != 0 {
		t.Errorf("movies = %+v, want no title from a folder of stubs", result.movies)
	}
}

// A folder whose sidecar could not be read writes no row this pass. A
// row read from the folder name would carry a path-derived id beside the
// provider-derived one the catalog already holds, and a browser would
// then draw the title twice.
func TestAnUnreadableSidecarWritesNoRow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Solaris (1972)")
	writeFile(t, filepath.Join(dir, "movie.mkv"), "video")
	if err := os.MkdirAll(filepath.Join(dir, "movie.nfo"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := walkMovies(root, "house/movies", nil)

	if len(result.movies) != 0 {
		t.Errorf("movies = %+v, want no row from a folder with no identity", result.movies)
	}
	if len(result.files) != 0 {
		t.Errorf("files = %+v, want no file row either", result.files)
	}
}

// streamNFO is the sidecar the probe writes for one video: the root element
// the scanner reads, and the stream details inside it.
func streamNFO(title, videoCodec, audioCodec string, width, height, seconds int) string {
	return `<movie><title>` + title + `</title><fileinfo><streamdetails>` +
		`<video><codec>` + videoCodec + `</codec>` +
		`<width>` + strconv.Itoa(width) + `</width>` +
		`<height>` + strconv.Itoa(height) + `</height>` +
		`<durationinseconds>` + strconv.Itoa(seconds) + `</durationinseconds></video>` +
		`<audio><codec>` + audioCodec + `</codec></audio>` +
		`</streamdetails></fileinfo></movie>`
}

// partedMovieFolder holds every shape the sidecar rule covers: the first
// video, which the folder's own movie.nfo describes, a second video with its
// own sidecar, a second video with none, and an extra with its own.
func partedMovieFolder(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "Solaris (1972)")
	writeFile(t, filepath.Join(dir, "movie.nfo"), streamNFO("Solaris", "h264", "dts", 1920, 1080, 8000))
	writeFile(t, filepath.Join(dir, "Solaris (1972) - part1.mkv"), "video")
	writeFile(t, filepath.Join(dir, "Solaris (1972) - part2.mkv"), "video")
	writeFile(t, filepath.Join(dir, "Solaris (1972) - part2.nfo"),
		streamNFO("Solaris", "hevc", "eac3", 3840, 2160, 3000))
	writeFile(t, filepath.Join(dir, "Solaris (1972) - part3.720p.mkv"), "video")
	writeFile(t, filepath.Join(dir, "Extras", "Making Of.mkv"), "video")
	writeFile(t, filepath.Join(dir, "Extras", "Making Of.nfo"),
		streamNFO("Making Of", "mpeg4", "mp3", 640, 480, 600))
	return root
}

// Every video reads the sidecar the probe wrote for it, and a video with no
// sidecar still reads its name.
func TestEveryVideoReadsTheSidecarBesideIt(t *testing.T) {
	files := filesByPath(walkMovies(partedMovieFolder(t), "house/movies", nil))

	cases := []struct {
		name           string
		path           string
		wantWidth      int
		wantHeight     int
		wantVideoCodec string
		wantDurationMs int64
	}{
		{name: "the first video reads the folder's movie.nfo",
			path: "Solaris (1972) - part1.mkv", wantWidth: 1920, wantHeight: 1080,
			wantVideoCodec: "h264", wantDurationMs: 8000000},
		{name: "a second video reads its own sidecar",
			path: "Solaris (1972) - part2.mkv", wantWidth: 3840, wantHeight: 2160,
			wantVideoCodec: "hevc", wantDurationMs: 3000000},
		{name: "a video with no sidecar reads its name",
			path: "Solaris (1972) - part3.720p.mkv", wantWidth: 1280, wantHeight: 720},
		{name: "an extra reads its own sidecar",
			path: filepath.Join("Extras", "Making Of.mkv"), wantWidth: 640, wantHeight: 480,
			wantVideoCodec: "mpeg4", wantDurationMs: 600000},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row := files[filepath.Join("Solaris (1972)", test.path)]

			if row.Width != test.wantWidth || row.Height != test.wantHeight ||
				row.VideoCodec != test.wantVideoCodec || row.DurationMs != test.wantDurationMs {
				t.Errorf("attributes = %dx%d %s %dms, want %dx%d %s %dms",
					row.Width, row.Height, row.VideoCodec, row.DurationMs,
					test.wantWidth, test.wantHeight, test.wantVideoCodec, test.wantDurationMs)
			}
		})
	}
}

// A per-file sidecar the scanner cannot read marks the walk incomplete, the
// way an unreadable movie.nfo does, so the prune stands down.
func TestAnUnreadablePerFileSidecarMarksTheWalkIncomplete(t *testing.T) {
	cases := []struct {
		name    string
		sidecar string
	}{
		{name: "beside a second video", sidecar: "Solaris (1972) - part2.nfo"},
		{name: "inside an extras folder", sidecar: filepath.Join("Extras", "Making Of.nfo")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := partedMovieFolder(t)
			sidecar := filepath.Join(root, "Solaris (1972)", test.sidecar)
			// A directory in the sidecar's place is a read that fails for a
			// reason other than an absent file, on every filesystem and for any
			// user.
			if err := os.RemoveAll(sidecar); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(sidecar, 0o755); err != nil {
				t.Fatal(err)
			}

			if result := walkMovies(root, "house/movies", nil); !result.readError {
				t.Error("a sidecar that could not be read left the walk complete")
			}
		})
	}
}
