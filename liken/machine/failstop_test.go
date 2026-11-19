package machine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFailStopRoundTrips(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 7, 27, 4, 15, 0, 0, time.UTC)
	want := FailStop{Reason: "machine identity: bad cluster document", Time: at}

	if err := WriteFailStop(root, want); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	got, err := ReadFailStop(root)
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if got == nil {
		t.Fatal("ReadFailStop returned nil after a write, want the record")
	}
	if got.Reason != want.Reason {
		t.Errorf("the reason is %q, want %q", got.Reason, want.Reason)
	}
	if !got.Time.Equal(want.Time) {
		t.Errorf("the time is %s, want %s", got.Time, want.Time)
	}
}

func TestFailStopOverwrites(t *testing.T) {
	root := t.TempDir()
	first := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	second := first.Add(24 * time.Hour)
	if err := WriteFailStop(root, FailStop{Reason: "storage", Time: first}); err != nil {
		t.Fatalf("writing the first record: %v", err)
	}
	if err := WriteFailStop(root, FailStop{Reason: "identity", Time: second}); err != nil {
		t.Fatalf("writing the second record: %v", err)
	}
	got, err := ReadFailStop(root)
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if got.Reason != "identity" || !got.Time.Equal(second) {
		t.Errorf("the record is %+v, want only the second refusal", got)
	}
}

func TestReadFailStopOnAMachineThatNeverRefused(t *testing.T) {
	got, err := ReadFailStop(t.TempDir())
	if err != nil {
		t.Fatalf("ReadFailStop of an absent record returned the error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("ReadFailStop of an absent record returned %+v, want nil", got)
	}
}

func TestReadFailStopSeparatesAbsentFromUnreadable(t *testing.T) {
	// A directory in the record's place cannot be read. That must return
	// an error, not the nil of a machine that never refused a boot.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, failStopRecord), 0o755); err != nil {
		t.Fatalf("creating a directory in place of the record: %v", err)
	}
	if _, err := ReadFailStop(root); err == nil {
		t.Error("ReadFailStop of an unreadable record returned no error, want one")
	}
}

func TestReadFailStopTreatsAReasonlessRecordAsNoRefusal(t *testing.T) {
	// An empty file is what a write that never reached the disk leaves.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, failStopRecord), nil, 0o644); err != nil {
		t.Fatalf("writing the empty record: %v", err)
	}
	got, err := ReadFailStop(root)
	if err != nil {
		t.Fatalf("ReadFailStop of an empty record returned the error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("ReadFailStop of an empty record returned %+v, want nil", got)
	}
}

func TestReadFailStopReportsATornRecord(t *testing.T) {
	// A partly written record that does not parse returns an error. A
	// record that parses but has no reason reads as no refusal.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, failStopRecord), []byte("time: half-a-t"), 0o644); err != nil {
		t.Fatalf("writing the partial record: %v", err)
	}
	if _, err := ReadFailStop(root); err == nil {
		t.Error("ReadFailStop of a record that does not parse returned no error, want one")
	}
}

func TestWriteFailStopReportsARootItCannotCreate(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatalf("writing the file that blocks the root: %v", err)
	}
	err := WriteFailStop(filepath.Join(blocked, "machine"), FailStop{Reason: "storage", Time: time.Now()})
	if err == nil {
		t.Error("WriteFailStop under a file returned no error, want one")
	}
}
