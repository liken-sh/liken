package edl

// This file turns an album folder into an EDL timeline. The player shim
// in the playback pod calls it because only that pod mounts the media,
// and the local/edl tool calls it on a workstation. Because both use
// this code, the timeline that local/edl prints is the one that a Play
// runs.

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

// AudioExtensions are the file extensions of album tracks, matched
// without regard to case. mpv decodes all of them. dhowden/tag reads
// tags only from MP3, M4A, FLAC, OGG, and DSF files, so a wma or wav
// track takes its title from its file name.
var AudioExtensions = []string{".mp3", ".flac", ".m4a", ".ogg", ".wma", ".wav"}

// trackPrefix matches the track number at the start of a file name,
// such as "01 - " or "02.", so that the title taken from the file name
// can leave it out. It needs a dash, dot, or underscore after the
// digits.
var trackPrefix = regexp.MustCompile(`^\d+\s*[-._]\s*`)

// Facts is the artist, album title, and year that the tags of an
// album's files give, for a caller that builds a presentation block
// from a folder. Year is 0 when no tag sets one.
type Facts struct {
	Artist string
	Album  string
	Year   int
}

// AlbumFiles returns the absolute paths of the album's tracks in play
// order. The order comes from the file names and not from the track
// tags.
//
// The walk enters subfolders, because a multi-disc album is a folder of
// disc folders. It sorts by the whole relative path, so CD1 plays
// before CD2 and each disc keeps its own track order. It skips files
// and folders whose names start with a dot, because macOS and sync
// tools write hidden companion files next to the real ones.
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

// compareNatural orders names so that track 2 comes before track 10.
// Byte order would put 10 first, and file names often have no zero
// padding. It compares the two names run by run, comparing a run of
// digits by its numeric value and any other run by its bytes.
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

// compareNumbers compares two digit strings by numeric value. When the
// values are equal, the shorter string sorts first, so "1" comes
// before "01" and names that differ only in zero padding still have a
// fixed order.
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

// runOf splits name after its longest prefix of bytes for which
// accepts returns true.
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

// IsAudio reports whether a file name has one of the AudioExtensions.
func IsAudio(name string) bool {
	extension := strings.ToLower(filepath.Ext(name))
	return slices.Contains(AudioExtensions, extension)
}

// Timeline returns the EDL text: the header, then one segment per
// file. A segment has the file's path and a title, which mpv uses as
// the chapter title of that track.
func Timeline(files []string) string {
	var text strings.Builder
	text.WriteString(Header + "\n")
	for _, file := range files {
		text.WriteString(Quote(file) + ",title=" + Quote(TitleOf(file)) + "\n")
	}
	return text.String()
}

// TitleOf returns a track's title from its tag. When the file has no
// title tag, it returns the file name without its extension and track
// number, which works for a ripped library because each file is named
// for its track.
func TitleOf(file string) string {
	if title := taggedTitle(file); title != "" {
		return flatten(title)
	}
	name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	return flatten(trackPrefix.ReplaceAllString(name, ""))
}

// flatten replaces each run of control bytes in a title with one
// space. The EDL has one line per segment, so a newline in a title
// would end the segment early and the rest of the title would parse
// as another segment.
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

// taggedTitle returns the file's title tag, or an empty string when the
// file has no readable tags.
func taggedTitle(file string) string {
	metadata := readTags(file)
	if metadata == nil {
		return ""
	}
	return strings.TrimSpace(metadata.Title())
}

// AlbumFacts returns the artist, album, and year from the first file
// that has readable tags, because those fields are the same on every
// track of an album. It returns empty Facts when no file has tags.
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

// readTags returns the file's tags, or nil when the file cannot be
// opened or has no tags.
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
