package main

// These tests fix the classification vocabulary in place: the category read
// off the extension, the role read off the name and the directory, and the
// language tag. A change to any of the three changes what the media browser
// draws, so each word is a case here.

import "testing"

func TestClassifyFile(t *testing.T) {
	movies := filePlace{kind: libraryKindMovies}
	extras := filePlace{kind: libraryKindMovies, extras: "extras"}
	trailers := filePlace{kind: libraryKindMovies, extras: extrasTrailers}
	series := filePlace{kind: libraryKindSeries}
	season := filePlace{kind: libraryKindSeries, season: true}

	cases := []struct {
		name         string
		place        filePlace
		wantType     string
		wantRole     string
		wantLanguage string
	}{
		{name: "The Matrix (1999).mkv", place: movies, wantType: fileTypeVideo, wantRole: fileRolePrimary},
		{name: "The Matrix (1999)-trailer.mkv", place: movies, wantType: fileTypeVideo, wantRole: fileRoleTrailer},
		{name: "The Matrix (1999)-sample.mkv", place: movies, wantType: fileTypeVideo, wantRole: fileRoleSample},
		{name: "The Matrix (1999)-behindthescenes.mkv", place: movies, wantType: fileTypeVideo, wantRole: fileRoleExtra},
		{name: "The Matrix (1999)-theme.mkv", place: movies, wantType: fileTypeVideo, wantRole: fileRoleTheme},
		{name: "-.mkv", place: movies, wantType: fileTypeVideo, wantRole: fileRolePrimary},
		{name: "Making Of.mkv", place: extras, wantType: fileTypeVideo, wantRole: fileRoleExtra},
		{name: "Chase.mkv", place: trailers, wantType: fileTypeVideo, wantRole: fileRoleTrailer},
		{name: "Making Of-trailer.mkv", place: extras, wantType: fileTypeVideo, wantRole: fileRoleTrailer},
		{name: "theme.mp3", place: movies, wantType: fileTypeAudio, wantRole: fileRoleTheme},
		{name: "The Matrix-theme.mp3", place: movies, wantType: fileTypeAudio, wantRole: fileRoleTheme},
		{name: "01 Opening.flac", place: movies, wantType: fileTypeAudio, wantRole: fileRoleTrack},
		{name: "The Matrix (1999).srt", place: movies, wantType: fileTypeSubtitle, wantRole: fileRoleFull},
		{name: "The Matrix (1999).en.srt", place: movies, wantType: fileTypeSubtitle, wantRole: fileRoleFull, wantLanguage: "en"},
		{name: "The Matrix (1999).fr.forced.srt", place: movies, wantType: fileTypeSubtitle, wantRole: fileRoleForced, wantLanguage: "fr"},
		{name: "The Matrix (1999).eng.sdh.ass", place: movies, wantType: fileTypeSubtitle, wantRole: fileRoleSDH, wantLanguage: "eng"},
		{name: "The Matrix (1999).en.cc.vtt", place: movies, wantType: fileTypeSubtitle, wantRole: fileRoleSDH, wantLanguage: "en"},
		{name: "folder.jpg", place: movies, wantType: fileTypeImage, wantRole: fileRolePoster},
		{name: "poster.png", place: movies, wantType: fileTypeImage, wantRole: fileRolePoster},
		{name: "cover.jpg", place: movies, wantType: fileTypeImage, wantRole: fileRolePoster},
		{name: "backdrop.jpg", place: movies, wantType: fileTypeImage, wantRole: fileRoleBackdrop},
		{name: "fanart.jpg", place: movies, wantType: fileTypeImage, wantRole: fileRoleBackdrop},
		{name: "logo.png", place: movies, wantType: fileTypeImage, wantRole: fileRoleLogo},
		{name: "clearlogo.png", place: movies, wantType: fileTypeImage, wantRole: fileRoleLogo},
		{name: "clearart.png", place: movies, wantType: fileTypeImage, wantRole: fileRoleClearart},
		{name: "banner.jpg", place: movies, wantType: fileTypeImage, wantRole: fileRoleBanner},
		{name: "disc.png", place: movies, wantType: fileTypeImage, wantRole: fileRoleDisc},
		{name: "discart.png", place: movies, wantType: fileTypeImage, wantRole: fileRoleDisc},
		{name: "cdart.png", place: movies, wantType: fileTypeImage, wantRole: fileRoleDisc},
		{name: "landscape.jpg", place: movies, wantType: fileTypeImage, wantRole: fileRoleThumb},
		{name: "season02-poster.jpg", place: season, wantType: fileTypeImage, wantRole: fileRolePoster},
		{name: "Breaking Bad - S02E05-thumb.jpg", place: season, wantType: fileTypeImage, wantRole: fileRoleThumb},
		{name: "Breaking Bad - S02E05.jpg", place: season, wantType: fileTypeImage, wantRole: fileRoleStill},
		{name: "extrafanart1.jpg", place: movies, wantType: fileTypeImage, wantRole: fileRoleBackdrop},
		{name: "screenshot.jpg", place: movies, wantType: fileTypeImage},
		{name: "movie.nfo", place: movies, wantType: fileTypeMetadata, wantRole: fileRoleMovie},
		{name: "The Matrix (1999).nfo", place: movies, wantType: fileTypeMetadata, wantRole: fileRoleMovie},
		{name: "tvshow.nfo", place: series, wantType: fileTypeMetadata, wantRole: fileRoleTVShow},
		{name: "season02.nfo", place: series, wantType: fileTypeMetadata, wantRole: fileRoleSeason},
		{name: "collection.nfo", place: movies, wantType: fileTypeMetadata, wantRole: fileRoleCollection},
		{name: "Breaking Bad - S02E05.nfo", place: season, wantType: fileTypeMetadata, wantRole: fileRoleEpisode},
		{name: "release.txt", place: movies, wantType: fileTypeOther},
		{name: "checksums.sfv", place: movies, wantType: fileTypeOther},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := classifyFile(testCase.name, testCase.place)
			want := fileClass{Type: testCase.wantType, Role: testCase.wantRole, Language: testCase.wantLanguage}
			if got != want {
				t.Errorf("classifyFile(%q) = %+v, want %+v", testCase.name, got, want)
			}
		})
	}
}

