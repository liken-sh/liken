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

// The art a folder holds is the art the files table already
// classified, so a name-prefixed poster is the item's poster, a bare name wins
// over a prefixed one, and the first name in order wins among equals.
func TestDiscoverArtPicksByRole(t *testing.T) {
	cases := []struct {
		name        string
		files       []string
		wantPrimary string
		wantAll     []string
	}{
		{
			name:        "bare names",
			files:       []string{"folder.jpg", "backdrop.jpg", "logo.png"},
			wantPrimary: "folder.jpg",
			wantAll:     []string{"folder.jpg", "backdrop.jpg", "logo.png"},
		},
		{
			name:        "bare poster and fanart",
			files:       []string{"poster.jpg", "fanart.jpg"},
			wantPrimary: "poster.jpg",
			wantAll:     []string{"poster.jpg", "fanart.jpg"},
		},
		{
			name:        "folder wins over poster",
			files:       []string{"folder.jpg", "poster.jpg"},
			wantPrimary: "folder.jpg",
			wantAll:     []string{"folder.jpg"},
		},
		{
			name:        "backdrop wins over fanart",
			files:       []string{"backdrop.jpg", "fanart.jpg"},
			wantPrimary: "",
			wantAll:     []string{"backdrop.jpg"},
		},
		{
			name:        "a name-prefixed poster",
			files:       []string{"The Karate Kid, Part III [1989]-poster.jpg"},
			wantPrimary: "The Karate Kid, Part III [1989]-poster.jpg",
			wantAll:     []string{"The Karate Kid, Part III [1989]-poster.jpg"},
		},
		{
			name: "a name-prefixed set",
			files: []string{
				"Solaris (1972)-poster.jpg",
				"Solaris (1972)-fanart.jpg",
				"Solaris (1972)-clearlogo.png",
			},
			wantPrimary: "Solaris (1972)-poster.jpg",
			wantAll: []string{
				"Solaris (1972)-poster.jpg",
				"Solaris (1972)-fanart.jpg",
				"Solaris (1972)-clearlogo.png",
			},
		},
		{
			name:        "a bare poster wins over a prefixed one",
			files:       []string{"Solaris (1972)-poster.jpg", "folder.jpg"},
			wantPrimary: "folder.jpg",
			wantAll:     []string{"folder.jpg"},
		},
		{
			name:        "name order parts two prefixed posters",
			files:       []string{"Solaris (1972)-poster.jpg", "Andrei Rublev (1966)-poster.jpg"},
			wantPrimary: "Andrei Rublev (1966)-poster.jpg",
			wantAll:     []string{"Andrei Rublev (1966)-poster.jpg"},
		},
		{
			name:        "a folder with no art",
			files:       []string{"Solaris (1972).mkv", "movie.nfo"},
			wantPrimary: "",
			wantAll:     nil,
		},
		{
			name:        "an image the words do not name is no art",
			files:       []string{"Solaris (1972).jpg", "Solaris (1972)-thumb.jpg"},
			wantPrimary: "",
			wantAll:     nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "Title (1970)")
			for _, name := range testCase.files {
				writeFile(t, filepath.Join(dir, name), "bytes")
			}

			primary, all, err := discoverArt(root, dir)
			if err != nil {
				t.Fatal(err)
			}
			if primary != underTitle(testCase.wantPrimary) {
				t.Errorf("primary art = %q, want %q", primary, underTitle(testCase.wantPrimary))
			}
			var want []string
			for _, name := range testCase.wantAll {
				want = append(want, underTitle(name))
			}
			if !reflect.DeepEqual(all, want) {
				t.Errorf("art = %v, want %v", all, want)
			}
		})
	}
}

// underTitle renders a fixture's file name as the path relative to the
// library root, the form every art path takes. An empty name stays empty.
func underTitle(name string) string {
	if name == "" {
		return ""
	}
	return filepath.Join("Title (1970)", name)
}

func TestDiscoverArtOnAMissingFolder(t *testing.T) {
	primary, all, err := discoverArt("testdata", filepath.Join("testdata", "nowhere"))
	if err == nil {
		t.Errorf("discoverArt = %q %v, want an error for a folder it cannot read", primary, all)
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
}

func TestANameStatesAProviderIdInJellyfinsForm(t *testing.T) {
	cases := []struct {
		name  string
		want  map[string]string
		title string
		year  int
	}{
		{
			name:  "The Matrix (1999) [tmdbid-603]",
			want:  map[string]string{"tmdb": "603"},
			title: "The Matrix",
			year:  1999,
		},
		{
			name:  "Jellyfin Documentary (2030) [imdbid-tt00000000]",
			want:  map[string]string{"imdb": "tt00000000"},
			title: "Jellyfin Documentary",
			year:  2030,
		},
		{
			name:  "Twin Peaks [tvdbid-70533]",
			want:  map[string]string{"tvdb": "70533"},
			title: "Twin Peaks",
		},
		{
			name:  "Two Ids [tmdbid-603] [imdbid-tt0133093]",
			want:  map[string]string{"tmdb": "603", "imdb": "tt0133093"},
			title: "Two Ids",
		},
		{
			name:  "The Thing (1982)",
			want:  nil,
			title: "The Thing",
			year:  1982,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ids := parseProviderIDs(test.name)
			if len(ids) != len(test.want) {
				t.Fatalf("ids = %v, want %v", ids, test.want)
			}
			for provider, value := range test.want {
				if ids[provider] != value {
					t.Errorf("ids[%s] = %q, want %q", provider, ids[provider], value)
				}
			}
			title, year := parseReleaseName(test.name)
			if title != test.title || year != test.year {
				t.Errorf("parseReleaseName = %q, %d, want %q, %d", title, year, test.title, test.year)
			}
		})
	}
}

func TestASidecarsIdsWinOverANames(t *testing.T) {
	merged := mergeProviderIDs(map[string]string{"tmdb": "1"}, map[string]string{"tmdb": "2", "imdb": "tt3"})

	if merged["tmdb"] != "1" {
		t.Errorf("tmdb = %q, want the sidecar's", merged["tmdb"])
	}
	if merged["imdb"] != "tt3" {
		t.Errorf("imdb = %q, want the name's, which the sidecar left out", merged["imdb"])
	}
	if got := mergeProviderIDs(map[string]string{"tmdb": "1"}, nil); got["tmdb"] != "1" {
		t.Errorf("a name with no ids changed the sidecar's to %v", got)
	}
}

func TestAFolderNamedWithAProviderIdTakesThatIdAndIsIdentified(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix [tmdbid-603]", "The Matrix.mkv"), "video")

	result := walkMovies(root, "house/movies", nil)

	if len(result.movies) != 1 {
		t.Fatalf("movies = %+v, want one", result.movies)
	}
	if got := result.movies[0]; got.Id != "movie:tmdb:603" || got.Title != "The Matrix" {
		t.Errorf("movie = %+v, want the provider id and the plain title", got)
	}
	if result.unidentified != 0 {
		t.Errorf("unidentified = %d, want none, because the name states an id", result.unidentified)
	}
}

func TestASeriesFolderNamedWithAProviderIdTakesThatId(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Twin Peaks [tvdbid-70533]", "Season 01", "S01E01.mkv"), "video")

	result := walkSeries(root, "house/series", nil)

	if len(result.series) != 1 || result.series[0].Id != "series:tvdb:70533" {
		t.Fatalf("series = %+v, want the provider id off the name", result.series)
	}
}
