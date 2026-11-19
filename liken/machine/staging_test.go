package machine

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
		t.Errorf("LoadStaged on an empty root = %q, %v, want nil, nil", raw, err)
	}
	if raw, err := s.LoadProven(); raw != nil || err != nil {
		t.Errorf("LoadProven on an empty root = %q, %v, want nil, nil", raw, err)
	}
	if r, err := s.LoadRejection(); r != nil || err != nil {
		t.Errorf("LoadRejection on an empty root = %+v, %v, want nil, nil", r, err)
	}
}

func TestWriteStagedRoundTrips(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	raw, err := s.LoadStaged()
	if err != nil {
		t.Fatalf("loading the staged document: %v", err)
	}
	if ManifestHash(raw) != ManifestHash([]byte(sampleManifest)) {
		t.Errorf("the staged bytes read back are %q, want the bytes written", raw)
	}
}

func TestMachineAndClusterStoresUseSeparateDirectories(t *testing.T) {
	root := t.TempDir()
	machines := MachineManifests(root)
	clusters := ClusterManifests(root)
	if err := machines.WriteStaged([]byte("kind: Machine\n")); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := clusters.WriteStaged([]byte("kind: Cluster\n")); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if raw, _ := machines.LoadStaged(); string(raw) != "kind: Machine\n" {
		t.Errorf("the Machine store staged %q, want %q", raw, "kind: Machine\n")
	}
	if raw, _ := clusters.LoadStaged(); string(raw) != "kind: Cluster\n" {
		t.Errorf("the Cluster store staged %q, want %q", raw, "kind: Cluster\n")
	}
	if err := machines.WithdrawStaged(); err != nil {
		t.Fatalf("withdrawing the staged document: %v", err)
	}
	if raw, _ := clusters.LoadStaged(); raw == nil {
		t.Error("the Cluster store has no staged file after the Machine store withdrew its own, want it kept")
	}
}

func TestPromoteMakesStagedProven(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := s.Promote(); err != nil {
		t.Fatalf("promoting the staged document: %v", err)
	}
	if raw, _ := s.LoadStaged(); raw != nil {
		t.Error("a staged manifest remains after Promote, want none")
	}
	raw, err := s.LoadProven()
	if err != nil || ManifestHash(raw) != ManifestHash([]byte(sampleManifest)) {
		t.Errorf("after Promote the proven manifest is %q with error %v, want the staged bytes", raw, err)
	}
}

