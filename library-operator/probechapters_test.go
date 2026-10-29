package main

// probechapters_test.go proves that the probe records a file's chapters, and
// that the walk reads the start of the chapter that names the credits into
// the file's row.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ffprobe's answer about a one-minute file with four chapters, which ffmpeg
// wrote from a chapter list for this test.
var ffprobeOfAFileWithChapters = ffprobeCapture("video-chapters.json")

func TestTheRecordHoldsEveryChapterOfAFile(t *testing.T) {
	record := ffprobeAnswerOf(t, ffprobeOfAFileWithChapters).probedFile()

	want := []probedChapter{
		{Start: 0, End: 4, Title: "Opening"},
		{Start: 4, End: 40, Title: "Chapter 2"},
		{Start: 40, End: 55, Title: "End Credits"},
		{Start: 55, End: 60, Title: "Next Time"},
	}
	if !reflect.DeepEqual(record.Chapters, want) {
		t.Errorf("chapters = %+v, want %+v", record.Chapters, want)
	}
}

// The probe asks ffprobe for the chapters, because the container states them
// only when it is asked.
func TestTheCommandAsksForTheChapters(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ffprobe"), "#!/bin/sh\necho \"$*\"\n")
	if err := os.Chmod(filepath.Join(dir, "ffprobe"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	output, err := ffprobeFile(t.Context(), filepath.Join(dir, "a.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "-show_chapters") {
		t.Errorf("arguments = %q, want -show_chapters among them", output)
	}
}

// One chapter list of a 3,000-second file.
func chaptersOf(chapters ...probedChapter) probedFile {
	return probedFile{Duration: 3000, Chapters: chapters}
}

func TestTheCreditsChapterIsTheEarliestThatNamesTheCreditsInTheSecondHalf(t *testing.T) {
	cases := []struct {
		name   string
		record probedFile
		want   int64
	}{
		{name: "end credits", record: chaptersOf(
			probedChapter{Start: 0, End: 2850, Title: "Chapter 1"},
			probedChapter{Start: 2850, End: 3000, Title: "End Credits"}), want: 2850000},
		{name: "a title in capitals", record: chaptersOf(
			probedChapter{Start: 2900.5, End: 3000, Title: "CREDITS"}), want: 2900500},
		{name: "closing credits", record: chaptersOf(
			probedChapter{Start: 2800, End: 3000, Title: "Closing Credits"}), want: 2800000},
		{name: "an outro", record: chaptersOf(
			probedChapter{Start: 2950, End: 3000, Title: "Outro"}), want: 2950000},
		{name: "end titles", record: chaptersOf(
			probedChapter{Start: 2700, End: 3000, Title: "End Titles"}), want: 2700000},
		{name: "the earliest of two", record: chaptersOf(
			probedChapter{Start: 2900, End: 2950, Title: "Credits"},
			probedChapter{Start: 2950, End: 3000, Title: "Outro"}), want: 2900000},
		{name: "opening credits in the first half", record: chaptersOf(
			probedChapter{Start: 60, End: 120, Title: "Opening Credits"},
			probedChapter{Start: 2900, End: 3000, Title: "End Credits"}), want: 2900000},
		{name: "a first-half chapter alone", record: chaptersOf(
			probedChapter{Start: 60, End: 120, Title: "Opening Credits"}), want: 0},
		{name: "a chapter that names no credits", record: chaptersOf(
			probedChapter{Start: 2900, End: 3000, Title: "Chapter 12"}), want: 0},
		{name: "no chapters", record: chaptersOf(), want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.record.creditsChapter(); got != test.want {
				t.Errorf("creditsChapter = %d, want %d", got, test.want)
			}
		})
	}
}

func TestTheIntroChapterRunsToTheNextChapterInTheFirstHalf(t *testing.T) {
	cases := []struct {
		name   string
		record probedFile
		want   timeSpan
	}{
		{name: "an intro", record: chaptersOf(
			probedChapter{Start: 0, End: 60, Title: "Cold Open"},
			probedChapter{Start: 60, End: 100, Title: "Intro"},
			probedChapter{Start: 105, End: 3000, Title: "Part One"}), want: timeSpan{Start: 60000, End: 105000}},
		{name: "opening credits", record: chaptersOf(
			probedChapter{Start: 0, End: 90, Title: "Opening Credits"},
			probedChapter{Start: 90, End: 3000, Title: "Story"}), want: timeSpan{Start: 0, End: 90000}},
		{name: "main titles", record: chaptersOf(
			probedChapter{Start: 30, End: 75, Title: "Main Titles"},
			probedChapter{Start: 75, End: 3000, Title: "Story"}), want: timeSpan{Start: 30000, End: 75000}},
		{name: "a title sequence", record: chaptersOf(
			probedChapter{Start: 40, End: 80, Title: "Title Sequence"},
			probedChapter{Start: 80, End: 3000, Title: "Story"}), want: timeSpan{Start: 40000, End: 80000}},
		{name: "a theme that is the last chapter", record: chaptersOf(
			probedChapter{Start: 0, End: 50, Title: "THEME"}), want: timeSpan{Start: 0, End: 50000}},
		{name: "an opening chapter in the second half", record: chaptersOf(
			probedChapter{Start: 0, End: 1600, Title: "Story"},
			probedChapter{Start: 1600, End: 1700, Title: "Opening of the Vault"},
			probedChapter{Start: 1700, End: 3000, Title: "Story"}), want: timeSpan{}},
		{name: "a chapter that names no intro", record: chaptersOf(
			probedChapter{Start: 0, End: 60, Title: "Chapter 1"}), want: timeSpan{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.record.introChapter(); got != test.want {
				t.Errorf("introChapter = %+v, want %+v", got, test.want)
			}
		})
	}
}

// The walk reads the credits chapter of a fresh record into the file's row,
// where the close container reads it.
func TestAFreshProbeFillsTheCreditsChapter(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Long Survey (1982)")
	video := filepath.Join(folder, "The Long Survey (1982).mkv")
	writeFile(t, video, "video")
	writeProbeLedger(t, folder, probedFile{
		Path: "The Long Survey (1982).mkv", Modified: modifiedOf(t, video), Container: "mkv", Duration: 6000,
		Chapters: []probedChapter{
			{Start: 0, End: 120, Title: "Opening Titles"},
			{Start: 120, End: 5600, Title: "Story"},
			{Start: 5600, End: 6000, Title: "Credits"},
		},
	})

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	row := fileRowAt(t, result, "The Long Survey (1982)/The Long Survey (1982).mkv")
	if row.CreditsChapterMs != 5600000 {
		t.Errorf("credits chapter = %d, want 5600000", row.CreditsChapterMs)
	}
	if row.IntroChapterStartMs != 0 || row.IntroChapterEndMs != 120000 {
		t.Errorf("intro chapter = %d to %d, want 0 to 120000", row.IntroChapterStartMs, row.IntroChapterEndMs)
	}
}

// The probe writes the credits chapter with the columns it owns, so the close
// container of the same Job reads it without waiting for a walk.
func TestTheProbeWritesTheCreditsChapterColumn(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Long Survey (1982)", "The Long Survey (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfAFileWithChapters)); err != nil {
		t.Fatal(err)
	}

	chapter, err := catalog.queryInt(t.Context(),
		`SELECT credits_chapter_ms FROM files WHERE library = ? AND path = ?`,
		[]any{"house/movies", filepath.Join("The Long Survey (1982)", "The Long Survey (1982).mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if chapter != 40000 {
		t.Errorf("credits chapter = %d, want the start of End Credits", chapter)
	}
}
