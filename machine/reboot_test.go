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
	// missing one is an error the operator reports, not papers over.
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
		"aggressive": RebootManual, // when in doubt, don't reboot
	}
	for in, want := range cases {
		spec := MachineSpec{RebootPolicy: in}
		if got := spec.RebootPolicyOrDefault(); got != want {
			t.Errorf("RebootPolicyOrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteRebootIntentNeedsItsChannel(t *testing.T) {
	// The channel directory is init's to create; an operator writing
	// before it exists is a bug to surface, not a directory to invent.
	intent := &RebootIntent{Reason: "testing"}
	if err := WriteRebootIntent(filepath.Join(t.TempDir(), "absent"), intent); err == nil {
		t.Error("a missing channel directory must be an error")
	}
}