func TestPromoteReplacesTheOldProvenAndClearsRejections(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteProven([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the proven document: %v", err)
	}
	// An earlier staged manifest was rejected.
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := s.Reject(Rejection{Hash: "bad", Reason: "did not fit", RejectedAt: time.Now()}); err != nil {
		t.Fatalf("rejecting the staged document: %v", err)
	}
	// A new staged manifest is staged and then promoted.
	newer := sampleManifest + "  sysctls:\n    vm.overcommit_memory: \"1\"\n"
	if err := s.WriteStaged([]byte(newer)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := s.Promote(); err != nil {
		t.Fatalf("promoting the staged document: %v", err)
	}

	raw, err := s.LoadProven()
	if err != nil || raw == nil {
		t.Fatalf("the proven document is %q with error %v, want its bytes", raw, err)
	}
	if ManifestHash(raw) != ManifestHash([]byte(newer)) {
		t.Error("after Promote the proven manifest is the old one, want the newly staged one")
	}
	if r, _ := s.LoadRejection(); r != nil {
		t.Error("a rejection remains after Promote, want none because a promotion supersedes it")
	}
}

func TestRejectMovesTheStagedManifestToRejectedYAML(t *testing.T) {
	root := t.TempDir()
	s := MachineManifests(root)
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	rejection := Rejection{
		Hash:       ManifestHash([]byte(sampleManifest)),
		Reason:     "disk /dev/vdc is not attached",
		RejectedAt: time.Now().UTC(),
	}
	if err := s.Reject(rejection); err != nil {
		t.Fatalf("rejecting the staged document: %v", err)
	}

	if raw, _ := s.LoadStaged(); raw != nil {
		t.Error("a staged manifest remains after Reject, want none")
	}
	r, err := s.LoadRejection()
	if err != nil || r == nil {
		t.Fatalf("the rejection is %+v with error %v, want a rejection", r, err)
	}
	if r.Hash != rejection.Hash || r.Reason != rejection.Reason {
		t.Errorf("the rejection is %+v, want hash %q and reason %q", r, rejection.Hash, rejection.Reason)
	}
	// Reject keeps the rejected bytes for a person to read.
	if _, err := os.Stat(filepath.Join(root, "manifests", "rejected.yaml")); err != nil {
		t.Error("manifests/rejected.yaml does not exist after Reject, want the rejected bytes kept there")
	}
}

func TestCrashBetweenRejectionNoteAndRename(t *testing.T) {
	// Reject can be interrupted after it writes the rejection note and
	// before it renames the staged file. The next boot must still find
	// the staged manifest and retry it.
	root := t.TempDir()
	s := MachineManifests(root)
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	note := filepath.Join(root, "manifests", "rejection.yaml")
	if err := os.WriteFile(note, []byte("hash: h\nreason: interrupted\nrejectedAt: 2026-07-05T00:00:00Z\n"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	if raw, err := s.LoadStaged(); raw == nil || err != nil {
		t.Errorf("LoadStaged after a rejection note with no rename = %q, %v, want the staged bytes", raw, err)
	}
	// Retrying the rejection completes it.
	if err := s.Reject(Rejection{Hash: "h", Reason: "still broken", RejectedAt: time.Now()}); err != nil {
		t.Fatalf("rejecting the staged document: %v", err)
	}
	if raw, _ := s.LoadStaged(); raw != nil {
		t.Error("a staged manifest remains after the second Reject, want none")
	}
}

func TestPromoteWithoutAStagedManifestFails(t *testing.T) {
	// Init promotes only a manifest that it just booted, so a missing
	// staged file means a bug and Promote must report it.
	if err := MachineManifests(t.TempDir()).Promote(); err == nil {
		t.Error("Promote with no staged manifest returned nil, want an error")
	}
}

func TestRejectWithoutAStagedManifestFails(t *testing.T) {
	s := MachineManifests(t.TempDir())
	err := s.Reject(Rejection{Reason: "nothing to reject", RejectedAt: time.Now()})
	if err == nil {
		t.Error("Reject with no staged manifest returned nil, want an error")
	}
}

func TestWithdrawStagedRemovesTheStagedManifest(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := s.WithdrawStaged(); err != nil {
		t.Fatalf("withdrawing the staged document: %v", err)
	}
	if raw, _ := s.LoadStaged(); raw != nil {
		t.Error("a staged manifest remains after WithdrawStaged, want none")
	}
}

func TestWithdrawStagedWithNothingStagedIsFine(t *testing.T) {
	if err := MachineManifests(t.TempDir()).WithdrawStaged(); err != nil {
		t.Errorf("WithdrawStaged with nothing staged = %v, want nil", err)
	}
}

func TestClearRejectionRemovesBothFiles(t *testing.T) {
	root := t.TempDir()
	s := MachineManifests(root)
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := s.Reject(Rejection{Hash: "h", Reason: "did not fit", RejectedAt: time.Now()}); err != nil {
		t.Fatalf("rejecting the staged document: %v", err)
	}
	if err := s.ClearRejection(); err != nil {
		t.Fatalf("clearing the rejection: %v", err)
	}
	if r, _ := s.LoadRejection(); r != nil {
		t.Error("a rejection remains after ClearRejection, want none")
	}
	if _, err := os.Stat(filepath.Join(root, "manifests", "rejected.yaml")); !os.IsNotExist(err) {
		t.Error("manifests/rejected.yaml exists after ClearRejection, want it removed")
	}
}

func TestClearRejectionWithNoRejectionIsFine(t *testing.T) {
	if err := MachineManifests(t.TempDir()).ClearRejection(); err != nil {
		t.Errorf("ClearRejection with no rejection = %v, want nil", err)
	}
}

func TestLoadRejectionRejectsGarbage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rejection.yaml"), []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if _, err := MachineManifests(root).LoadRejection(); err == nil {
		t.Error("LoadRejection of a file that is not YAML returned nil, want an error")
	}
}

func TestAttemptedMarkerRoundTrips(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if h, err := s.LoadAttempted(); h != "" || err != nil {
		t.Errorf("LoadAttempted on an empty store = %q, %v, want empty, nil", h, err)
	}
	if err := s.WriteAttempted("abc123"); err != nil {
		t.Fatalf("writing the attempted marker: %v", err)
	}
	h, err := s.LoadAttempted()
	if err != nil || h != "abc123" {
		t.Errorf("LoadAttempted = %q, %v, want abc123, nil", h, err)
	}
}

func TestPromoteClearsTheAttemptedMarker(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := s.WriteAttempted(ManifestHash([]byte(sampleManifest))); err != nil {
		t.Fatalf("writing the attempted marker: %v", err)
	}
	if err := s.Promote(); err != nil {
		t.Fatalf("promoting the staged document: %v", err)
	}
	if h, _ := s.LoadAttempted(); h != "" {
		t.Errorf("the attempted marker is %q after Promote, want none because promotion ends the trial", h)
	}
}

func TestRejectClearsTheAttemptedMarker(t *testing.T) {
	s := MachineManifests(t.TempDir())
	if err := s.WriteStaged([]byte(sampleManifest)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := s.WriteAttempted(ManifestHash([]byte(sampleManifest))); err != nil {
		t.Fatalf("writing the attempted marker: %v", err)
	}
	if err := s.Reject(Rejection{Hash: "h", Reason: "never joined", RejectedAt: time.Now()}); err != nil {
		t.Fatalf("rejecting the staged document: %v", err)
	}
	if h, _ := s.LoadAttempted(); h != "" {
		t.Errorf("the attempted marker is %q after Reject, want none because rejection ends the trial", h)
	}
}

func TestManifestHashIsStable(t *testing.T) {
	a := ManifestHash([]byte(sampleManifest))
	b := ManifestHash([]byte(sampleManifest))
	if a != b || len(a) != 64 {
		t.Errorf("hashing the same bytes twice gives %q and %q, want the same 64 hexadecimal digits", a, b)
	}
	if a == ManifestHash([]byte(sampleManifest+"\n")) {
		t.Error("two different manifests have the same hash, want different hashes")
	}
}

func TestWithdrawClearsTheAttemptedMarker(t *testing.T) {
	store := SystemReleases(t.TempDir())
	if err := store.WriteStaged([]byte("record")); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := store.WriteAttempted(ManifestHash([]byte("record"))); err != nil {
		t.Fatalf("writing the attempted marker: %v", err)
	}

	if err := store.WithdrawStaged(); err != nil {
		t.Fatalf("withdrawing the staged document: %v", err)
	}

	if attempted, _ := store.LoadAttempted(); attempted != "" {
		t.Errorf("the attempted marker is %q after WithdrawStaged, want none because the next staging of an identical record would be rejected", attempted)
	}
}

func TestNewRejectionRecordsTheHashAndTime(t *testing.T) {
	at := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	r := NewRejection([]byte(sampleManifest), "did not fit", at)
	if r.Hash != ManifestHash([]byte(sampleManifest)) {
		t.Errorf("the rejection hash is %q, want the hash of the refused bytes", r.Hash)
	}
	if r.Reason != "did not fit" || !r.RejectedAt.Equal(at) {
		t.Errorf("the rejection is %+v, want reason %q and time %v", r, "did not fit", at)
	}
}

func TestWriteStagedReportsAnUncreatableStore(t *testing.T) {
	s := MachineManifests(readOnlyDir(t))
	if err := s.WriteStaged([]byte(sampleManifest)); err == nil {
		t.Error("WriteStaged under a read-only root returned nil, want an error")
	}
}

func TestWriteProvenReportsAnUncreatableStore(t *testing.T) {
	s := MachineManifests(readOnlyDir(t))
	if err := s.WriteProven([]byte(sampleManifest)); err == nil {
		t.Error("WriteProven under a read-only root returned nil, want an error")
	}
}

func TestWriteAttemptedReportsAnUncreatableStore(t *testing.T) {
	s := MachineManifests(readOnlyDir(t))
	if err := s.WriteAttempted("abc123"); err == nil {
		t.Error("WriteAttempted under a read-only root returned nil, want an error")
	}
}

func TestWriteStagedReportsASealedStore(t *testing.T) {
	s := sealedStore(t, nil)
	if err := s.WriteStaged([]byte(sampleManifest)); err == nil {
		t.Error("WriteStaged into a read-only store returned nil, want an error")
	}
}

func TestRejectReportsASealedStore(t *testing.T) {
	s := sealedStore(t, map[string]string{"staged.yaml": sampleManifest})
	err := s.Reject(Rejection{Hash: "h", Reason: "testing", RejectedAt: time.Now()})
	if err == nil {
		t.Error("Reject in a read-only store returned nil, want an error")
	}
}

func TestWithdrawStagedReportsASealedStore(t *testing.T) {
	s := sealedStore(t, map[string]string{"staged.yaml": sampleManifest})
	if err := s.WithdrawStaged(); err == nil {
		t.Error("WithdrawStaged in a read-only store returned nil, want an error")
	}
}

func TestClearRejectionReportsASealedStore(t *testing.T) {
	s := sealedStore(t, map[string]string{"rejected.yaml": sampleManifest})
	if err := s.ClearRejection(); err == nil {
		t.Error("ClearRejection in a read-only store returned nil, want an error")
	}
}

func TestLoadStagedReportsAnUnreadableFile(t *testing.T) {
	root := t.TempDir()
	unreadableFile(t, filepath.Join(root, "manifests", "staged.yaml"))
	if _, err := MachineManifests(root).LoadStaged(); err == nil {
		t.Error("LoadStaged of an unreadable staged file returned nil, want an error and not an empty store")
	}
}

func TestWriteDurableReportsAnUnwritableDirectory(t *testing.T) {
	err := WriteDurable(filepath.Join(readOnlyDir(t), "file.yaml"), []byte("x"))
	if err == nil {
		t.Error("WriteDurable into a read-only directory returned nil, want an error")
	}
}

func TestSyncDirReportsAMissingDirectory(t *testing.T) {
	if err := syncDir(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("syncDir of a missing directory returned nil, want an error")
	}
}
