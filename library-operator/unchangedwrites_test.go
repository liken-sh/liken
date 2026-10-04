package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The second the tests set on a file before the second pass, so a pass that
// rewrites the file moves its modification time away from it.
var agedSecond = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

// Sets a file's modification time to agedSecond, as if it landed in an
// earlier pass.
func age(t *testing.T, path string) {
	t.Helper()
	if err := os.Chtimes(path, agedSecond, agedSecond); err != nil {
		t.Fatal(err)
	}
}

func modified(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime().UTC()
}

// One pass of a writer over one directory, which returns the file it writes.
type writePass func(t *testing.T, w *volumeWriter, dir string) string

// Every door that writes into the library tree, each run twice with the same
// input. A program that watches the tree, such as Jellyfin's library monitor,
// reads a rename or a new modification time as a change and refreshes the
// item, so a pass that has nothing new to say leaves the file as it is.
func TestASecondPassWithTheSameInputWritesNothing(t *testing.T) {
	tests := []struct {
		name string
		pass writePass
	}{
		{"a write", func(t *testing.T, w *volumeWriter, dir string) string {
			target := filepath.Join(dir, "movie.nfo")
			if err := w.write(target, []byte("<movie></movie>")); err != nil {
				t.Fatal(err)
			}
			return target
		}},
		{"an update", func(t *testing.T, w *volumeWriter, dir string) string {
			target := filepath.Join(dir, "contributor.yaml")
			_, err := w.update(target, func([]byte) ([]byte, error) { return []byte("name: A. Person\n"), nil })
			if err != nil {
				t.Fatal(err)
			}
			return target
		}},
		{"a ledger note", func(t *testing.T, w *volumeWriter, dir string) string {
			err := w.updateLikenLedger(dir, factTrickplay, func(ledger *likenLedger) {
				ledger.noteItem(likenItem{Path: "a.mkv", Reason: "existing"})
			})
			if err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, likenDirectory, likenLedgerName(factTrickplay))
		}},
		{"a work list", func(t *testing.T, w *volumeWriter, dir string) string {
			items := []workItem{{Path: "A/a.mkv", Size: 4, DurationMs: 1000, Listed: time.Now().UTC()}}
			if err := w.writeWorkList(dir, testWorkLibrary, factTrickplay, items); err != nil {
				t.Fatal(err)
			}
			return workListPath(dir, testWorkLibrary, factTrickplay)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writer := newVolumeWriter("movies-close")
			age(t, test.pass(t, writer, dir))

			target := test.pass(t, writer, dir)

			if got := modified(t, target); !got.Equal(agedSecond) {
				t.Errorf("the second pass moved the modification time to %v, want %v", got, agedSecond)
			}
			if left := namesIn(t, filepath.Dir(target)); len(left) != 1 {
				t.Errorf("the directory holds %v, want the file alone", left)
			}
		})
	}
}

// A list of the same videos keeps the time of the list that named them first.
// The worker passes over a video whose ledger holds an attempt at or after
// that time, so the earlier time still covers an attempt the catalog has not
// read yet.
func TestAWorkListOfTheSameVideosKeepsItsFirstTime(t *testing.T) {
	root := t.TempDir()
	writer := newVolumeWriter("movies-close")
	first := []workItem{{Path: "A/a.mkv", Size: 4, DurationMs: 1000, Listed: ledgerTime}}
	again := []workItem{{Path: "A/a.mkv", Size: 4, DurationMs: 1000, Listed: ledgerTime.Add(time.Hour)}}
	if err := writer.writeWorkList(root, testWorkLibrary, factTrickplay, first); err != nil {
		t.Fatal(err)
	}

	if err := writer.writeWorkList(root, testWorkLibrary, factTrickplay, again); err != nil {
		t.Fatal(err)
	}

	read, err := readWorkList(root, testWorkLibrary, factTrickplay)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(read, first) {
		t.Errorf("read %+v, want the first list %+v", read, first)
	}
}

// A list that names another video, another size, or another length is a new
// list, and it lands whole with its own time.
func TestAWorkListOfOtherVideosReplacesTheOneBefore(t *testing.T) {
	tests := []struct {
		name string
		next workItem
	}{
		{"another video", workItem{Path: "B/b.mkv", Size: 4, DurationMs: 1000}},
		{"another size", workItem{Path: "A/a.mkv", Size: 5, DurationMs: 1000}},
		{"another length", workItem{Path: "A/a.mkv", Size: 4, DurationMs: 2000}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writer := newVolumeWriter("movies-close")
			first := []workItem{{Path: "A/a.mkv", Size: 4, DurationMs: 1000, Listed: ledgerTime}}
			if err := writer.writeWorkList(root, testWorkLibrary, factTrickplay, first); err != nil {
				t.Fatal(err)
			}
			next := test.next
			next.Listed = ledgerTime.Add(time.Hour)

			if err := writer.writeWorkList(root, testWorkLibrary, factTrickplay, []workItem{next}); err != nil {
				t.Fatal(err)
			}

			read, err := readWorkList(root, testWorkLibrary, factTrickplay)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(read, []workItem{next}) {
				t.Errorf("read %+v, want the new list %+v", read, []workItem{next})
			}
		})
	}
}
