package main

// These tests cover how an item names a timeline: a file the shim wrote, or an
// edl:// URL where a semicolon stands in for the newline.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/media-operator/edl"
)

// mpv reads a timeline from a file or from an edl:// URL, where a semicolon
// stands in for the newline. Both reach the same segments.
func TestAlbumTimeline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "album.edl")
	mustSucceed(t, os.WriteFile(path, []byte(edl.Header+"\none.mp3,title=Oh No\n"), 0o644))

	t.Run("a file", func(t *testing.T) {
		text, from, ok := albumTimeline(path)
		mustMatch(t, ok, true)
		mustMatch(t, from, dir)
		mustMatch(t, len(edl.Parse(text)), 1)
	})

	t.Run("an inline URL", func(t *testing.T) {
		text, from, ok := albumTimeline("edl://one.mp3,title=Oh No;two.mp3,title=Masterswarm")
		mustMatch(t, ok, true)
		mustMatch(t, from, "")
		mustMatch(t, len(edl.Parse(text)), 2)
	})

	t.Run("a plain media file", func(t *testing.T) {
		_, _, ok := albumTimeline(filepath.Join(dir, "one.mp3"))
		mustMatch(t, ok, false)
	})
}

// A file that ends in .edl but opens with something else is no timeline, so
// the bridge reads it as the media file it names.
func TestAlbumTimelineRefusesAFileWithNoHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "album.edl")
	mustSucceed(t, os.WriteFile(path, []byte("one.mp3,title=Oh No\n"), 0o644))

	_, _, ok := albumTimeline(path)
	mustMatch(t, ok, false)
}
