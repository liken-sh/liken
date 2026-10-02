package main

import (
	"errors"
	"path/filepath"
	"testing"
)

// The read-change-write door lands the change on the file it read, applies
// the change again where another writer changed the file in between, and
// gives up where the file never holds still.

// A change that adds one line to whatever the file holds.
func appendLine(line string) func([]byte) ([]byte, error) {
	return func(current []byte) ([]byte, error) {
		return append(append([]byte{}, current...), line+"\n"...), nil
	}
}

func TestAnUpdateLandsTheChangeOnTheFileItRead(t *testing.T) {
	target := filepath.Join(t.TempDir(), "probe.yaml")
	writeFile(t, target, "one\n")

	landed, err := newVolumeWriter("movies-enrich").update(target, appendLine("two"))

	if err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, target); got != "one\ntwo\n" || string(landed) != got {
		t.Errorf("the file holds %q and the door answered %q, want both lines", got, landed)
	}
	if left := namesIn(t, filepath.Dir(target)); len(left) != 1 {
		t.Errorf("the directory holds %v, want the target alone", left)
	}
}

// Another cluster writes the file while this writer works, so the door reads
// the file again before the rename and applies its change to what the other
// writer left. Both changes are in the file.
func TestAnUpdateKeepsTheChangeAnotherWriterLandedFirst(t *testing.T) {
	target := filepath.Join(t.TempDir(), "probe.yaml")
	writeFile(t, target, "one\n")
	raced := false
	change := func(current []byte) ([]byte, error) {
		if !raced {
			raced = true
			writeFile(t, target, "one\nother\n")
		}
		return appendLine("mine")(current)
	}

	if _, err := newVolumeWriter("movies-enrich").update(target, change); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, target); got != "one\nother\nmine\n" {
		t.Errorf("the file holds %q, want the other writer's line and this one's", got)
	}
}

// A change that has nothing to add leaves the file as it is, and the door
// answers what the file holds.
func TestAnUpdateWithNothingToChangeWritesNothing(t *testing.T) {
	target := filepath.Join(t.TempDir(), "probe.yaml")
	writeFile(t, target, "one\n")

	landed, err := newVolumeWriter("movies-enrich").update(target, func([]byte) ([]byte, error) {
		return nil, nil
	})

	if err != nil || string(landed) != "one\n" {
		t.Fatalf("update = %q, %v, want the file's own bytes", landed, err)
	}
}

// A file that is not there reads as nothing, and the change creates it.
func TestAnUpdateCreatesAFileThatIsNotThere(t *testing.T) {
	target := filepath.Join(t.TempDir(), "probe.yaml")

	if _, err := newVolumeWriter("movies-enrich").update(target, appendLine("one")); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, target); got != "one\n" {
		t.Errorf("the file holds %q, want the one line", got)
	}
}

// A file another writer changes on every try is an error and not a write,
// so the change of the last try never replaces a file it was not made from.
func TestAnUpdateGivesUpOnAFileThatNeverHoldsStill(t *testing.T) {
	target := filepath.Join(t.TempDir(), "probe.yaml")
	writeFile(t, target, "start\n")
	tries := 0
	change := func(current []byte) ([]byte, error) {
		tries++
		writeFile(t, target, string(current)+"other\n")
		return appendLine("mine")(current)
	}

	_, err := newVolumeWriter("movies-enrich").update(target, change)

	if !errors.Is(err, errUpdateRaced) {
		t.Fatalf("update = %v, want the door to give up", err)
	}
	if tries != updateTries {
		t.Errorf("the change ran %d times, want %d", tries, updateTries)
	}
	if left := namesIn(t, filepath.Dir(target)); len(left) != 1 {
		t.Errorf("the directory holds %v, want no temporary left", left)
	}
}
