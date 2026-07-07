package machine

// Tests for the manifest lifecycle, including every crash window
// staging.go's comments describe: each half-done state must converge
// on retry, never strand a boot without a manifest. The store deals
// in bytes; parsing belongs to its callers, so nothing here parses.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sampleManifest = `apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: liken-test
spec:
  storage:
    machineState:
      device: /dev/vda
      size: 64Mi
`

func TestManifestLifecycleStartsEmpty(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if raw, err := s.LoadStaged(); raw != nil || err != nil {
		t.Errorf("an empty root should have nothing staged: %v %v", raw, err)
	}
	if raw, err := s.LoadProven(); raw != nil || err != nil {
		t.Errorf("an empty root should have nothing proven: %v %v", raw, err)
	}
	if r, err := s.LoadRejection(); r != nil || err != nil {
		t.Errorf("an empty root should have no rejection: %v %v", r, err)
	}
}

func TestWriteStagedRoundTrips(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	raw, err := s.LoadStaged()
	if err != nil {
		t.Fatal(err)
	}
	if ManifestHash(raw) != ManifestHash([]byte(sampleManifest)) {
		t.Error("the bytes read back must hash identically to the bytes written")
	}
}

func TestTheTwoStoresNeverCollide(t *testing.T) {
	root := t.TempDir()
	machines := MachineManifests(root)
	clusters := ClusterManifests(root)
	if err := machines.WriteStaged([]byte("kind: Machine\n")); err != nil {
		t.Fatal(err)
	}
	if err := clusters.WriteStaged([]byte("kind: Cluster\n")); err != nil {
		t.Fatal(err)
	}
	if raw, _ := machines.LoadStaged(); string(raw) != "kind: Machine\n" {
		t.Errorf("the Machine store read back %q", raw)
	}
	if raw, _ := clusters.LoadStaged(); string(raw) != "kind: Cluster\n" {
		t.Errorf("the Cluster store read back %q", raw)
	}
	if err := machines.WithdrawStaged(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := clusters.LoadStaged(); raw == nil {
		t.Error("withdrawing one document's staged file must not touch the other's")
	}
}

func TestPromoteMakesStagedProven(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := s.Promote(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := s.LoadStaged(); raw != nil {
		t.Error("promotion should consume the staged manifest")
	}
	raw, err := s.LoadProven()
	if err != nil || ManifestHash(raw) != ManifestHash([]byte(sampleManifest)) {
		t.Errorf("promotion should install the proven manifest: %v %v", raw, err)
	}
}

func TestPromoteReplacesTheOldProvenAndClearsRejections(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteProven([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	// An earlier staged manifest was rejected; its quarantine stands.
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := s.Reject(Rejection{Hash: "bad", Reason: "did not fit", RejectedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// A new staged manifest arrives and proves out.
	newer := sampleManifest + "  sysctls:\n    vm.overcommit_memory: \"1\"\n"
	if err := s.WriteStaged([]byte(newer)); err != nil {
		t.Fatal(err)
	}
	if err := s.Promote(); err != nil {
		t.Fatal(err)
	}

	raw, err := s.LoadProven()
	if err != nil || raw == nil {
		t.Fatal(err)
	}
	if ManifestHash(raw) != ManifestHash([]byte(newer)) {
		t.Error("promotion should replace the old proven manifest")
	}
	if r, _ := s.LoadRejection(); r != nil {
		t.Error("a success supersedes the standing rejection")
	}
}

func TestRejectQuarantinesTheStagedManifest(t *testing.T) {
	root := t.TempDir()
	s := MachineManifests(root)
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	rejection := Rejection{
		Hash:       ManifestHash([]byte(sampleManifest)),
		Reason:     "disk /dev/vdc is not attached",
		RejectedAt: time.Now().UTC(),
	}
	if err := s.Reject(rejection); err != nil {
		t.Fatal(err)
	}

	if raw, _ := s.LoadStaged(); raw != nil {
		t.Error("rejection should consume the staged manifest")
	}
	r, err := s.LoadRejection()
	if err != nil || r == nil {
		t.Fatal(err)
	}
	if r.Hash != rejection.Hash || r.Reason != rejection.Reason {
		t.Errorf("the rejection note should say what and why: %+v", r)
	}
	// The rejected bytes are quarantined, not destroyed.
	if _, err := os.Stat(filepath.Join(root, "manifests", "rejected.yaml")); err != nil {
		t.Error("the rejected manifest should be kept aside")
	}
}

func TestCrashBetweenRejectionNoteAndRename(t *testing.T) {
	// The crash window in Reject: the note landed, the rename didn't.
	// The next boot must still see the staged manifest and retry it.
	root := t.TempDir()
	s := MachineManifests(root)
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(root, "manifests", "rejection.yaml")
	if err := os.WriteFile(note, []byte("hash: h\nreason: interrupted\nrejectedAt: 2026-07-05T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if raw, err := s.LoadStaged(); raw == nil || err != nil {
		t.Errorf("staged must survive the half-done rejection: %v %v", raw, err)
	}
	// Retrying the rejection completes it.
	if err := s.Reject(Rejection{Hash: "h", Reason: "still broken", RejectedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := s.LoadStaged(); raw != nil {
		t.Error("the retried rejection should complete the quarantine")
	}
}

func TestPromoteWithoutAStagedManifestFails(t *testing.T) {
	// Promotion is only ever called on the manifest that just booted;
	// a missing staged file at that moment is a bug worth reporting.
	if err := MachineManifests(t.TempDir()).Promote(); err == nil {
		t.Error("expected an error promoting nothing")
	}
}

func TestRejectWithoutAStagedManifestFails(t *testing.T) {
	s := MachineManifests(t.TempDir())
	err := s.Reject(Rejection{Reason: "nothing to reject", RejectedAt: time.Now()})
	if err == nil {
		t.Error("expected an error rejecting nothing")
	}
}

func TestWithdrawStagedRemovesTheStagedManifest(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := s.WithdrawStaged(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := s.LoadStaged(); raw != nil {
		t.Error("withdrawal should remove the staged manifest")
	}
}

func TestWithdrawStagedWithNothingStagedIsFine(t *testing.T) {
	if err := MachineManifests(t.TempDir()).WithdrawStaged(); err != nil {
		t.Errorf("nothing to withdraw is not an error: %v", err)
	}
}

func TestClearRejectionRemovesBothFiles(t *testing.T) {
	root := t.TempDir()
	s := MachineManifests(root)
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := s.Reject(Rejection{Hash: "h", Reason: "did not fit", RejectedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearRejection(); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.LoadRejection(); r != nil {
		t.Error("the rejection note should be gone")
	}
	if _, err := os.Stat(filepath.Join(root, "manifests", "rejected.yaml")); !os.IsNotExist(err) {
		t.Error("the quarantined manifest should be gone")
	}
}

func TestClearRejectionWithNoRejectionIsFine(t *testing.T) {
	if err := MachineManifests(t.TempDir()).ClearRejection(); err != nil {
		t.Errorf("nothing to clear is not an error: %v", err)
	}
}

func TestLoadRejectionRejectsGarbage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rejection.yaml"), []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MachineManifests(root).LoadRejection(); err == nil {
		t.Error("expected an error for an unparseable rejection note")
	}
}

func TestAttemptedMarkerRoundTrips(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if h, err := s.LoadAttempted(); h != "" || err != nil {
		t.Errorf("an empty store has no attempted marker: %q %v", h, err)
	}
	if err := s.WriteAttempted("abc123"); err != nil {
		t.Fatal(err)
	}
	h, err := s.LoadAttempted()
	if err != nil || h != "abc123" {
		t.Errorf("got %q %v", h, err)
	}
}

func TestPromoteClearsTheAttemptedMarker(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAttempted(ManifestHash([]byte(sampleManifest))); err != nil {
		t.Fatal(err)
	}
	if err := s.Promote(); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.LoadAttempted(); h != "" {
		t.Errorf("promotion ends the trial; the marker should be gone, got %q", h)
	}
}

func TestRejectClearsTheAttemptedMarker(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAttempted(ManifestHash([]byte(sampleManifest))); err != nil {
		t.Fatal(err)
	}
	if err := s.Reject(Rejection{Hash: "h", Reason: "never joined", RejectedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.LoadAttempted(); h != "" {
		t.Errorf("rejection ends the trial; the marker should be gone, got %q", h)
	}
}

func TestManifestHashIsStable(t *testing.T) {
	a := ManifestHash([]byte(sampleManifest))
	b := ManifestHash([]byte(sampleManifest))
	if a != b || len(a) != 64 {
		t.Errorf("hashes should be stable 64-char hex: %q %q", a, b)
	}
	if a == ManifestHash([]byte(sampleManifest+"\n")) {
		t.Error("different bytes must hash differently")
	}
}

func TestWithdrawClearsTheAttemptedMarker(t *testing.T) {
	store := SystemReleases(t.TempDir())
	if err := store.WriteStaged([]byte("record")); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteAttempted(ManifestHash([]byte("record"))); err != nil {
		t.Fatal(err)
	}

	if err := store.WithdrawStaged(); err != nil {
		t.Fatal(err)
	}

	if attempted, _ := store.LoadAttempted(); attempted != "" {
		t.Error("a withdrawn trial's marker must go with it, or the next staging of the identical record would falsely reject")
	}
}
