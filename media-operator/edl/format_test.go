package edl

// These tests cover the timeline format itself: one segment per track, and the
// quoting that lets a file name or a title carry a comma.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A segment line parses into the file it plays and the title mpv turns into a
// chapter name. The quoting states each run's length, so the text inside it
// survives whatever punctuation it holds.
func TestParseSegment(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		wantFile  string
		wantTitle string
		wantOK    bool
	}{
		{
			name:      "a plain path and title",
			line:      "one.mp3,title=Oh No",
			wantFile:  "one.mp3",
			wantTitle: "Oh No",
			wantOK:    true,
		},
		{
			name:      "a quoted path with spaces and brackets",
			line:      "%37%/music/Aesop Rock [2007]/01 track.mp3,title=%5%Oh No",
			wantFile:  "/music/Aesop Rock [2007]/01 track.mp3",
			wantTitle: "Oh No",
			wantOK:    true,
		},
		{
			name:      "a quoted path holding a comma",
			line:      "%23%/music/Fitz, and Co.mp3,title=%13%Fitz, and Co.",
			wantFile:  "/music/Fitz, and Co.mp3",
			wantTitle: "Fitz, and Co.",
			wantOK:    true,
		},
		{
			name:      "a title holding an equals sign",
			line:      "one.mp3,title=%6%a=b=c=",
			wantFile:  "one.mp3",
			wantTitle: "a=b=c=",
			wantOK:    true,
		},
		{
			name:      "the positional start and length",
			line:      "one.mp3,10,260,title=Masterswarm",
			wantFile:  "one.mp3",
			wantTitle: "Masterswarm",
			wantOK:    true,
		},
		{
			name:     "a segment with no parameters",
			line:     "one.mp3",
			wantFile: "one.mp3",
			wantOK:   true,
		},
		{
			name: "a quoted run that states no end",
			line: "%34%one.mp3",
		},
		{
			name: "a quoted length that is not a number",
			line: "%many%one.mp3,title=One",
		},
		{
			name: "a line that names no file",
			line: ",title=One",
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			segment, ok := ParseSegment(each.line)
			mustMatch(t, ok, each.wantOK)
			mustMatch(t, segment.File, each.wantFile)
			mustMatch(t, segment.Params["title"], each.wantTitle)
		})
	}
}

// The parser reads the timeline and skips what it has no use for: the header,
// the comments, and the ! directives that carry stream and chapter options.
func TestParseSkipsTheHeaderAndTheDirectives(t *testing.T) {
	text := Header + "\n" +
		"!no_chapters\n" +
		"# a comment\n" +
		"\n" +
		"one.mp3,title=Oh No\n" +
		"two.mp3,title=Masterswarm\n"

	segments := Parse(text)
	mustMatch(t, len(segments), 2)
	mustMatch(t, segments[0].File, "one.mp3")
	mustMatch(t, segments[1].Params["title"], "Masterswarm")
}

// The album's own directory is where a relative segment path is measured from,
// and an absolute path stands on its own.
func TestSegmentPath(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		file string
		want string
	}{
		{name: "a name beside the timeline", dir: "/music/album", file: "one.mp3", want: "/music/album/one.mp3"},
		{name: "an absolute path", dir: "/music/album", file: "/elsewhere/one.mp3", want: "/elsewhere/one.mp3"},
		{name: "a URL", dir: "/music/album", file: "https://media.example/one.mp3", want: "https://media.example/one.mp3"},
		{name: "the inline form, which has no directory", dir: "", file: "one.mp3", want: "one.mp3"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, SegmentPath(each.dir, each.file), each.want)
		})
	}
}

// A file the caller names is a timeline only when it opens with the
// header. Anything else is the media file it names, and the caller plays it as
// one.
func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	timeline := filepath.Join(dir, "album.edl")
	mustSucceed(t, os.WriteFile(timeline, []byte(Header+"\ntrack.flac,180\n"), 0o644))
	plain := filepath.Join(dir, "track.flac")
	mustSucceed(t, os.WriteFile(plain, []byte("not a timeline"), 0o644))

	cases := []struct {
		name string
		path string
		ok   bool
	}{
		{name: "a timeline", path: timeline, ok: true},
		{name: "a media file", path: plain},
		{name: "a file that is not there", path: filepath.Join(dir, "absent.edl")},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			text, ok := ReadFile(each.path)
			if ok != each.ok {
				t.Fatalf("ok = %v, want %v", ok, each.ok)
			}
			if ok && !strings.HasPrefix(text, Header) {
				t.Errorf("text = %q, want it to open with the header", text)
			}
			if !ok && text != "" {
				t.Errorf("text = %q, want it empty", text)
			}
		})
	}
}
