package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// what these tests read: the folder Jellyfin's own layout puts the sheets in,
// how many thumbnails a title of one length needs, and the WebVTT that maps a
// time range onto a region of one sheet.

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

func TestHowManyThumbnailsCoverATitle(t *testing.T) {
	cases := []struct {
		name     string
		duration time.Duration
		sheets   int
		want     int
	}{
		{name: "a whole number of intervals", duration: 100 * time.Second, sheets: 1, want: 10},
		{name: "a part of an interval left over", duration: 105 * time.Second, sheets: 2, want: 11},
		{name: "shorter than one interval", duration: 4 * time.Second, sheets: 1, want: 1},
		{name: "more thumbnails than ffmpeg wrote sheets for",
			duration: 3000 * time.Second, sheets: 1, want: trickplayTilesPerSheet},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := trickplayTiles(test.duration, test.sheets); got != test.want {
				t.Errorf("thumbnails = %d, want %d", got, test.want)
			}
		})
	}
}

func TestAWebVTTTimestampPadsEveryField(t *testing.T) {
	cases := []struct {
		at   time.Duration
		want string
	}{
		{at: 0, want: "00:00:00.000"},
		{at: 90 * time.Second, want: "00:01:30.000"},
		{at: 3661500 * time.Millisecond, want: "01:01:01.500"},
	}
	for _, test := range cases {
		t.Run(test.want, func(t *testing.T) {
			if got := vttTimestamp(test.at); got != test.want {
				t.Errorf("timestamp = %q, want %q", got, test.want)
			}
		})
	}
}

// The first cue names the top left of the first sheet, the eleventh wraps
// onto the second row, and the last one ends where the title ends.
func TestTheMapNamesTheRegionOfEachThumbnail(t *testing.T) {
	index := string(trickplayVTT(12, 320, 180, 115*time.Second))

	if !strings.HasPrefix(index, "WEBVTT\n") {
		t.Fatalf("the map starts %q, want the WEBVTT header", index[:min(len(index), 10)])
	}
	for _, want := range []string{
		"00:00:00.000 --> 00:00:10.000\n0.jpg#xywh=0,0,320,180\n",
		"00:00:10.000 --> 00:00:20.000\n0.jpg#xywh=320,0,320,180\n",
		"00:01:40.000 --> 00:01:50.000\n0.jpg#xywh=0,180,320,180\n",
		"00:01:50.000 --> 00:01:55.000\n0.jpg#xywh=320,180,320,180\n",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("the map holds no cue %q", want)
		}
	}
}

// A title longer than one sheet moves onto the next sheet at the hundredth
// thumbnail, and the region starts again at the top left.
func TestTheMapMovesOntoTheNextSheet(t *testing.T) {
	index := string(trickplayVTT(102, 320, 180, 1020*time.Second))

	want := "00:16:40.000 --> 00:16:50.000\n1.jpg#xywh=0,0,320,180\n"
	if !strings.Contains(index, want) {
		t.Errorf("the map holds no cue %q", want)
	}
	if got := strings.Count(index, "#xywh="); got != 102 {
		t.Errorf("cues = %d, want one per thumbnail", got)
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

// The tile size comes off the first sheet's own header, and a sheet that is
// no image is an error rather than a guess.
func TestTheTileSizeComesOffTheFirstSheetsHeader(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "0.jpg")
	writeFile(t, broken, "this is not a JPEG")

	cases := []struct {
		name  string
		sheet string
	}{
		{name: "a sheet that is not there", sheet: filepath.Join(dir, "gone.jpg")},
		{name: "a sheet that is no image", sheet: broken},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := tileSize(test.sheet); err == nil {
				t.Error("the size read, want an error")
			}
		})
	}
}
