package main

// These tests fix the *arr name parses and the file
// attribute reads, so a re-walk of a sidecar-less volume reads the same
// titles, years, and resolutions every time.

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseReleaseName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		title string
		year  int
	}{
		{name: "title and parenthesized year", input: "The Matrix (1999)", title: "The Matrix", year: 1999},
		{name: "dotted release cut at resolution", input: "The.Thing.1982.1080p.BluRay.x264-GROUP", title: "The Thing", year: 1982},
		{name: "dotted release with no year", input: "Some.Short.WEBRip.x264-GRP", title: "Some Short", year: 0},
		{name: "title keeps an internal dash", input: "Wall-E.2008.1080p.BluRay", title: "Wall-E", year: 2008},
		{name: "codec-group token cuts the title", input: "Movie.Name.2015.x265-RARBG", title: "Movie Name", year: 2015},
		{name: "plain title with no markers", input: "Mystery Folder", title: "Mystery Folder", year: 0},
		{name: "video extension is stripped", input: "The.Thing.1982.1080p.mkv", title: "The Thing", year: 1982},
		{name: "a four-digit part of a name is not a year", input: "Blade Runner 2049 (2017)", title: "Blade Runner 2049", year: 2017},
		{name: "bracketed year", input: "Civil War [2024]", title: "Civil War", year: 2024},
		{name: "a numeric title with a bracketed year", input: "2012 [2009]", title: "2012", year: 2009},
		{name: "a numeric run in the title with a bracketed year", input: "Blade Runner 2049 [2017]", title: "Blade Runner 2049", year: 2017},
		{name: "a numeric title with a parenthesized year", input: "300 (2006)", title: "300", year: 2006},
		{name: "a bare numeric title is not a year", input: "2012", title: "2012", year: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			title, year := parseReleaseName(testCase.input)
			if title != testCase.title || year != testCase.year {
				t.Errorf("parseReleaseName(%q) = %q %d, want %q %d", testCase.input, title, year, testCase.title, testCase.year)
			}
		})
	}
}

func TestParseSeasonFolder(t *testing.T) {
	cases := []struct {
		input  string
		season int
		ok     bool
	}{
		{input: "Season 02", season: 2, ok: true},
		{input: "Season 2", season: 2, ok: true},
		{input: "season 10", season: 10, ok: true},
		{input: "Specials", season: 0, ok: true},
		{input: "Extras", season: 0, ok: false},
		{input: "Breaking Bad", season: 0, ok: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.input, func(t *testing.T) {
			season, ok := parseSeasonFolder(testCase.input)
			if season != testCase.season || ok != testCase.ok {
				t.Errorf("parseSeasonFolder(%q) = %d %v, want %d %v", testCase.input, season, ok, testCase.season, testCase.ok)
			}
		})
	}
}

func TestParseEpisodeMarker(t *testing.T) {
	cases := []struct {
		input    string
		season   int
		episodes []int
		ok       bool
	}{
		{input: "Coastline - S02E05.mkv", season: 2, episodes: []int{5}, ok: true},
		{input: "show.s01e10.1080p.mkv", season: 1, episodes: []int{10}, ok: true},
		{input: "Show 2x05.mkv", season: 2, episodes: []int{5}, ok: true},
		{input: "S02 E05.mkv", season: 2, episodes: []int{5}, ok: true},
		{input: "No Marker Here.mkv", season: 0, episodes: nil, ok: false},
		{input: "Coastline - S04E10-E11 - The Long Way.mkv", season: 4, episodes: []int{10, 11}, ok: true},
		{input: "Coastline - S04E10-11 - The Long Way.mkv", season: 4, episodes: []int{10, 11}, ok: true},
		{input: "Coastline - S04E10E11 - The Long Way.mkv", season: 4, episodes: []int{10, 11}, ok: true},
		{input: "coastline.s04e10e12.720p.mkv", season: 4, episodes: []int{10, 11, 12}, ok: true},
		{input: "Coastline - S01E01-E40.mkv", season: 1, episodes: []int{1}, ok: true},
		{input: "Coastline - S01E05-E02.mkv", season: 1, episodes: []int{5}, ok: true},
		{input: "Coastline - S01E05-E05.mkv", season: 1, episodes: []int{5}, ok: true},
		{input: "coastline.s01e05-1080p.mkv", season: 1, episodes: []int{5}, ok: true},
		{input: "coastline.s01e05-11th.hour.mkv", season: 1, episodes: []int{5}, ok: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.input, func(t *testing.T) {
			season, episodes, ok := parseEpisodeMarker(testCase.input)
			if season != testCase.season || !reflect.DeepEqual(episodes, testCase.episodes) || ok != testCase.ok {
				t.Errorf("parseEpisodeMarker(%q) = %d %v %v, want %d %v %v", testCase.input, season, episodes, ok, testCase.season, testCase.episodes, testCase.ok)
			}
		})
	}
}

