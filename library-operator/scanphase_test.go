package main

// The tests of the scan phase of a library Job: the walk writes its runs row
// and its mark and leaves the hand-off to the close container, a failed walk
// fails its mark and not the container, and a walk of several folders
// rescans each one.

import (
	"context"
	"path/filepath"
	"testing"
)

// A scan in a library Job writes its finished row and its mark, and makes no
// hand-off of its own, so it ends with no confirmation.
func TestTheScanPhaseWritesItsMarkAndLeavesTheHandOff(t *testing.T) {
	scan, recorder, _ := scanJob(t, "testdata/movies", libraryKindMovies, "")
	scan.board = newPhaseBoard(t.TempDir())

	if err := scan.runPhase(t.Context()); err != nil {
		t.Fatal(err)
	}

	if end := scan.board.ended(scanPhase); end != (phaseEnd{ended: true}) {
		t.Errorf("mark = %+v, want a walk that finished", end)
	}
	runs := runsPosted(recorder)
	if len(runs) != 2 || runs[1].params[4] == float64(0) {
		t.Errorf("the scan posted %d runs, want the start and the finish and no write for a hand-off", len(runs))
	}
}

// A walk that fails writes a failed mark and exits zero, so the phases and
// the close container still run, and the scan row carries the failure.
func TestAFailedWalkWritesAFailedMark(t *testing.T) {
	scan, recorder, _ := scanJob(t, "testdata/movies", libraryKindMovies, "")
	scan.root = filepath.Join(t.TempDir(), "gone")
	scan.board = newPhaseBoard(t.TempDir())

	if err := scan.runPhase(t.Context()); err != nil {
		t.Fatalf("runPhase = %v, want the failure in the mark", err)
	}

	if end := scan.board.ended(scanPhase); !end.ended || end.failure == "" {
		t.Errorf("mark = %+v, want a failed mark", end)
	}
	runs := runsPosted(recorder)
	if failure := runs[len(runs)-1].params[7]; failure == "" {
		t.Error("the finished run carries no failure")
	}
}

// A scan stopped mid-walk writes no mark, so the kubelet's verdict on the
// container stands.
func TestAStoppedScanWritesNoMark(t *testing.T) {
	scan, _, _ := scanJob(t, "testdata/movies", libraryKindMovies, "")
	scan.board = newPhaseBoard(t.TempDir())
	ctx, stop := context.WithCancel(t.Context())
	stop()

	if err := scan.runPhase(ctx); err == nil {
		t.Error("runPhase = nil error on a stopped container")
	}
	if _, marked := scan.board.mark(scanPhase); marked {
		t.Error("a stopped scan wrote a mark")
	}
}

// A scan with no phases volume to lock on fails before it walks.
func TestAScanThatCannotTakeItsLockFails(t *testing.T) {
	scan, recorder, _ := scanJob(t, "testdata/movies", libraryKindMovies, "")
	scan.board = newPhaseBoard(filepath.Join(t.TempDir(), "absent"))

	if err := scan.runPhase(t.Context()); err == nil {
		t.Error("runPhase = nil error with no phases volume")
	}
	if len(recorder.all()) != 0 {
		t.Error("the scan wrote to the catalog with no lock")
	}
}

// A walk of several folders rescans each one, and a folder that maps onto
// nothing makes it a walk of the whole root.
func TestAWalkOfSeveralFoldersRescansEach(t *testing.T) {
	cases := []struct {
		name   string
		paths  []string
		titles int
		wrote  []string
	}{
		{name: "two folders", paths: []string{webhookFolderPath, "Action/The Matrix (1999)"},
			wrote: []string{"movie:path:the-thing-1982-1080p-bluray-x264-group", "movie:path:the-matrix-1999"}},
		{name: "a folder the volume does not hold", paths: []string{webhookFolderPath, "/nothing/here"}, titles: 3},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			scan, recorder, _ := scanJob(t, "testdata/movies", libraryKindMovies, "")
			scan.scanPaths = one.paths
			scan.board = newPhaseBoard(t.TempDir())

			if err := scan.runPhase(t.Context()); err != nil {
				t.Fatal(err)
			}

			if scan.report.Titles != one.titles {
				t.Errorf("the walk read %d titles, want %d", scan.report.Titles, one.titles)
			}
			for _, id := range one.wrote {
				if !postedWith(recorder, id) {
					t.Errorf("the walk wrote no row for %s", id)
				}
			}
			if scan.worker() != workerRescan {
				t.Errorf("worker = %s, want the rescan worker", scan.worker())
			}
		})
	}
}

// The folders reach the scanner as the JSON list, or as the one path a Job
// of one folder names, and a list this image cannot read is the full walk.
func TestTheScannerReadsItsFolders(t *testing.T) {
	cases := []struct {
		name, list, single string
		want               int
	}{
		{name: "a list", list: `["/a","/b"]`, want: 2},
		{name: "one path", single: "/a", want: 1},
		{name: "a list that wins over the path", list: `["/a"]`, single: "/b", want: 1},
		{name: "a list this image cannot read", list: "[", want: 0},
		{name: "nothing"},
	}
	for _, one := range cases {
		if got := scanPathsOf(one.list, one.single); len(got) != one.want {
			t.Errorf("%s: scanPathsOf = %v, want %d folders", one.name, got, one.want)
		}
	}
}
