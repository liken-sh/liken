package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// The folder Jellyfin's own layout puts the sheets in, and which files of a run
// are its sheets.

func TestTheTilesFolderStatesTheWidthAndTheGrid(t *testing.T) {
	if got := trickplayTilesFolder(); got != "320 - 10x10" {
		t.Errorf("folder = %q, want the width and the grid Jellyfin writes", got)
	}
}

func TestTheTrickplayDirectorySitsBesideTheVideo(t *testing.T) {
	got := trickplayDirectory("/media/One (2001)/One (2001).mkv")
	if got != "/media/One (2001)/One (2001).trickplay" {
		t.Errorf("directory = %q, want the file's own name with the extension replaced", got)
	}
}

// The numbered files are the sheets, in the order ffmpeg wrote them. A name
// that is not a number, and a directory, are neither.
func TestTheSheetsOfARunAreItsNumberedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"0.jpg", "2.jpg", "10.jpg", "cover.jpg", "notes.txt"} {
		writeFile(t, filepath.Join(dir, name), "sheet")
	}
	writeFile(t, filepath.Join(dir, "0.jpg.d", "inside"), "not a sheet")

	sheets, err := sheetsIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sheets, []string{"0.jpg", "2.jpg", "10.jpg"}) {
		t.Errorf("sheets = %v, want the numbered files in number order", sheets)
	}
	if _, err := sheetsIn(filepath.Join(dir, "gone")); err == nil {
		t.Error("a directory that is not there read as no sheets, want an error")
	}
}
