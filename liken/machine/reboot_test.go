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
		t.Fatalf("writing the reboot intent: %v", err)
	}
	got, err := ReadRebootIntent(dir)
	if err != nil {
		t.Fatalf("reading the reboot intent: %v", err)
	}
	if got == nil || got.Reason != want.Reason || got.ManifestHash != want.ManifestHash || !got.RequestedAt.Equal(want.RequestedAt) {
		t.Errorf("the intent is %+v, want %+v", got, want)
	}
}

func TestReadRebootIntentAbsentMeansNoRequest(t *testing.T) {
	intent, err := ReadRebootIntent(t.TempDir())
	if intent != nil || err != nil {
		t.Errorf("with no file, ReadRebootIntent returns %v and %v, want nil and nil", intent, err)
	}
}

func TestReadRebootIntentRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reboot-intent.yaml"), []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("writing the intent file: %v", err)
	}
	if _, err := ReadRebootIntent(dir); err == nil {
		t.Error("ReadRebootIntent accepted an unparseable file, want an error")
	}
}

func TestWriteRebootIntentNeedsItsDirectory(t *testing.T) {
	err := WriteRebootIntent(filepath.Join(t.TempDir(), "absent"), &RebootIntent{Reason: "test"})
	if err == nil {
		t.Error("WriteRebootIntent returned nil for a missing intent directory, want an error")
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
			t.Errorf("RebootPolicyOrDefault(%q) is %q, want %q", in, got, want)
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
		t.Fatalf("writing the restart intent: %v", err)
	}
	got, err := ReadRestartIntent(dir)
	if err != nil {
		t.Fatalf("reading the restart intent: %v", err)
	}
	if got == nil || got.Reason != want.Reason || !got.RequestedAt.Equal(want.RequestedAt) {
		t.Errorf("the intent is %+v, want %+v", got, want)
	}
}

func TestReadRestartIntentAbsentMeansNoRequest(t *testing.T) {
	intent, err := ReadRestartIntent(t.TempDir())
	if intent != nil || err != nil {
		t.Errorf("with no file, ReadRestartIntent returns %v and %v, want nil and nil", intent, err)
	}
}

func TestClearRestartIntentConsumesTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRestartIntent(dir, &RestartIntent{Reason: "testing"}); err != nil {
		t.Fatalf("writing the restart intent: %v", err)
	}
	if err := ClearRestartIntent(dir); err != nil {
		t.Fatalf("clearing the restart intent: %v", err)
	}
	intent, err := ReadRestartIntent(dir)
	if intent != nil || err != nil {
		t.Errorf("after the intent is cleared, the read returns %v and %v, want nil and nil", intent, err)
	}
}

func TestClearRestartIntentToleratesAbsence(t *testing.T) {
	if err := ClearRestartIntent(t.TempDir()); err != nil {
		t.Errorf("clearing an absent intent: %v", err)
	}
}

func TestRestartIntentIsItsOwnFile(t *testing.T) {
	// An older init reads only the reboot intent file, and it reboots
	// when that file is unreadable. A restart request must not appear
	// there.
	dir := t.TempDir()
	if err := WriteRestartIntent(dir, &RestartIntent{Reason: "testing"}); err != nil {
		t.Fatalf("writing the restart intent: %v", err)
	}
	reboot, err := ReadRebootIntent(dir)
	if reboot != nil || err != nil {
		t.Errorf("the reboot intent read is %v and %v after a restart intent, want nil and nil", reboot, err)
	}
}

func TestModulesIntentRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := &ModulesIntent{
		Reason:       "loading the staged spec's added modules",
		ManifestHash: "d29abd212304",
		RequestedAt:  time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
	}
	if err := WriteModulesIntent(dir, want); err != nil {
		t.Fatalf("writing the modules intent: %v", err)
	}
	got, err := ReadModulesIntent(dir)
	if err != nil {
		t.Fatalf("reading the modules intent: %v", err)
	}
	if got == nil || got.Reason != want.Reason || got.ManifestHash != want.ManifestHash {
		t.Errorf("the intent is %+v, want %+v", got, want)
	}
}

func TestReadModulesIntentAbsentMeansNoRequest(t *testing.T) {
	intent, err := ReadModulesIntent(t.TempDir())
	if intent != nil || err != nil {
		t.Errorf("with no file, ReadModulesIntent returns %v and %v, want nil and nil", intent, err)
	}
}

func TestClearModulesIntentConsumesTheFileAndToleratesAbsence(t *testing.T) {
	dir := t.TempDir()
	if err := WriteModulesIntent(dir, &ModulesIntent{Reason: "testing"}); err != nil {
		t.Fatalf("writing the modules intent: %v", err)
	}
	if err := ClearModulesIntent(dir); err != nil {
		t.Fatalf("clearing the modules intent: %v", err)
	}
	if intent, err := ReadModulesIntent(dir); intent != nil || err != nil {
		t.Errorf("after the intent is cleared, the read returns %v and %v, want nil and nil", intent, err)
	}
	if err := ClearModulesIntent(dir); err != nil {
		t.Errorf("clearing an absent intent: %v", err)
	}
}

func TestModulesIntentIsItsOwnFile(t *testing.T) {
	// An older init reads only the reboot intent file, so a modules
	// request must not appear there.
	dir := t.TempDir()
	if err := WriteModulesIntent(dir, &ModulesIntent{Reason: "testing"}); err != nil {
		t.Fatalf("writing the modules intent: %v", err)
	}
	if reboot, err := ReadRebootIntent(dir); reboot != nil || err != nil {
		t.Errorf("the reboot intent read is %v and %v after a modules intent, want nil and nil", reboot, err)
	}
}

func TestReadRebootIntentReportsAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	unreadableFile(t, filepath.Join(dir, "reboot-intent.yaml"))
	if _, err := ReadRebootIntent(dir); err == nil {
		t.Error("ReadRebootIntent returned nil for an unreadable file, want an error")
	}
}