func TestFileAttributes(t *testing.T) {
	cases := []struct {
		name      string
		file      string
		stream    *streamInfo
		container string
		width     int
		height    int
		vcodec    string
		acodec    string
		duration  int64
	}{
		{
			name:      "streamdetails wins over the name",
			file:      "Movie.720p.mkv",
			stream:    &streamInfo{Width: 1920, Height: 1080, VideoCodec: "h264", AudioCodec: "dts", DurationMs: 8160000},
			container: "mkv", width: 1920, height: 1080, vcodec: "h264", acodec: "dts", duration: 8160000,
		},
		{
			name:      "the name fills resolution with no stream",
			file:      "Movie.2160p.WEB.mp4",
			container: "mp4", width: 3840, height: 2160,
		},
		{
			name:      "no resolution token leaves zeroes",
			file:      "Movie.avi",
			container: "avi",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			container, vcodec, acodec, width, height, duration := fileAttributes(testCase.file, testCase.stream)
			if container != testCase.container || width != testCase.width || height != testCase.height ||
				vcodec != testCase.vcodec || acodec != testCase.acodec || duration != testCase.duration {
				t.Errorf("fileAttributes = %q %q %q %d %d %d", container, vcodec, acodec, width, height, duration)
			}
		})
	}
}

func TestFolderKey(t *testing.T) {
	if got := folderKey("The Matrix (1999)"); got != "the-matrix-1999" {
		t.Errorf("folderKey = %q, want the-matrix-1999", got)
	}
}

func TestFolderKeyAddsAHashSuffixToALetterlessSlug(t *testing.T) {
	if got := folderKey("2012"); got != "2012-4b9a7f50" {
		t.Errorf("folderKey = %q, want 2012-4b9a7f50", got)
	}
}

func TestFolderKeyOfANameWithNoSlugIsTheHashAlone(t *testing.T) {
	if got := folderKey("千と千尋の神隠し"); got != "3146d995" {
		t.Errorf("folderKey = %q, want 3146d995", got)
	}
}

func TestFolderKeySeparatesTwoNonLatinNamesOfTheSameYear(t *testing.T) {
	first := folderKey("千と千尋の神隠し (2001)")
	second := folderKey("君の名は。 (2001)")
	if first == second {
		t.Errorf("two names key the same: %q", first)
	}
	if first != "2001-6c16d9c8" {
		t.Errorf("folderKey = %q, want 2001-6c16d9c8", first)
	}
	if second != "2001-f5c54d27" {
		t.Errorf("folderKey = %q, want 2001-f5c54d27", second)
	}
}

func TestFolderKeyIsStableForOneName(t *testing.T) {
	if first, second := folderKey("千と千尋の神隠し (2001)"), folderKey("千と千尋の神隠し (2001)"); first != second {
		t.Errorf("two passes differ: %q and %q", first, second)
	}
}

func TestDiscoverArtAndTrickplay(t *testing.T) {
	root := "testdata/movies"
	dir := filepath.Join(root, "Action", "The Matrix (1999)")

	primary, all, err := discoverArt(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	wantPrimary := filepath.Join("Action", "The Matrix (1999)", "folder.jpg")
	if primary != wantPrimary {
		t.Errorf("primary art = %q, want %q", primary, wantPrimary)
	}
	if len(all) != 3 {
		t.Errorf("art = %v, want the poster, backdrop, and logo", all)
	}

	trick := trickplayFor(root, dir, "The Matrix (1999).mkv")
	wantTrick := filepath.Join("Action", "The Matrix (1999)", "The Matrix (1999).trickplay")
	if trick != wantTrick {
		t.Errorf("trickplay = %q, want %q", trick, wantTrick)
	}
	if trickplayFor(root, dir, "Missing.mkv") != "" {
		t.Error("trickplayFor found a directory for a file with none")
	}
}

func TestListVideoFilesSkipsSidecars(t *testing.T) {
	dir := filepath.Join("testdata", "movies", "Action", "The Matrix (1999)")
	files, err := listVideoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "The Matrix (1999).mkv" {
		t.Errorf("video files = %v, want only the mkv", files)
	}
}

func TestResolutionFromNameLowerTiers(t *testing.T) {
	if w, h := resolutionFromName("Show.720p.mkv"); w != 1280 || h != 720 {
		t.Errorf("720p = %dx%d, want 1280x720", w, h)
	}
	if w, h := resolutionFromName("Show.480p.mkv"); w != 854 || h != 480 {
		t.Errorf("480p = %dx%d, want 854x480", w, h)
	}
}

func TestFileHelpersOnMissingPaths(t *testing.T) {
	files, err := listVideoFiles("testdata/nowhere")
	if files != nil || err == nil {
		t.Errorf("listVideoFiles = %v %v, want nothing and an error for a missing directory", files, err)
	}
	exists, err := fileExists("testdata/nowhere/x.mkv")
	if exists || err != nil {
		t.Errorf("fileExists = %v %v, want false and no error for a missing file", exists, err)
	}
	if added := addedTime("testdata/nowhere/x.mkv"); added != 0 {
		t.Errorf("addedTime = %d, want 0 for a missing file", added)
	}
}
