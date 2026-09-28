package main

// These tests fix the *arr name parses and the file
// attribute reads, so a re-walk of a volume with no .nfo files reads the same
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
		{name: "title and parenthesized year", input: "Some Film (1999)", title: "Some Film", year: 1999},
		{name: "dotted release cut at resolution", input: "The.Long.Survey.1982.1080p.BluRay.x264-GROUP", title: "The Long Survey", year: 1982},
		{name: "dotted release with no year", input: "Some.Short.WEBRip.x264-GRP", title: "Some Short", year: 0},
		{name: "title keeps an internal dash", input: "Tide-Walker.2008.1080p.BluRay", title: "Tide-Walker", year: 2008},
		{name: "codec-group token cuts the title", input: "Movie.Name.2015.x265-GRP", title: "Movie Name", year: 2015},
		{name: "plain title with no markers", input: "Mystery Folder", title: "Mystery Folder", year: 0},
		{name: "video extension is stripped", input: "The.Long.Survey.1982.1080p.mkv", title: "The Long Survey", year: 1982},
		{name: "a four-digit part of a name is not a year", input: "Harbor Line 2049 (2017)", title: "Harbor Line 2049", year: 2017},
		{name: "bracketed year", input: "Dry Season [2024]", title: "Dry Season", year: 2024},
		{name: "a numeric title with a bracketed year", input: "2031 [2009]", title: "2031", year: 2009},
		{name: "a numeric run in the title with a bracketed year", input: "Harbor Line 2049 [2017]", title: "Harbor Line 2049", year: 2017},
		{name: "a numeric title with a parenthesized year", input: "412 (2006)", title: "412", year: 2006},
		{name: "a bare numeric title is not a year", input: "2031", title: "2031", year: 0},
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
		{input: "Copper Line", season: 0, ok: false},
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
	if got := folderKey("Some Film (1999)"); got != "some-film-1999" {
		t.Errorf("folderKey = %q, want some-film-1999", got)
	}
}

func TestFolderKeyAddsAHashSuffixToALetterlessSlug(t *testing.T) {
	if got := folderKey("2031"); got != "2031-d740238f" {
		t.Errorf("folderKey = %q, want 2031-d740238f", got)
	}
}

func TestFolderKeyOfANameWithNoSlugIsTheHashAlone(t *testing.T) {
	if got := folderKey("静かな港"); got != "3b0d11d8" {
		t.Errorf("folderKey = %q, want 3b0d11d8", got)
	}
}

func TestFolderKeySeparatesTwoNonLatinNamesOfTheSameYear(t *testing.T) {
	first := folderKey("静かな港 (2001)")
	second := folderKey("北風の町 (2001)")
	if first == second {
		t.Errorf("two names key the same: %q", first)
	}
	if first != "2001-caf8459e" {
		t.Errorf("folderKey = %q, want 2001-caf8459e", first)
	}
	if second != "2001-7935e3ff" {
		t.Errorf("folderKey = %q, want 2001-7935e3ff", second)
	}
}

func TestFolderKeyIsStableForOneName(t *testing.T) {
	if first, second := folderKey("静かな港 (2001)"), folderKey("静かな港 (2001)"); first != second {
		t.Errorf("two passes differ: %q and %q", first, second)
	}
}

