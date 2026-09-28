package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// A stand-in for ffmpeg on PATH that records the arguments it was called with,
// space separated, and writes no sheet.
func standInFFmpegRecordingArguments(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "arguments")
	writeFile(t, filepath.Join(dir, "ffmpeg"),
		"#!/bin/sh\nprintf '%s ' \"$@\" > \""+record+"\"\n")
	if err := os.Chmod(filepath.Join(dir, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

// The device directory of one case, with the named nodes in it, which the
// render node lookup reads in place of the machine's own.
func seedRenderNodes(t *testing.T, names ...string) string {
	t.Helper()
	devices := t.TempDir()
	for _, name := range names {
		writeFile(t, filepath.Join(devices, name), "")
	}
	held := renderNodeDirectory
	t.Cleanup(func() { renderNodeDirectory = held })
	renderNodeDirectory = devices
	return devices
}

// The render node is the first renderD entry of the device directory, and a
// directory with none answers with nothing.
func TestTheRenderNodeIsTheFirstOneTheMachineHolds(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		want  string
	}{
		{name: "one render node", nodes: []string{"renderD128"}, want: "renderD128"},
		{name: "two render nodes", nodes: []string{"renderD129", "renderD128"}, want: "renderD128"},
		{name: "a card and no render node", nodes: []string{"card0"}},
		{name: "an empty device directory"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			devices := seedRenderNodes(t, test.nodes...)

			got := strings.TrimPrefix(renderNode(), devices+string(os.PathSeparator))

			if got != test.want {
				t.Errorf("render node = %q, want %q", got, test.want)
			}
		})
	}
}

// A render node puts the VA-API decoder on the command line before the input,
// and the frames come back to software for the filter chain.
func TestTheDecodeAsksForTheRenderNode(t *testing.T) {
	record := standInFFmpegRecordingArguments(t)
	devices := seedRenderNodes(t, "renderD128")

	if err := ffmpegSheets(t.Context(), "One (2001).mkv", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	got := readFileString(t, record)
	want := "-hwaccel vaapi -hwaccel_device " + filepath.Join(devices, "renderD128") + " -i "
	if !strings.Contains(got, want) {
		t.Errorf("ffmpeg ran with %q, want it to hold %q", got, want)
	}
	if strings.Contains(got, "-hwaccel_output_format") {
		t.Errorf("ffmpeg ran with %q, want the frames downloaded for the filter chain", got)
	}
}

// A machine with no render node runs the software decode it always ran.
func TestTheDecodeStaysInSoftwareWithNoRenderNode(t *testing.T) {
	record := standInFFmpegRecordingArguments(t)
	seedRenderNodes(t)

	if err := ffmpegSheets(t.Context(), "One (2001).mkv", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, record); strings.Contains(got, "-hwaccel") {
		t.Errorf("ffmpeg ran with %q, want no hardware decoder", got)
	}
}

func TestTheLogNamesTheDecoder(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		want  string
	}{
		{name: "a render node", nodes: []string{"renderD128"}, want: "decoding on "},
		{name: "no render node", want: "decoding in software"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			seedRenderNodes(t, test.nodes...)

			if got := decoderName(); !strings.HasPrefix(got, test.want) {
				t.Errorf("decoder = %q, want a prefix of %q", got, test.want)
			}
		})
	}
}
