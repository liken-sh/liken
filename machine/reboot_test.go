package machine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRebootIntentRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := &RebootIntent{
		Reason:       "applying the staged spec",
		ManifestHash: "abc123",
		RequestedAt:  time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
	}
	if err := WriteRebootIntent(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRebootIntent(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Reason != want.Reason || got.ManifestHash != want.ManifestHash || !got.RequestedAt.Equal(want.RequestedAt) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReadRebootIntentAbsentMeansNoRequest(t *testing.T) {
	intent, err := ReadRebootIntent(t.TempDir())
	if intent != nil || err != nil {
		t.Errorf("no file should mean nil, nil: %v %v", intent, err)
	}
}

func TestReadRebootIntentRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reboot-intent.yaml"), []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRebootIntent(dir); err == nil {
		t.Error("expected an error for an unparseable intent")
	}
}

func TestWriteRebootIntentNeedsItsDirectory(t *testing.T) {
	// The channel directory is init's to create; writing into a
	// missing one is an error the operator should report rather than
	// hide.
	err := WriteRebootIntent(filepath.Join(t.TempDir(), "absent"), &RebootIntent{Reason: "test"})
	if err == nil {
		t.Error("expected an error for a missing channel directory")
	}
}

func TestRebootPolicyDefaultsToManual(t *testing.T) {
	cases := map[RebootPolicy]RebootPolicy{
		"":           RebootManual,
		"Manual":     RebootManual,
		"Auto":       RebootAuto,
		"aggressive": RebootManual, // an unrecognized policy must never reboot automatically
	}
	for in, want := range cases {
		spec := MachineSpec{RebootPolicy: in}
		if got := spec.RebootPolicyOrDefault(); got != want {
			t.Errorf("RebootPolicyOrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRestartIntentRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := &RestartIntent{
		Reason:      "applying the staged cluster document",
		RequestedAt: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
	}
	if err := WriteRestartIntent(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRestartIntent(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Reason != want.Reason || !got.RequestedAt.Equal(want.RequestedAt) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReadRestartIntentAbsentMeansNoRequest(t *testing.T) {
	intent, err := ReadRestartIntent(t.TempDir())
	if intent != nil || err != nil {
		t.Errorf("no file should mean nil, nil: %v %v", intent, err)
	}
}

func TestClearRestartIntentConsumesTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRestartIntent(dir, &RestartIntent{Reason: "testing"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearRestartIntent(dir); err != nil {
		t.Fatal(err)
	}
	intent, err := ReadRestartIntent(dir)
	if intent != nil || err != nil {
		t.Errorf("a cleared intent must be gone: %v %v", intent, err)
	}
}

func TestClearRestartIntentToleratesAbsence(t *testing.T) {
	// Clearing twice (or clearing a never-written intent) is a no-op:
	// the watcher clears before delivering, and a crash between the
	// two must not turn the next clear into an error.
	if err := ClearRestartIntent(t.TempDir()); err != nil {
		t.Errorf("clearing an absent intent must not error: %v", err)
	}
}

func TestRestartIntentIsItsOwnFile(t *testing.T) {
	// A restart intent must be invisible to the reboot reader and vice
	// versa: an older init that knows only reboots must see nothing at
	// all when a restart is requested, not an unreadable reboot intent
	// (which it would honor by rebooting).
	dir := t.TempDir()
	if err := WriteRestartIntent(dir, &RestartIntent{Reason: "testing"}); err != nil {
		t.Fatal(err)
	}
	reboot, err := ReadRebootIntent(dir)
	if reboot != nil || err != nil {
		t.Errorf("a restart intent must not read as a reboot intent: %v %v", reboot, err)
	}
}

func TestWriteRebootIntentNeedsItsChannel(t *testing.T) {
	// The channel directory is init's to create. If the operator
	// writes before it exists, that is a bug to surface, not a reason
	// to create the directory.
	intent := &RebootIntent{Reason: "testing"}
	if err := WriteRebootIntent(filepath.Join(t.TempDir(), "absent"), intent); err == nil {
		t.Error("a missing channel directory must be an error")
	}
}
