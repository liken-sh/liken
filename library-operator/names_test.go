package main

// These tests fix the *arr name parses and the file
// attribute reads, so a re-walk of a sidecar-less volume reads the same
// titles, years, and resolutions every time.

import (
	"path/filepath"
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
		input   string
		season  int
		episode int
		ok      bool
	}{
		{input: "Breaking Bad - S02E05.mkv", season: 2, episode: 5, ok: true},
		{input: "show.s01e10.1080p.mkv", season: 1, episode: 10, ok: true},
		{input: "Show 2x05.mkv", season: 2, episode: 5, ok: true},
		{input: "S02 E05.mkv", season: 2, episode: 5, ok: true},
		{input: "No Marker Here.mkv", season: 0, episode: 0, ok: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.input, func(t *testing.T) {
			season, episode, ok := parseEpisodeMarker(testCase.input)
			if season != testCase.season || episode != testCase.episode || ok != testCase.ok {
				t.Errorf("parseEpisodeMarker(%q) = %d %d %v, want %d %d %v", testCase.input, season, episode, ok, testCase.season, testCase.episode, testCase.ok)
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

func TestDiscoverArtAndTrickplay(t *testing.T) {
	root := "testdata/movies"
	dir := filepath.Join(root, "Action", "The Matrix (1999)")

	primary, all := discoverArt(root, dir)
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
	files := listVideoFiles(dir)
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
	if files := listVideoFiles("testdata/nowhere"); files != nil {
		t.Errorf("listVideoFiles = %v, want nil for a missing directory", files)
	}
	if size := fileSize("testdata/nowhere/x.mkv"); size != 0 {
		t.Errorf("fileSize = %d, want 0 for a missing file", size)
	}
	if added := addedTime("testdata/nowhere/x.mkv"); added != 0 {
		t.Errorf("addedTime = %d, want 0 for a missing file", added)
	}
}
