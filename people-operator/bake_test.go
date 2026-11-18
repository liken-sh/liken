package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The bake command prints one JSON line for a file: the thumbnail,
// modification time, and size, or the reason it failed.
func TestBakePrintsOneLinePerFile(t *testing.T) {
	dir := t.TempDir()
	picture := solid(t, 16, 16, green)
	write := func(name string, body []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		return path
	}
	when := time.Date(2026, 9, 29, 14, 2, 11, 0, time.UTC)
	good := write("ada.png", picture)
	if err := os.Chtimes(good, when, when); err != nil {
		t.Fatalf("setting the modification time of ada.png: %v", err)
	}

	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantResult bakeResult
	}{
		{"a picture", good, 0, bakeResult{ModTime: "2026-09-29T14:02:11Z", Size: int64(len(picture))}},
		{"no file", filepath.Join(dir, "missing.png"), 1, bakeResult{Reason: reasonBakeFailed}},
		{"a directory", dir, 1, bakeResult{Reason: reasonBakeFailed}},
		{"a file over 10 MiB", write("large.png", make([]byte, maxPictureBytes+1)), 1, bakeResult{Reason: reasonBakeFailed}},
		{"a file that is no picture", write("notes.txt", []byte("hello")), 1, bakeResult{Reason: reasonDecodeFailed}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			status := bake(&out, c.path, blue)

			if !strings.HasSuffix(out.String(), "\n") || strings.Count(out.String(), "\n") != 1 {
				t.Errorf("the command wrote %q, want one line", out.String())
			}
			var got bakeResult
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decoding the line %q as JSON: %v", out.String(), err)
			}
			if status != c.wantStatus || got.ModTime != c.wantResult.ModTime || got.Size != c.wantResult.Size || got.Reason != c.wantResult.Reason {
				t.Errorf("the command returned status %d and %+v, want %d and %+v", status, got, c.wantStatus, c.wantResult)
			}
			if (got.Thumbnail != "") != (c.wantStatus == 0) {
				t.Errorf("the line has a thumbnail: %v, want %v", got.Thumbnail != "", c.wantStatus == 0)
			}
		})
	}
}

func TestColourArgumentRoundTrips(t *testing.T) {
	for _, colour := range palette {
		got, err := parseColour(colourArgument(colour))
		if err != nil || got != colour {
			t.Errorf("parseColour(colourArgument(%v)) = %v, %v", colour, got, err)
		}
	}
	for _, bad := range []string{"", "b71c1c", "#b71c1", "#b71c1c00", "#zzzzzz"} {
		if _, err := parseColour(bad); err == nil {
			t.Errorf("parseColour(%q) returned no error, want one because the text is not #rrggbb", bad)
		}
	}
}

func TestBakeExitsWith2ForBadArguments(t *testing.T) {
	good := filepath.Join(t.TempDir(), "ada.png")
	if err := os.WriteFile(good, solid(t, 8, 8, green), 0o644); err != nil {
		t.Fatalf("writing ada.png: %v", err)
	}
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"a path and a colour", []string{good, "#b71c1c"}, 0},
		{"no colour", []string{good}, 2},
		{"a colour in another form", []string{good, "red"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			if got := runBake(&out, &stderr, c.args); got != c.want {
				t.Errorf("runBake(%q) = %d, want %d (it wrote %q)", c.args, got, c.want, stderr.String())
			}
		})
	}
}
