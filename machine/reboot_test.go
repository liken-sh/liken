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
	// Init creates the channel directory, not the operator. If the
	// operator writes into a missing directory, that is an error. The
	// operator should report this error, not hide it.
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
	// Clearing twice, or clearing a never-written intent, does nothing
	// and causes no error. The watcher clears the intent file before
	// it delivers the intent's effect. A crash between these two steps
	// must not turn the next clear into an error.
	if err := ClearRestartIntent(t.TempDir()); err != nil {
		t.Errorf("clearing an absent intent must not error: %v", err)
	}
}

func TestRestartIntentIsItsOwnFile(t *testing.T) {
	// A restart intent must be invisible to the reboot reader, and a
	// reboot intent must be invisible to the restart reader. An older
	// init that knows only about reboots must see nothing at all when
	// a restart is requested. It must not see an unreadable reboot
	// intent, because it would honor an unreadable reboot intent by
	// rebooting the machine.
	dir := t.TempDir()
	if err := WriteRestartIntent(dir, &RestartIntent{Reason: "testing"}); err != nil {
		t.Fatal(err)
	}
	reboot, err := ReadRebootIntent(dir)
	if reboot != nil || err != nil {
		t.Errorf("a restart intent must not read as a reboot intent: %v %v", reboot, err)
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
		t.Fatal(err)
	}
	got, err := ReadModulesIntent(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Reason != want.Reason || got.ManifestHash != want.ManifestHash {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReadModulesIntentAbsentMeansNoRequest(t *testing.T) {
	intent, err := ReadModulesIntent(t.TempDir())
	if intent != nil || err != nil {
		t.Errorf("no file should mean nil, nil: %v %v", intent, err)
	}
}

func TestClearModulesIntentConsumesTheFileAndToleratesAbsence(t *testing.T) {
	dir := t.TempDir()
	if err := WriteModulesIntent(dir, &ModulesIntent{Reason: "testing"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearModulesIntent(dir); err != nil {
		t.Fatal(err)
	}
	if intent, err := ReadModulesIntent(dir); intent != nil || err != nil {
		t.Errorf("a cleared intent must be gone: %v %v", intent, err)
	}
	if err := ClearModulesIntent(dir); err != nil {
		t.Errorf("clearing an absent intent must not error: %v", err)
	}
}

func TestModulesIntentIsItsOwnFile(t *testing.T) {
	// Like the restart intent, a modules intent is invisible to the
	// reboot reader. So an older init that knows only about reboots
	// sees nothing at all.
	dir := t.TempDir()
	if err := WriteModulesIntent(dir, &ModulesIntent{Reason: "testing"}); err != nil {
		t.Fatal(err)
	}
	if reboot, err := ReadRebootIntent(dir); reboot != nil || err != nil {
		t.Errorf("a modules intent must not read as a reboot intent: %v %v", reboot, err)
	}
}

func TestWriteRebootIntentNeedsItsChannel(t *testing.T) {
	// Init creates the channel directory, not the operator. If the
	// operator writes before the directory exists, that is a bug to
	// surface. The system must not create the directory to work around
	// the bug.
	intent := &RebootIntent{Reason: "testing"}
	if err := WriteRebootIntent(filepath.Join(t.TempDir(), "absent"), intent); err == nil {
		t.Error("a missing channel directory must be an error")
	}
}

func TestReadRebootIntentReportsAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	unreadableFile(t, filepath.Join(dir, "reboot-intent.yaml"))
	if _, err := ReadRebootIntent(dir); err == nil {
		t.Error("an intent that exists but can't be read is an error, not an absent intent")
	}
}
