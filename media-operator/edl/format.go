// Package edl reads and writes mpv's EDL v0 format. An album plays as one
// EDL timeline: mpv reads it as one playlist entry with one duration and one
// chapter per track, so the display drives track selection with the chapter
// machinery a film already uses. The format lives in its own package
// of its own: the player shim writes a timeline, the command sidecar reads one
// back for the album's art, and the local tool prints one, and all three must
// agree byte for byte.
package edl

// The format is a header line, one segment per line, and comma-separated
// parameters, with a %<length>% quoting that lets a file name or a title
// carry a comma.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The header mpv reads an EDL by. A file that opens with anything else is no
// timeline. The edl:// URL form carries no header, because the scheme has
// already said what the text is.
const Header = "# mpv EDL v0"

// One segment of a timeline: the file it plays, and the named
// parameters the line carries, such as the title that becomes mpv's chapter
// name. The positional start and length are not read here.
type Segment struct {
	File   string
	Params map[string]string
}

// Parse reads a timeline into its segments. It skips the header, the
// comments, and the ! lines, which carry stream and chapter directives no
// reader here has a use for. A line it cannot read is skipped rather than
// fatal, so one malformed segment costs the album one cover and not the run.
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

// ParseSegment reads one line: the file name first, then the
// parameters. A positional field is the segment's start or length, which
// nothing here reads, so only the named parameters are kept.
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

// One field of a segment line. A file name and a positional number
// carry no name, and a parameter carries both halves of its name=value.
type field struct {
	name  string
	value string
}

// splitFields breaks one line on its commas, with the quoting the
// format defines: a %<length>% run states its own length in bytes, so the text
// inside it may hold a comma, an equals sign, or a percent sign and still
// arrive whole.
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

// readValue reads the right half of a name=value field, quoted or plain.
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

// readQuoted reads one %<length>%<text> run and returns the text and
// where the line goes on. A length that does not parse, or one that runs past
// the end of the line, is a line the reader cannot read.
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

// Quote writes one value in the format's own quoting: a percent sign, the
// length in bytes, another percent sign, then the text. The length is what
// lets a file name or a title carry a comma, an equals sign, or a percent
// sign, so every value is quoted and none is inspected first.
func Quote(value string) string {
	return "%" + strconv.Itoa(len(value)) + "%" + value
}

// SegmentPath is where one segment's file lives. A relative path is
// measured from the timeline's own directory, the way mpv measures it, and an
// absolute path or a URL stands on its own.
func SegmentPath(dir, file string) string {
	if dir == "" || filepath.IsAbs(file) || strings.Contains(file, "://") {
		return file
	}
	return filepath.Join(dir, file)
}

// ReadFile reads a timeline off disk. A file that opens with anything
// but the header is no timeline, and the caller reads it as the media file it
// names.
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