func TestDiscoverArtAndTrickplay(t *testing.T) {
	root := "testdata/movies"
	dir := filepath.Join(root, "Action", "Some Film (1999)")

	primary, all, err := discoverArt(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	wantPrimary := filepath.Join("Action", "Some Film (1999)", "folder.jpg")
	if primary != wantPrimary {
		t.Errorf("primary art = %q, want %q", primary, wantPrimary)
	}
	if len(all) != 3 {
		t.Errorf("art = %v, want the poster, backdrop, and logo", all)
	}

	trick := trickplayFor(root, dir, "Some Film (1999).mkv")
	wantTrick := filepath.Join("Action", "Some Film (1999)", "Some Film (1999).trickplay")
	if trick != wantTrick {
		t.Errorf("trickplay = %q, want %q", trick, wantTrick)
	}
	if trickplayFor(root, dir, "Missing.mkv") != "" {
		t.Error("trickplayFor found a directory for a file with none")
	}
}

// The art a folder holds is the art the files table already classified,
// so a name-prefixed poster is the item's poster. A bare name wins over a
// prefixed one, among names of one shape the explicit mark wins over the
// generic one (poster over folder over cover), and the first name in
// order wins among equals.
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
			name:        "backdrop wins over fanart",
			files:       []string{"backdrop.jpg", "fanart.jpg"},
			wantPrimary: "",
			wantAll:     []string{"backdrop.jpg"},
		},
		{
			name:        "a name-prefixed poster",
			files:       []string{"The Long Survey, Part III [1989]-poster.jpg"},
			wantPrimary: "The Long Survey, Part III [1989]-poster.jpg",
			wantAll:     []string{"The Long Survey, Part III [1989]-poster.jpg"},
		},
		{
			name: "a name-prefixed set",
			files: []string{
				"Glass Tide (1972)-poster.jpg",
				"Glass Tide (1972)-fanart.jpg",
				"Glass Tide (1972)-clearlogo.png",
			},
			wantPrimary: "Glass Tide (1972)-poster.jpg",
			wantAll: []string{
				"Glass Tide (1972)-poster.jpg",
				"Glass Tide (1972)-fanart.jpg",
				"Glass Tide (1972)-clearlogo.png",
			},
		},
		{
			name:        "a bare poster wins over a prefixed one",
			files:       []string{"Glass Tide (1972)-poster.jpg", "folder.jpg"},
			wantPrimary: "folder.jpg",
			wantAll:     []string{"folder.jpg"},
		},
		{
			name:        "name order parts two prefixed posters",
			files:       []string{"Glass Tide (1972)-poster.jpg", "Amber Road (1966)-poster.jpg"},
			wantPrimary: "Amber Road (1966)-poster.jpg",
			wantAll:     []string{"Amber Road (1966)-poster.jpg"},
		},
		{
			name:        "poster wins over folder",
			files:       []string{"folder.jpg", "poster.jpg"},
			wantPrimary: "poster.jpg",
			wantAll:     []string{"poster.jpg"},
		},
		{
			name:        "poster wins over cover",
			files:       []string{"cover.jpg", "poster.jpg"},
			wantPrimary: "poster.jpg",
			wantAll:     []string{"poster.jpg"},
		},
		{
			name:        "folder wins over cover",
			files:       []string{"cover.jpg", "folder.jpg"},
			wantPrimary: "folder.jpg",
			wantAll:     []string{"folder.jpg"},
		},
		{
			name:        "a bare folder still wins over a prefixed poster",
			files:       []string{"folder.jpg", "Glass Tide (1972)-poster.jpg"},
			wantPrimary: "folder.jpg",
			wantAll:     []string{"folder.jpg"},
		},
		{
			name:        "a folder with no art",
			files:       []string{"Glass Tide (1972).mkv", "movie.nfo"},
			wantPrimary: "",
			wantAll:     nil,
		},
		{
			name:        "an image the words do not name is no art",
			files:       []string{"Glass Tide (1972).jpg", "Glass Tide (1972)-thumb.jpg"},
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

func TestListVideoFilesSkipsEveryFileThatIsNotAVideo(t *testing.T) {
	dir := filepath.Join("testdata", "movies", "Action", "Some Film (1999)")
	files, err := listVideoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "Some Film (1999).mkv" {
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
			name:  "Some Film (1999) [tmdbid-1001]",
			want:  map[string]string{"tmdb": "1001"},
			title: "Some Film",
			year:  1999,
		},
		{
			name:  "Jellyfin Documentary (2030) [imdbid-tt00000000]",
			want:  map[string]string{"imdb": "tt00000000"},
			title: "Jellyfin Documentary",
			year:  2030,
		},
		{
			name:  "Pine Hollow [tvdbid-800002]",
			want:  map[string]string{"tvdb": "800002"},
			title: "Pine Hollow",
		},
		{
			name:  "Two Ids [tmdbid-1001] [imdbid-tt9001001]",
			want:  map[string]string{"tmdb": "1001", "imdb": "tt9001001"},
			title: "Two Ids",
		},
		{
			name:  "The Long Survey (1982)",
			want:  nil,
			title: "The Long Survey",
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

func TestAnNFOsIdsWinOverANames(t *testing.T) {
	merged := mergeProviderIDs(map[string]string{"tmdb": "1"}, map[string]string{"tmdb": "2", "imdb": "tt3"})

	if merged["tmdb"] != "1" {
		t.Errorf("tmdb = %q, want the .nfo file's", merged["tmdb"])
	}
	if merged["imdb"] != "tt3" {
		t.Errorf("imdb = %q, want the name's, which the .nfo file left out", merged["imdb"])
	}
	if got := mergeProviderIDs(map[string]string{"tmdb": "1"}, nil); got["tmdb"] != "1" {
		t.Errorf("a name with no ids changed the .nfo file's to %v", got)
	}
}

func TestAFolderNamedWithAProviderIdTakesThatIdAndIsIdentified(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Some Film [tmdbid-1001]", "Some Film.mkv"), "video")

	result := walkMovies(root, "house/movies", nil)

	if len(result.movies) != 1 {
		t.Fatalf("movies = %+v, want one", result.movies)
	}
	if got := result.movies[0]; got.Id != "movie:tmdb:1001" || got.Title != "Some Film" {
		t.Errorf("movie = %+v, want the provider id and the plain title", got)
	}
	if result.unidentified != 0 {
		t.Errorf("unidentified = %d, want none, because the name states an id", result.unidentified)
	}
}

func TestASeriesFolderNamedWithAProviderIdTakesThatId(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Pine Hollow [tvdbid-800002]", "Season 01", "S01E01.mkv"), "video")

	result := walkSeries(root, "house/series", nil)

	if len(result.series) != 1 || result.series[0].Id != "series:tvdb:800002" {
		t.Fatalf("series = %+v, want the provider id off the name", result.series)
	}
}
