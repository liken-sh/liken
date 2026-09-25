package main

// The tests of the phase marks: what a waiter reads for a phase that has not
// started, one that runs, one that finished, one that failed, and one whose
// process ended with no mark, and the watch that wakes a waiter. They also
// cover the lock that keeps two edits of one .nfo file apart.

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The phases volume of one test pod.
func testBoard(t *testing.T) *phaseBoard {
	t.Helper()
	return newPhaseBoard(t.TempDir())
}

// Each state a phase can be in reads as the waiter needs it.
func TestAPhaseEndsOnlyWithAMarkOrADeadProcess(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, board *phaseBoard)
		want  phaseEnd
	}{
		{name: "a phase that has not started", setup: func(*testing.T, *phaseBoard) {}},
		{name: "a phase that runs", setup: func(t *testing.T, board *phaseBoard) {
			if err := board.start("probe"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a phase that finished", want: phaseEnd{ended: true}, setup: func(t *testing.T, board *phaseBoard) {
			if err := board.start("probe"); err != nil {
				t.Fatal(err)
			}
			if err := board.finish("probe", nil); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a phase that failed", want: phaseEnd{ended: true, failure: "no provider answered"},
			setup: func(t *testing.T, board *phaseBoard) {
				if err := board.start("probe"); err != nil {
					t.Fatal(err)
				}
				if err := board.finish("probe", errors.New("no provider answered")); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "a phase whose process ended with no mark",
			want: phaseEnd{ended: true, failure: "the probe container ended and wrote no mark"},
			setup: func(t *testing.T, board *phaseBoard) {
				if err := board.start("probe"); err != nil {
					t.Fatal(err)
				}
				// Closing the file releases the lock, as the kernel does
				// for a process it kills.
				if err := board.running.Close(); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "a failed mark with no text", want: phaseEnd{ended: true, failure: "the probe phase failed"},
			setup: func(t *testing.T, board *phaseBoard) {
				if err := os.WriteFile(board.path("probe", phaseFailedSuffix), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			board := testBoard(t)
			one.setup(t, board)

			// A second board is a second container of the same pod.
			if got := newPhaseBoard(board.dir).ended("probe"); got != one.want {
				t.Errorf("ended = %+v, want %+v", got, one.want)
			}
		})
	}
}

// A list has ended when every phase in it has, and it names each failure.
func TestAllEndedNamesTheFailures(t *testing.T) {
	board := testBoard(t)
	for _, phase := range []string{"probe", "identity", "art"} {
		if err := newPhaseBoard(board.dir).start(phase); err != nil {
			t.Fatal(err)
		}
	}
	probe, identity := newPhaseBoard(board.dir), newPhaseBoard(board.dir)
	if err := probe.finish("probe", nil); err != nil {
		t.Fatal(err)
	}
	if err := identity.finish("identity", errors.New("the key was refused")); err != nil {
		t.Fatal(err)
	}

	if all, _ := board.allEnded([]string{"probe", "identity", "art"}); all {
		t.Error("allEnded = true while art runs")
	}
	all, failures := board.allEnded([]string{"probe", "identity"})
	if !all || len(failures) != 1 || failures[0] != "the key was refused" {
		t.Errorf("allEnded = %v, %v, want true and the identity failure", all, failures)
	}
}

// The watch wakes on a mark, and on a running file whose process ended.
func TestTheWatchWakesOnAMarkAndOnAnEndedProcess(t *testing.T) {
	cases := []struct {
		name string
		act  func(t *testing.T, board *phaseBoard)
	}{
		{name: "a mark", act: func(t *testing.T, board *phaseBoard) {
			if err := board.finish("probe", nil); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "an ended process", act: func(t *testing.T, board *phaseBoard) {
			if err := board.running.Close(); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			board := testBoard(t)
			if err := board.start("probe"); err != nil {
				t.Fatal(err)
			}
			changed, err := newPhaseBoard(board.dir).watch(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			one.act(t, board)

			select {
			case <-changed:
			case <-time.After(5 * time.Second):
				t.Fatal("the watch reported no change")
			}
		})
	}
}

// A directory the kernel cannot watch is an error, and the watch starts no
// reader.
func TestTheWatchReportsADirectoryItCannotWatch(t *testing.T) {
	board := newPhaseBoard(filepath.Join(t.TempDir(), "absent"))
	if _, err := board.watch(t.Context()); err == nil {
		t.Error("watch = nil error on a missing directory")
	}
}

// A running file or a mark the volume refuses to take is an error.
func TestTheBoardReportsAVolumeItCannotWrite(t *testing.T) {
	board := newPhaseBoard(filepath.Join(t.TempDir(), "absent"))
	if err := board.start("probe"); err == nil {
		t.Error("start = nil error on a missing directory")
	}
	if err := board.finish("probe", nil); err == nil {
		t.Error("finish = nil error on a missing directory")
	}
}

// Two edits of one .nfo file never hold the lock at once, and an edit of
// another file takes a lock of its own.
func TestTheNFOLockHoldsOneEditAtATime(t *testing.T) {
	locks := t.TempDir()
	release, err := lockNFO(locks, "/library/Arrival (2016)/movie.nfo")
	if err != nil {
		t.Fatal(err)
	}
	other, err := lockNFO(locks, "/library/Heat (1995)/movie.nfo")
	if err != nil {
		t.Fatal(err)
	}
	other()

	path := filepath.Join(locks, nfoLocksDir, nfoLockName("/library/Arrival (2016)/movie.nfo"))
	if !heldElsewhere(t, path) {
		t.Error("a second holder took the lock while the first edit held it")
	}
	release()
	if heldElsewhere(t, path) {
		t.Error("the lock stayed taken after the edit released it")
	}
}

// Whether another open file description holds the lock at path.
func heldElsewhere(t *testing.T, path string) bool {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	return false
}

// A writer with no phases volume edits with no lock, which is how a test
// and a local run edit a .nfo file.
func TestAnEditWithNoPhasesVolumeTakesNoLock(t *testing.T) {
	release, err := lockNFO("", "/library/Arrival (2016)/movie.nfo")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// A locks directory the volume refuses is an error.
func TestTheNFOLockReportsAVolumeItCannotWrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lockNFO(file, "/library/Arrival (2016)/movie.nfo"); err == nil {
		t.Error("lockNFO = nil error under a file")
	}
}

// The environment names the phases a container waits for, and an empty
// value names none.
func TestPhaseNeedsReadsTheList(t *testing.T) {
	if got := phaseNeeds("scan, identity"); len(got) != 2 || got[0] != "scan" || got[1] != "identity" {
		t.Errorf("phaseNeeds = %v, want scan and identity", got)
	}
	if got := phaseNeeds(""); len(got) != 0 {
		t.Errorf("phaseNeeds = %v, want none", got)
	}
}
