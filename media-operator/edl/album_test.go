package edl

// The tests build an album folder under t.TempDir(), with the tags each case
// names, and check the timeline written for it.

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wanted no error, got %v", err)
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// id3Frame is one frame of the fixture tag: the four-character name and the
// bytes that follow the frame header.
type id3Frame struct {
	name    string
	payload []byte
}

// textFrame builds one ISO-8859-1 text frame, the encoding byte and the text.
func textFrame(name, value string) id3Frame {
	return id3Frame{name: name, payload: append([]byte{0}, value...)}
}

// writeTrack writes one file with an ID3v2.3 tag: the ten-byte header, the
// frames, and a few 0xFF bytes where the audio would start. The tag is the
// whole of what the tool reads, so the fixture needs no audio.
func writeTrack(t *testing.T, path string, frames ...id3Frame) string {
	t.Helper()
	var body bytes.Buffer
	for _, frame := range frames {
		body.WriteString(frame.name)
		mustSucceed(t, binary.Write(&body, binary.BigEndian, uint32(len(frame.payload))))
		body.Write([]byte{0, 0})
		body.Write(frame.payload)
	}

	var file bytes.Buffer
	file.WriteString("ID3")
	file.Write([]byte{3, 0, 0})
	size := 10 + body.Len()
	file.Write([]byte{
		byte(size>>21) & 0x7f, byte(size>>14) & 0x7f,
		byte(size>>7) & 0x7f, byte(size) & 0x7f,
	})
	file.Write(body.Bytes())
	file.Write(bytes.Repeat([]byte{0xff}, 8))

	mustSucceed(t, os.WriteFile(path, file.Bytes(), 0o644))
	return path
}

// Every value the timeline carries is quoted by its length in bytes, so a path
// or a title that holds a space, a bracket, or a comma arrives whole.
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

// A track's title is its tag. A file with no title tag falls back to its own
// name, without the extension and without the track number a library writes in
// front of it.
func TestTitleOf(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		frames []id3Frame
		want   string
	}{
		{
			name:   "the tag states the title",
			file:   "01 - track.mp3",
			frames: []id3Frame{textFrame("TIT2", "Oh No")},
			want:   "Oh No",
		},
		{
			name:   "a hyphen prefix falls away",
			file:   "01 - Masterswarm.mp3",
			frames: []id3Frame{textFrame("TPE1", "Aesop Rock")},
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
			path := writeTrack(t, filepath.Join(t.TempDir(), each.file), each.frames...)
			mustMatch(t, TitleOf(path), each.want)
		})
	}
}

// The timeline is the header and one segment per track, in name order, with
// every path absolute.
func TestTheTimeline(t *testing.T) {
	dir := t.TempDir()
	writeTrack(t, filepath.Join(dir, "02 - Masterswarm.mp3"), textFrame("TIT2", "Masterswarm"))
	writeTrack(t, filepath.Join(dir, "01 - Oh No.mp3"), textFrame("TIT2", "Oh No"))
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

// An album's own extensions are the ones mpv decodes, whatever case the
// library wrote them in.
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

// The album's words come from the first file that carries tags, and a field no
// file states stays empty.
func TestAlbumFacts(t *testing.T) {
	cases := []struct {
		name   string
		frames []id3Frame
		want   Facts
	}{
		{
			name: "every field",
			frames: []id3Frame{
				textFrame("TPE1", "Aesop Rock"),
				textFrame("TALB", "None Shall Pass"),
				textFrame("TYER", "2007"),
			},
			want: Facts{Artist: "Aesop Rock", Album: "None Shall Pass", Year: 2007},
		},
		{
			name:   "no year",
			frames: []id3Frame{textFrame("TPE1", "Aesop Rock"), textFrame("TALB", "None Shall Pass")},
			want:   Facts{Artist: "Aesop Rock", Album: "None Shall Pass"},
		},
		{
			name:   "no words at all",
			frames: []id3Frame{textFrame("TCON", "Rock")},
			want:   Facts{},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTrack(t, filepath.Join(dir, "01.mp3"), each.frames...)
			files, err := AlbumFiles(dir)
			mustSucceed(t, err)
			mustMatch(t, AlbumFacts(files), each.want)
		})
	}
}

// A person writes track 2 and track 10 without padding, so the order is the
// natural one and not the byte order the file system gives.
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

// A multi-disc album is a folder of folders, so the walk descends. The whole
// relative path settles the order, so CD1 plays before CD2 and each disc keeps
// its own track order.
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

// A title that holds a newline would end its segment early and the rest of it
// would read as another segment, so every control byte becomes one space.
func TestTitleOfFlattensAControlByte(t *testing.T) {
	dir := t.TempDir()
	writeTrack(t, filepath.Join(dir, "01.mp3"), textFrame("TIT2", "Oh No\r\nPart Two\tand Three"))

	files, err := AlbumFiles(dir)
	mustSucceed(t, err)
	mustMatch(t, TitleOf(files[0]), "Oh No Part Two and Three")

	segments := Parse(Timeline(files))
	mustMatch(t, len(segments), 1)
	mustMatch(t, segments[0].File, files[0])
	mustMatch(t, segments[0].Params["title"], "Oh No Part Two and Three")
}

// writeTracks writes one tagless file per name, so a case states the names
// alone and the order under test is the file names' own.
func writeTracks(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		writeTrack(t, filepath.Join(dir, name))
	}
}

// baseNames is the file names alone, for a case that asserts one folder's
// order.
func baseNames(dir string, files []string) []string {
	names := make([]string, len(files))
	for index, file := range files {
		names[index] = filepath.Base(file)
	}
	return names
}

// relativeNames is each file's path below the album, for a case that asserts
// the order across the disc folders.
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
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The album's facts come from the first file that carries tags. A folder
// whose files carry none has no facts, and the caller then names the album by
// its folder.
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
