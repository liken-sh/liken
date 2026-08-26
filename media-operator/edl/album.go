package edl

// An album folder becomes a timeline. Two callers write one: the player
// shim in the playback pod, because only the pod mounts the media, and the
// local/edl tool on a workstation. Both call this code, so what a person
// sees locally is what a Play runs.

import (
	"cmp"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/dhowden/tag"
)

// The file names an album is made of, matched without case because a library
// writes either. mpv decodes all six, and dhowden/tag reads only the MP3, M4A,
// FLAC, OGG, and DSF families, so a wma or wav track takes its title from its
// file name and its cover from the folder it sits in.
var AudioExtensions = []string{".mp3", ".flac", ".m4a", ".ogg", ".wma", ".wav"}

// The track number a library writes in front of a file name, which the
// title fallback strips: digits, then a dash, a dot, or an underscore, with
// whatever spacing surrounds the mark.
var trackPrefix = regexp.MustCompile(`^\d+\s*[-._]\s*`)

// Facts are the album's words as its own files state them, for a caller
// that builds a presentation block from a folder.
//
// The year is a number, because the presentation block's year is a number.
// A file that states none reads as zero.
type Facts struct {
	Artist string
	Album  string
	Year   int
}

// AlbumFiles is the album in the order it plays. The file names carry the
// track order, so name order is play order and no tag is read to settle it.
//
// The walk descends, because a multi-disc album is a folder of folders, and
// the whole relative path settles the order, so CD1 comes before CD2 and
// each disc keeps its own track order. A hidden entry is skipped, because
// macOS and sync tools write dot-named companions beside the real files.
func AlbumFiles(dir string) ([]string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	var relatives []string
	walk := func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != absolute && strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !IsAudio(entry.Name()) {
			return nil
		}
		relative, err := filepath.Rel(absolute, path)
		if err != nil {
			return err
		}
		relatives = append(relatives, relative)
		return nil
	}
	if err := filepath.WalkDir(absolute, walk); err != nil {
		return nil, err
	}

	slices.SortFunc(relatives, compareNatural)
	files := make([]string, len(relatives))
	for index, relative := range relatives {
		files[index] = filepath.Join(absolute, relative)
	}
	return files, nil
}

// The order is natural, not the byte order the file system gives: a person
// writes track 2 and track 10 without padding, and the byte order plays 10
// before 2. The comparison reads both names in step, a run of digits as the
// number it spells and any other run as its bytes.
func compareNatural(a, b string) int {
	for len(a) > 0 && len(b) > 0 {
		digits := isDigit(a[0])
		if digits != isDigit(b[0]) {
			return cmp.Compare(a[0], b[0])
		}
		var left, right string
		if digits {
			left, a = runOf(a, isDigit)
			right, b = runOf(b, isDigit)
			if order := compareNumbers(left, right); order != 0 {
				return order
			}
			continue
		}
		left, a = runOf(a, func(c byte) bool { return !isDigit(c) })
		right, b = runOf(b, func(c byte) bool { return !isDigit(c) })
		if order := strings.Compare(left, right); order != 0 {
			return order
		}
	}
	return cmp.Compare(len(a), len(b))
}

// Two digit runs compare as the numbers they spell, and the written length
// breaks a value tie, so a padded name and a bare one hold one order.
func compareNumbers(a, b string) int {
	left, right := strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(left) != len(right) {
		return cmp.Compare(len(left), len(right))
	}
	if order := strings.Compare(left, right); order != 0 {
		return order
	}
	return cmp.Compare(len(a), len(b))
}

// runOf reads the leading run of bytes the test accepts, and returns it with
// what is left of the name.
func runOf(name string, accepts func(byte) bool) (run, rest string) {
	end := 0
	for end < len(name) && accepts(name[end]) {
		end++
	}
	return name[:end], name[end:]
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// IsAudio says whether one file name is an album track.
func IsAudio(name string) bool {
	extension := strings.ToLower(filepath.Ext(name))
	return slices.Contains(AudioExtensions, extension)
}

// Timeline writes the whole EDL: the header, then one segment per track.
// Each segment names the file and the title mpv turns into that track's
// chapter.
func Timeline(files []string) string {
	var text strings.Builder
	text.WriteString(Header + "\n")
	for _, file := range files {
		text.WriteString(Quote(file) + ",title=" + Quote(TitleOf(file)) + "\n")
	}
	return text.String()
}

// TitleOf is the track's name: the tag first, then the file name with its
// extension and its track number removed. A ripped library names each file
// for its track, so the file name is a sound fallback.
func TitleOf(file string) string {
	if title := taggedTitle(file); title != "" {
		return flatten(title)
	}
	name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	return flatten(trackPrefix.ReplaceAllString(name, ""))
}

// A timeline is a line per segment, so a title that holds a newline would
// end the segment early and the rest of the title would read as another
// one. Every control byte becomes a space, and a run of them becomes one
// space.
func flatten(title string) string {
	var text strings.Builder
	spaced := false
	for index := 0; index < len(title); index++ {
		if c := title[index]; c >= 0x20 && c != 0x7f {
			text.WriteByte(c)
			spaced = false
			continue
		}
		if !spaced {
			text.WriteByte(' ')
			spaced = true
		}
	}
	return strings.TrimSpace(text.String())
}

// taggedTitle reads one file's title tag, and reads nothing from a file with
// no tags.
func taggedTitle(file string) string {
	metadata := readTags(file)
	if metadata == nil {
		return ""
	}
	return strings.TrimSpace(metadata.Title())
}

// AlbumFacts is the album's words as its own files state them. The album's
// fields are the same on every track, so the first tagged file states them
// for the whole album.
func AlbumFacts(files []string) Facts {
	for _, file := range files {
		metadata := readTags(file)
		if metadata == nil {
			continue
		}
		return Facts{
			Artist: strings.TrimSpace(metadata.Artist()),
			Album:  strings.TrimSpace(metadata.Album()),
			Year:   max(0, metadata.Year()),
		}
	}
	return Facts{}
}

// readTags reads one file's tags, and reads nothing from a file it cannot open
// or that carries none.
func readTags(path string) tag.Metadata {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	metadata, err := tag.ReadFrom(file)
	if err != nil {
		return nil
	}
	return metadata
}
