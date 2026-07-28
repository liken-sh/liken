package machine

// Tests for the fail-stop record. The record exists to survive a
// power-off, so what matters is that a write lands, that a later write
// replaces it, and that a root with no record reads as no record
// rather than as an error.

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
		t.Fatal(err)
	}
	got, err := ReadFailStop(root)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("the record read back as nothing")
	}
	if got.Reason != want.Reason {
		t.Errorf("reason: want %q, got %q", want.Reason, got.Reason)
	}
	if !got.Time.Equal(want.Time) {
		t.Errorf("time: want %s, got %s", want.Time, got.Time)
	}
}

func TestFailStopOverwrites(t *testing.T) {
	// The record answers one question, the last refusal, so a second
	// refusal replaces the first rather than joining it.
	root := t.TempDir()
	first := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	second := first.Add(24 * time.Hour)
	if err := WriteFailStop(root, FailStop{Reason: "storage", Time: first}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFailStop(root, FailStop{Reason: "identity", Time: second}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFailStop(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reason != "identity" || !got.Time.Equal(second) {
		t.Errorf("the second refusal should stand alone: %+v", got)
	}
}

func TestReadFailStopOnAMachineThatNeverRefused(t *testing.T) {
	// Most machines never refuse a boot, so an absent record is the
	// ordinary answer and not a fault to report.
	got, err := ReadFailStop(t.TempDir())
	if err != nil {
		t.Fatalf("an absent record is not an error: %v", err)
	}
	if got != nil {
		t.Errorf("an absent record must read as nothing: %+v", got)
	}
}

func TestReadFailStopSeparatesAbsentFromUnreadable(t *testing.T) {
	// Absent means the machine never refused a boot. Unreadable means
	// the record is there and this code cannot have it, which is a
	// different answer and must not read as the reassuring one.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, failStopRecord), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFailStop(root); err == nil {
		t.Error("a record that cannot be read must report the failure")
	}
}

func TestReadFailStopReportsATornRecord(t *testing.T) {
	// The record is written durably to survive the power-off that
	// follows it, and a torn write is still possible on hardware that
	// lies about its flushes. A record that does not parse is reported,
	// never reported as no refusal at all.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, failStopRecord), []byte("time: half-a-t"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFailStop(root); err == nil {
		t.Error("a record that does not parse must report the failure")
	}
}

func TestWriteFailStopReportsARootItCannotCreate(t *testing.T) {
	// machineState is mounted before failBoot records anything, so this
	// is the case where the mount is gone from under the write. The
	// refusal is lost either way, and saying so beats a silent success.
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteFailStop(filepath.Join(blocked, "machine"), FailStop{Reason: "storage", Time: time.Now()})
	if err == nil {
		t.Error("a root that cannot exist must report the failure")
	}
}
