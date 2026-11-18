package edl

// These tests build album folders in t.TempDir() with tracks that
// edltest writes, tagged or not.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/media-operator/edl/edltest"
)

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("the setup returned the error %v, want none", err)
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("the value is %+v, want %+v", got, want)
	}
}

// Quote prefixes a value with its length in bytes, so a path or title
// with a space, bracket, or comma stays one value.
func TestQuote(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "a plain name", value: "one.mp3", want: "%7%one.mp3"},
		{name: "a path with spaces", value: "/music/Oh No.mp3", want: "%16%/music/Oh No.mp3"},
		{name: "a path with brackets", value: "/music/Album [2007]/01.mp3", want: "%26%/music/Album [2007]/01.mp3"},
		{name: "a title with a comma", value: "Fitz, and Co.", want: "%13%Fitz, and Co."},
		{name: "an empty value", value: "", want: "%0%"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, Quote(each.value), each.want)
		})
	}
}

// A track's title is its title tag. Without a tag, it is the file name
// without the extension and the leading track number.
func TestTitleOf(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		frames []edltest.Frame
		want   string
	}{
		{
			name:   "the tag states the title",
			file:   "01 - track.mp3",
			frames: []edltest.Frame{edltest.TextFrame("TIT2", "Oh No")},
			want:   "Oh No",
		},
		{
			name:   "a hyphen prefix falls away",
			file:   "01 - Masterswarm.mp3",
			frames: []edltest.Frame{edltest.TextFrame("TPE1", "Aesop Rock")},
			want:   "Masterswarm",
		},
		{
			name:   "a dot prefix falls away",
			file:   "02. Fitz and the Dizzyspells.mp3",
			frames: nil,
			want:   "Fitz and the Dizzyspells",
		},
		{
			name:   "a bare number prefix stays, because it names no separator",
			file:   "03 Coffee.mp3",
			frames: nil,
			want:   "03 Coffee",
		},
		{
			name:   "a name with no prefix at all",
			file:   "Coffee.mp3",
			frames: nil,
			want:   "Coffee",
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			path := edltest.WriteTrack(t, filepath.Join(t.TempDir(), each.file), each.frames...)
			mustMatch(t, TitleOf(path), each.want)
		})
	}
}

// The timeline is the header and one segment per track, in name order,
// with absolute paths.
func TestTheTimeline(t *testing.T) {
	dir := t.TempDir()
	edltest.WriteTrack(t, filepath.Join(dir, "02 - Masterswarm.mp3"), edltest.TextFrame("TIT2", "Masterswarm"))
	edltest.WriteTrack(t, filepath.Join(dir, "01 - Oh No.mp3"), edltest.TextFrame("TIT2", "Oh No"))
	mustSucceed(t, os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte("not audio"), 0o644))

	files, err := AlbumFiles(dir)
	mustSucceed(t, err)
	mustMatch(t, len(files), 2)

	lines := strings.Split(strings.TrimSuffix(Timeline(files), "\n"), "\n")
	mustMatch(t, len(lines), 3)
	mustMatch(t, lines[0], Header)
	mustMatch(t, lines[1], Quote(filepath.Join(dir, "01 - Oh No.mp3"))+",title=%5%Oh No")
	mustMatch(t, lines[2], Quote(filepath.Join(dir, "02 - Masterswarm.mp3"))+",title=%11%Masterswarm")
}

