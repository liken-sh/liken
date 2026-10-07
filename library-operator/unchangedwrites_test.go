package main

import (
	"os"
	"path/filepath"
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
