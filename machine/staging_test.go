package machine

// Tests for the manifest lifecycle, including every crash window
// staging.go's comments describe: each half-done state must converge
// on retry, never strand a boot without a manifest.

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
	root := t.TempDir()
	if m, raw, err := LoadStaged(root); m != nil || raw != nil || err != nil {
		t.Errorf("an empty root should have nothing staged: %v %v %v", m, raw, err)
	}
	if m, raw, err := LoadProven(root); m != nil || raw != nil || err != nil {
		t.Errorf("an empty root should have nothing proven: %v %v %v", m, raw, err)
	}
	if r, err := LoadRejection(root); r != nil || err != nil {
		t.Errorf("an empty root should have no rejection: %v %v", r, err)
	}
}

func TestWriteStagedRoundTrips(t *testing.T) {
	root := t.TempDir()
	if err := WriteStaged(root, []byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	m, raw, err := LoadStaged(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Metadata.Name != "liken-test" {
		t.Errorf("staged manifest lost its content: %+v", m)
	}
	if ManifestHash(raw) != ManifestHash([]byte(sampleManifest)) {
		t.Error("the bytes read back must hash identically to the bytes written")
	}
}

func TestPromoteMakesStagedProven(t *testing.T) {
	root := t.TempDir()
	if err := WriteStaged(root, []byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := Promote(root); err != nil {
		t.Fatal(err)
	}
	if m, _, _ := LoadStaged(root); m != nil {
		t.Error("promotion should consume the staged manifest")
	}
	m, _, err := LoadProven(root)
	if err != nil || m == nil || m.Metadata.Name != "liken-test" {
		t.Errorf("promotion should install the proven manifest: %v %v", m, err)
	}
}

func TestPromoteReplacesTheOldProvenAndClearsRejections(t *testing.T) {
	root := t.TempDir()
	if err := WriteProven(root, []byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	// An earlier staged manifest was rejected; its quarantine stands.
	if err := WriteStaged(root, []byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := Reject(root, Rejection{Hash: "bad", Reason: "did not fit", RejectedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// A new staged manifest arrives and proves out.
	newer := sampleManifest + "  sysctls:\n    vm.overcommit_memory: \"1\"\n"
	if err := WriteStaged(root, []byte(newer)); err != nil {
		t.Fatal(err)
	}
	if err := Promote(root); err != nil {
		t.Fatal(err)
	}

	m, raw, err := LoadProven(root)
	if err != nil || m == nil {
		t.Fatal(err)
	}
	if ManifestHash(raw) != ManifestHash([]byte(newer)) {
		t.Error("promotion should replace the old proven manifest")
	}
	if r, _ := LoadRejection(root); r != nil {
		t.Error("a success supersedes the standing rejection")
	}
}

func TestRejectQuarantinesTheStagedManifest(t *testing.T) {
	root := t.TempDir()
	if err := WriteStaged(root, []byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	rejection := Rejection{
		Hash:       ManifestHash([]byte(sampleManifest)),
		Reason:     "disk /dev/vdc is not attached",
		RejectedAt: time.Now().UTC(),
	}
	if err := Reject(root, rejection); err != nil {
		t.Fatal(err)
	}

	if m, _, _ := LoadStaged(root); m != nil {
		t.Error("rejection should consume the staged manifest")
	}
	r, err := LoadRejection(root)
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
	if err := WriteStaged(root, []byte(sampleManifest)); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(root, "manifests", "rejection.yaml")
	if err := os.WriteFile(note, []byte("hash: h\nreason: interrupted\nrejectedAt: 2026-07-05T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if m, _, err := LoadStaged(root); m == nil || err != nil {
		t.Errorf("staged must survive the half-done rejection: %v %v", m, err)
	}
	// Retrying the rejection completes it.
	if err := Reject(root, Rejection{Hash: "h", Reason: "still broken", RejectedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if m, _, _ := LoadStaged(root); m != nil {
		t.Error("the retried rejection should complete the quarantine")
	}
}

func TestLoadStagedReturnsBytesEvenWhenParsingFails(t *testing.T) {
	// A staged file that won't parse must still yield its bytes: the
	// rejection that follows records the hash of exactly what was read.
	root := t.TempDir()
	garbage := []byte("not: [valid")
	if err := WriteStaged(root, garbage); err != nil {
		t.Fatal(err)
	}
	m, raw, err := LoadStaged(root)
	if err == nil || m != nil {
		t.Fatal("garbage should not parse")
	}
	if ManifestHash(raw) != ManifestHash(garbage) {
		t.Error("the unparseable bytes must come back for hashing")
	}
}

func TestPromoteWithoutAStagedManifestFails(t *testing.T) {
	// Promotion is only ever called on the manifest that just booted;
	// a missing staged file at that moment is a bug worth hearing about.
	if err := Promote(t.TempDir()); err == nil {
		t.Error("expected an error promoting nothing")
	}
}

func TestRejectWithoutAStagedManifestFails(t *testing.T) {
	root := t.TempDir()
	err := Reject(root, Rejection{Reason: "nothing to reject", RejectedAt: time.Now()})
	if err == nil {
		t.Error("expected an error rejecting nothing")
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
	if _, err := LoadRejection(root); err == nil {
		t.Error("expected an error for an unparseable rejection note")
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