// AlbumFiles keeps the files with an audio extension, in any letter
// case, and skips other files.
func TestAlbumFilesTakeTheAudioNames(t *testing.T) {
	dir := t.TempDir()
	mustSucceed(t, os.MkdirAll(filepath.Join(dir, "scans"), 0o755))
	for _, name := range []string{"a.mp3", "b.FLAC", "c.m4a", "d.ogg", "e.wma", "f.wav", "g.txt", "cover.jpg"} {
		mustSucceed(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	files, err := AlbumFiles(dir)
	mustSucceed(t, err)
	mustMatch(t, len(files), 6)
}

// The facts come from the first file that has tags, and a field that no
// tag sets is empty.
func TestAlbumFacts(t *testing.T) {
	cases := []struct {
		name   string
		frames []edltest.Frame
		want   Facts
	}{
		{
			name: "every field",
			frames: []edltest.Frame{
				edltest.TextFrame("TPE1", "Aesop Rock"),
				edltest.TextFrame("TALB", "None Shall Pass"),
				edltest.TextFrame("TYER", "2007"),
			},
			want: Facts{Artist: "Aesop Rock", Album: "None Shall Pass", Year: 2007},
		},
		{
			name:   "no year",
			frames: []edltest.Frame{edltest.TextFrame("TPE1", "Aesop Rock"), edltest.TextFrame("TALB", "None Shall Pass")},
			want:   Facts{Artist: "Aesop Rock", Album: "None Shall Pass"},
		},
		{
			name:   "no words at all",
			frames: []edltest.Frame{edltest.TextFrame("TCON", "Rock")},
			want:   Facts{},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			dir := t.TempDir()
			edltest.WriteTrack(t, filepath.Join(dir, "01.mp3"), each.frames...)
			files, err := AlbumFiles(dir)
			mustSucceed(t, err)
			mustMatch(t, AlbumFacts(files), each.want)
		})
	}
}

// Track 2 sorts before track 10 even without zero padding, which byte
// order would not do.
func TestTheTrackOrderIsNatural(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  []string
	}{
		{
			name:  "unpadded numbers",
			files: []string{"1 - a.mp3", "2 - b.mp3", "10 - c.mp3", "11 - d.mp3"},
			want:  []string{"1 - a.mp3", "2 - b.mp3", "10 - c.mp3", "11 - d.mp3"},
		},
		{
			name:  "padded numbers, the order they already held",
			files: []string{"01 - a.mp3", "02 - b.mp3", "10 - c.mp3", "11 - d.mp3"},
			want:  []string{"01 - a.mp3", "02 - b.mp3", "10 - c.mp3", "11 - d.mp3"},
		},
		{
			name:  "a padded name and a bare one",
			files: []string{"01.mp3", "1.mp3", "2.mp3"},
			want:  []string{"1.mp3", "01.mp3", "2.mp3"},
		},
		{
			name:  "names with no number at all",
			files: []string{"beta.mp3", "alpha.mp3"},
			want:  []string{"alpha.mp3", "beta.mp3"},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTracks(t, dir, each.files...)
			files, err := AlbumFiles(dir)
			mustSucceed(t, err)
			mustMatchAll(t, baseNames(dir, files), each.want)
		})
	}
}

// The walk enters disc folders and sorts by the whole relative path,
// so CD1 comes before CD2 and each disc keeps its own track order. It
// skips hidden folders.
func TestAlbumFilesWalksTheDiscFolders(t *testing.T) {
	dir := t.TempDir()
	mustSucceed(t, os.MkdirAll(filepath.Join(dir, "CD1"), 0o755))
	mustSucceed(t, os.MkdirAll(filepath.Join(dir, "CD2"), 0o755))
	mustSucceed(t, os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755))
	writeTracks(t, filepath.Join(dir, "CD2"), "1 - c.mp3", "2 - d.mp3")
	writeTracks(t, filepath.Join(dir, "CD1"), "1 - a.mp3", "10 - b.mp3")
	writeTracks(t, filepath.Join(dir, ".hidden"), "1 - never.mp3")
	mustSucceed(t, os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte("not a track"), 0o644))

	files, err := AlbumFiles(dir)
	mustSucceed(t, err)
	mustMatchAll(t, relativeNames(dir, files), []string{
		filepath.Join("CD1", "1 - a.mp3"),
		filepath.Join("CD1", "10 - b.mp3"),
		filepath.Join("CD2", "1 - c.mp3"),
		filepath.Join("CD2", "2 - d.mp3"),
	})
}

// A newline in a title would end its segment early, so each run of
// control bytes becomes one space and the timeline still parses as one
// segment.
func TestTitleOfFlattensAControlByte(t *testing.T) {
	dir := t.TempDir()
	edltest.WriteTrack(t, filepath.Join(dir, "01.mp3"), edltest.TextFrame("TIT2", "Oh No\r\nPart Two\tand Three"))

	files, err := AlbumFiles(dir)
	mustSucceed(t, err)
	mustMatch(t, TitleOf(files[0]), "Oh No Part Two and Three")

	segments := Parse(Timeline(files))
	mustMatch(t, len(segments), 1)
	mustMatch(t, segments[0].File, files[0])
	mustMatch(t, segments[0].Params["title"], "Oh No Part Two and Three")
}

// writeTracks writes one file without tags for each name, so the order
// under test comes from the file names alone.
func writeTracks(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		edltest.WriteTrack(t, filepath.Join(dir, name))
	}
}

func baseNames(dir string, files []string) []string {
	names := make([]string, len(files))
	for index, file := range files {
		names[index] = filepath.Base(file)
	}
	return names
}

func relativeNames(dir string, files []string) []string {
	names := make([]string, len(files))
	for index, file := range files {
		names[index], _ = filepath.Rel(dir, file)
	}
	return names
}

func mustMatchAll[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("the value is %+v, want %+v", got, want)
	}
}

// Files that have no readable tags, or do not exist, give empty Facts.
func TestAlbumFactsFromFilesThatCarryNoTags(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"01.flac", "02.flac"} {
		mustSucceed(t, os.WriteFile(filepath.Join(dir, name), []byte("not a media file"), 0o644))
	}

	facts := AlbumFacts([]string{
		filepath.Join(dir, "absent.flac"),
		filepath.Join(dir, "01.flac"),
		filepath.Join(dir, "02.flac"),
	})

	if facts != (Facts{}) {
		t.Errorf("facts = %+v, want none", facts)
	}
}