// The walk classifies a trickplay directory where it finds it, and never off
// an extension, so this is the one role no file name states.
func TestFileRoleOfATrickplayDirectory(t *testing.T) {
	if got := fileRoleOf(fileTypeTrickplay, "The Matrix (1999).trickplay", filePlace{}); got != fileRoleTiles {
		t.Errorf("role = %q, want %q", got, fileRoleTiles)
	}
	if got := fileRoleOf("", "anything", filePlace{}); got != "" {
		t.Errorf("role = %q, want none for a category with no vocabulary", got)
	}
}

func TestFileLanguage(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "The Matrix (1999).en.srt", want: "en"},
		{name: "The Matrix (1999).eng.srt", want: "eng"},
		{name: "The Matrix (1999).en.forced.srt", want: "en"},
		{name: "The Matrix (1999).eng.sdh.srt", want: "eng"},
		{name: "The Matrix (1999).EN.srt", want: "en"},
		{name: "The Matrix (1999).srt", want: ""},
		{name: "Up.srt", want: ""},
		{name: "The Matrix (1999).english.srt", want: ""},
		{name: "The Matrix (1999).1999.srt", want: ""},
		{name: "The Matrix (1999).e1.srt", want: ""},
		{name: "The Matrix (1999).forced.srt", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := fileLanguage(testCase.name); got != testCase.want {
				t.Errorf("fileLanguage(%q) = %q, want %q", testCase.name, got, testCase.want)
			}
		})
	}
}

func TestSkipName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: ".DS_Store", want: true},
		{name: ".hidden", want: true},
		{name: "Thumbs.db", want: true},
		{name: "thumbs.db", want: true},
		{name: "desktop.ini", want: true},
		{name: "folder.jpg", want: false},
		{name: "The Matrix (1999).mkv", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := skipName(testCase.name); got != testCase.want {
				t.Errorf("skipName(%q) = %v, want %v", testCase.name, got, testCase.want)
			}
		})
	}
}

func TestExtrasFolderName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "Extras", want: "extras"},
		{name: "Featurettes", want: "featurettes"},
		{name: "Trailers", want: extrasTrailers},
		{name: "Behind The Scenes", want: "behind the scenes"},
		{name: "Deleted Scenes", want: "deleted scenes"},
		{name: "Interviews", want: "interviews"},
		{name: "Scenes", want: "scenes"},
		{name: "Shorts", want: "shorts"},
		{name: "Clips", want: "clips"},
		{name: "Other", want: "other"},
		{name: "Season 02", want: ""},
		{name: "Subs", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := extrasFolderName(testCase.name); got != testCase.want {
				t.Errorf("extrasFolderName(%q) = %q, want %q", testCase.name, got, testCase.want)
			}
		})
	}
}

// The longest episode name wins. Without that rule an episode takes a file
// that belongs to another episode whose name starts with its own.
func TestEpisodeItem(t *testing.T) {
	episodes := map[string]string{
		"Show - S01E01":       "episode:tvdb:1:s01e01",
		"Show - S01E01 Extra": "episode:tvdb:1:s01e02",
	}
	resolve := episodeItem(episodes, "series:tvdb:1")

	cases := []struct {
		name string
		want string
	}{
		{name: "Show - S01E01.en.srt", want: "episode:tvdb:1:s01e01"},
		{name: "Show - S01E01 Extra.nfo", want: "episode:tvdb:1:s01e02"},
		{name: "season01-poster.jpg", want: "series:tvdb:1"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := resolve(testCase.name); got != testCase.want {
				t.Errorf("episodeItem(%q) = %q, want %q", testCase.name, got, testCase.want)
			}
		})
	}
}

func TestStatFileOnAMissingPath(t *testing.T) {
	size, modified := statFile("testdata/nowhere/x.jpg")
	if size != 0 || modified != 0 {
		t.Errorf("statFile = %d %d, want zeroes for a path that cannot be read", size, modified)
	}
}

// A directory the walk cannot read yields no rows and no subdirectories, so
// a permission error on one folder does not end the walk.
func TestFolderFilesOnAnUnreadableDirectory(t *testing.T) {
	rows, subdirectories := folderFiles{
		root:    "testdata",
		dir:     "testdata/nowhere",
		library: "house/movies",
		item:    constantItem("movie:tmdb:1"),
	}.read()
	if rows != nil || subdirectories != nil {
		t.Errorf("read = %v %v, want nothing from a directory that cannot be read", rows, subdirectories)
	}
}
