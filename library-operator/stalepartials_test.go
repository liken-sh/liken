package main

// What these tests prove: a partial file that a stopped writer left under a
// .liken directory goes, from the walk that finds it to the close container
// that removes it, and a fresh partial file and a file with no partial mark
// stay.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A file on the volume whose last write was age ago.
func agedFile(t *testing.T, path string, age time.Duration) {
	t.Helper()
	writeFile(t, path, "partial")
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

const (
	staleAge = stalePartialAge + time.Hour
	freshAge = time.Hour
)

// The partial files of one title folder, by the two marks: the operator's own
// temporary in .liken/ and the appearances tool's partial file in
// .liken/appearances/.
var (
	staleTemporary  = filepath.Join("Film (1999)", likenDirectory, "identity.yaml"+likenTempMark+"movies-enrich")
	staleToolRecord = filepath.Join("Film (1999)", likenDirectory, "appearances",
		"Film (1999).mkv.jsonl.partial-movies-appearances-a1-41")
)

func TestTheStalePartialRemoveTakesOnlyAnOldMarkedFileUnderLiken(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		age     time.Duration
		removed bool
	}{
		{name: "an old temporary of this operator", path: staleTemporary, age: staleAge, removed: true},
		{name: "an old partial file of the appearances tool", path: staleToolRecord, age: staleAge, removed: true},
		{name: "a fresh temporary", path: staleTemporary, age: freshAge},
		{name: "a fresh partial file", path: staleToolRecord, age: freshAge},
		{name: "an old ledger with no mark", path: filepath.Join("Film (1999)", likenDirectory, "identity.yaml"),
			age: staleAge},
		{name: "a partial mark with no process", path: filepath.Join("Film (1999)", likenDirectory, "notes.partial-draft"),
			age: staleAge},
		{name: "an old partial file beside the media", path: filepath.Join("Film (1999)", "Film.mkv.partial-a1-41"),
			age: staleAge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.path)
			agedFile(t, path, test.age)

			removed, err := newVolumeWriter("movies-enrich").removeStalePartial(path, time.Now())

			if removed != test.removed {
				t.Errorf("removed = %v (%v), want %v", removed, err, test.removed)
			}
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) != test.removed {
				t.Errorf("the file is there = %v, want %v", statErr == nil, !test.removed)
			}
		})
	}
}

// The walk reads a title folder's .liken directory and its appearances
// directory, and names each partial file there that no writer has changed for
// a day. A fresh partial file and a ledger stay off the list.
func TestTheWalkFindsTheStalePartialsOfATitle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Film (1999)", "Film (1999).mkv"), "video")
	agedFile(t, filepath.Join(root, staleTemporary), staleAge)
	agedFile(t, filepath.Join(root, staleToolRecord), staleAge)
	agedFile(t, filepath.Join(root, "Film (1999)", likenDirectory, "appearances",
		"Film (1999).mkv.jsonl.partial-movies-appearances-b2-7"), freshAge)
	agedFile(t, filepath.Join(root, "Film (1999)", likenDirectory, "identity.yaml"), staleAge)

	result := readFolder(folderScan{root: root, library: "house/movies", kind: libraryKindMovies},
		filepath.Join(root, "Film (1999)"))

	found := slices.Sorted(slices.Values(result.stalePartials))
	want := slices.Sorted(slices.Values([]string{
		filepath.Join(root, staleTemporary), filepath.Join(root, staleToolRecord)}))
	if !slices.Equal(found, want) {
		t.Errorf("stale partials = %v, want %v", found, want)
	}
}

// The scan mounts the volume read-only, so it hands the stale partial files
// it found to the close container on the phases volume, and the close
// container removes them and logs each one.
func TestTheCloseContainerRemovesTheStalePartialsTheScanFound(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	run, log := closingJob(t, catalog, scanPhase)
	writeFile(t, filepath.Join(run.root, "Film (1999)", "Film (1999).mkv"), "video")
	agedFile(t, filepath.Join(run.root, staleTemporary), staleAge)
	agedFile(t, filepath.Join(run.root, staleToolRecord), staleAge)
	fresh := filepath.Join(run.root, "Film (1999)", likenDirectory, "appearances",
		"Film (1999).mkv.jsonl.partial-movies-appearances-b2-7")
	agedFile(t, fresh, freshAge)

	scan, _, _ := scanJob(t, run.root, libraryKindMovies, "")
	scan.board = newPhaseBoard(run.board.dir)
	if err := scan.runPhase(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()
	confirmTheRun(t, catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	for _, gone := range []string{staleTemporary, staleToolRecord} {
		if _, err := os.Stat(filepath.Join(run.root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v), want it removed", gone, err)
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the fresh partial file is gone: %v", err)
	}
	if got := strings.Count(log.String(), "removed the stale partial file"); got != 2 {
		t.Errorf("log = %q, want one line for each of the two files", log)
	}
}
