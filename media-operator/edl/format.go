// Package edl reads and writes mpv's EDL v0 format. An album plays as
// one EDL timeline, which mpv reads as one playlist entry with one
// chapter for each track, so a remote's chapter keys skip between
// tracks. The player shim writes the timeline in the playback pod, and
// the local/edl tool prints the same timeline on a workstation.
//
// A timeline file is the header line and then one segment on each
// line. A segment is a list of comma-separated fields.
package edl

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Header is the first line of an EDL file. A timeline passed inline as
// an edl:// URL has no header.
const Header = "# mpv EDL v0"

// Segment is one line of a timeline: the file it plays and its named
// parameters, such as the title that mpv shows as the chapter name.
// The positional start and length fields are dropped.
type Segment struct {
	File   string
	Params map[string]string
}

// Parse returns the segments of a timeline. It skips the header, the
// comments, the header directives that start with !, such as
// !no_chapters, and each line that it cannot parse, so one malformed
// segment does not fail the whole timeline.
func Parse(text string) []Segment {
	var segments []Segment
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		if segment, ok := ParseSegment(line); ok {
			segments = append(segments, segment)
		}
	}
	return segments
}

// ParseSegment parses one segment line: the file name, then the
// fields. It keeps only the named fields, because a positional field
// is the segment's start or length.
func ParseSegment(line string) (Segment, bool) {
	fields, ok := splitFields(line)
	if !ok || len(fields) == 0 || fields[0].name != "" || fields[0].value == "" {
		return Segment{}, false
	}
	segment := Segment{File: fields[0].value, Params: map[string]string{}}
	for _, field := range fields[1:] {
		if field.name == "" {
			continue
		}
		segment.Params[field.name] = field.value
	}
	return segment, true
}

// field is one field of a segment line. A file name or a positional
// number has an empty name.
type field struct {
	name  string
	value string
}

// splitFields splits a line on its commas. A value quoted as
// %<length>%<text> gives its length in bytes, so its text can contain
// a comma, an equals sign, or a percent sign.
func splitFields(line string) ([]field, bool) {
	var fields []field
	pos := 0
	for {
		read := field{}
		if pos < len(line) && line[pos] == '%' {
			text, next, ok := readQuoted(line, pos)
			if !ok {
				return nil, false
			}
			read.value, pos = text, next
		} else {
			start := pos
			for pos < len(line) && line[pos] != ',' && line[pos] != '=' {
				pos++
			}
			word := line[start:pos]
			if pos < len(line) && line[pos] == '=' {
				pos++
				value, next, ok := readValue(line, pos)
				if !ok {
					return nil, false
				}
				read.name, read.value, pos = word, value, next
			} else {
				read.value = word
			}
		}
		fields = append(fields, read)
		if pos >= len(line) {
			return fields, true
		}
		if line[pos] != ',' {
			return nil, false
		}
		pos++
	}
}

func readValue(line string, pos int) (value string, next int, ok bool) {
	if pos < len(line) && line[pos] == '%' {
		return readQuoted(line, pos)
	}
	start := pos
	for pos < len(line) && line[pos] != ',' {
		pos++
	}
	return line[start:pos], pos, true
}

// readQuoted reads one %<length>%<text> value and returns the text and
// the position after it. It fails when the second % is missing, or the
// length is not a number or runs past the end of the line.
func readQuoted(line string, pos int) (text string, next int, ok bool) {
	end := strings.IndexByte(line[pos+1:], '%')
	if end < 0 {
		return "", 0, false
	}
	end += pos + 1
	length, err := strconv.Atoi(line[pos+1 : end])
	if err != nil || length < 0 || end+1+length > len(line) {
		return "", 0, false
	}
	return line[end+1 : end+1+length], end + 1 + length, true
}

// Quote returns a value as %<length>%<text>, with the length in bytes.
// It quotes every value, so the caller does not check a file name or a
// title for a comma, an equals sign, or a percent sign.
func Quote(value string) string {
	return "%" + strconv.Itoa(len(value)) + "%" + value
}

// SegmentPath returns the location of a segment's file. As in mpv, a
// relative path is relative to the timeline's directory. An absolute
// path, a URL, or a file of an inline timeline, which has no
// directory, is returned unchanged.
func SegmentPath(dir, file string) string {
	if dir == "" || filepath.IsAbs(file) || strings.Contains(file, "://") {
		return file
	}
	return filepath.Join(dir, file)
}

// ReadFile returns the text of a timeline file. It returns false when
// the file cannot be read, and when it does not start with Header, as
// a media file does not.
func ReadFile(path string) (text string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), Header) {
		return "", false
	}
	return string(data), true
}
