package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

var lifecycleNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func TestCarryOutConvergenceStages(t *testing.T) {
	store := machine.ClusterManifests(t.TempDir())
	conv := convergence{
		condition: notConverged("ClusterConverged", "RebootPending", "staged"),
		stage:     true,
		manifest:  []byte("kind: Cluster\n"),
		hash:      "abc123",
	}
	condition := carryOutConvergence(conv, store, t.TempDir(), "cluster document", lifecycleNow, nil)
	if condition.Reason != "RebootPending" {
		t.Errorf("condition reason is %s, want RebootPending, the decision's condition", condition.Reason)
	}
	staged, err := store.LoadStaged()
	if err != nil || string(staged) != "kind: Cluster\n" {
		t.Errorf("staged manifest is %q (error %v), want the manifest of the decision", staged, err)
	}
}

func TestCarryOutConvergenceWithdrawsAndClears(t *testing.T) {
	store := machine.ClusterManifests(t.TempDir())
	if err := store.WriteStaged([]byte("kind: Cluster\n")); err != nil {
		t.Fatalf("staging the first document: %v", err)
	}
	if err := store.Reject(machine.Rejection{Hash: "stale", Reason: "the test says so", RejectedAt: lifecycleNow}); err != nil {
		t.Fatalf("recording the rejection: %v", err)
	}
	// The rejection moved the staged document aside, so a newer one is
	// staged beside the record.
	if err := store.WriteStaged([]byte("kind: Cluster\nmetadata: {name: newer}\n")); err != nil {
		t.Fatalf("staging the newer document: %v", err)
	}
	conv := convergence{
		condition:      converged("ClusterConverged", "Converged", "current"),
		withdraw:       true,
		clearRejection: true,
	}
	carryOutConvergence(conv, store, t.TempDir(), "cluster document", lifecycleNow, nil)
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged copy is still present, want it withdrawn")
	}
	if rejection, _ := store.LoadRejection(); rejection != nil {
		t.Error("the rejection record is still present, want it cleared")
	}
}

func TestCarryOutConvergenceReportsAFailedStaging(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("making the parent read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	store := machine.ClusterManifests(filepath.Join(parent, "unwritable"))
	conv := convergence{
		condition: notConverged("ClusterConverged", "RebootPending", "staged"),
		stage:     true,
		manifest:  []byte("kind: Cluster\n"),
	}
	condition := carryOutConvergence(conv, store, t.TempDir(), "cluster document", lifecycleNow, nil)
	if condition.Reason != "StagingFailed" {
		t.Errorf("condition reason is %s, want StagingFailed", condition.Reason)
	}
}

func TestCarryOutConvergenceReportsAFailedRestartRequest(t *testing.T) {
	// init creates the intent directory, so the operator does not
	// create it when it is missing.
	store := machine.RegistryCredentialsStore(t.TempDir())
	conv := convergence{
		condition:      notConverged("CredentialsConverged", "RestartRequested", "restarting"),
		requestRestart: true,
		hash:           "abc123",
	}
	condition := carryOutConvergence(conv, store, filepath.Join(t.TempDir(), "missing"), "registry credentials", lifecycleNow, nil)
	if condition.Reason != "StagingFailed" {
		t.Errorf("condition reason is %s, want StagingFailed", condition.Reason)
	}
}

func TestReadStagedHashOfAnEmptyStore(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	if hash := readStagedHash(store); hash != "" {
		t.Errorf("staged hash of an empty store is %q, want an empty string", hash)
	}
}

func TestReadStagedHashIsTheHashOfTheStagedBytes(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	raw := []byte("kind: Machine\nmetadata: {name: liken-dev}\n")
	if err := store.WriteStaged(raw); err != nil {
		t.Fatalf("staging the bytes: %v", err)
	}
	if hash := readStagedHash(store); hash != machine.ManifestHash(raw) {
		t.Errorf("staged hash is %q, want the hash of the staged bytes", hash)
	}
}

func TestReadStagedHashHashesUnparsableBytes(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	raw := []byte(":: this is not yaml ::")
	if err := store.WriteStaged(raw); err != nil {
		t.Fatalf("staging the bytes: %v", err)
	}
	if hash := readStagedHash(store); hash != machine.ManifestHash(raw) {
		t.Errorf("staged hash is %q, want the hash of the staged bytes", hash)
	}
}

// Each kind of disruption that a decision requests is written as the
// intent file that init reads, and counts as a write.
func TestCarryOutConvergenceWritesTheIntentItAsksFor(t *testing.T) {
	cases := []struct {
		name   string
		conv   convergence
		landed func(dir string) bool
	}{
		{"a reboot", convergence{requestReboot: true, hash: "abc123"}, func(dir string) bool {
			intent, _ := machine.ReadRebootIntent(dir)
			return intent != nil && intent.ManifestHash == "abc123"
		}},
		{"a k3s restart", convergence{requestRestart: true, hash: "abc123"}, func(dir string) bool {
			intent, _ := machine.ReadRestartIntent(dir)
			return intent != nil
		}},
		{"a live module load", convergence{requestLoad: true, hash: "abc123"}, func(dir string) bool {
			intent, _ := machine.ReadModulesIntent(dir)
			return intent != nil && intent.ManifestHash == "abc123"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runDir := t.TempDir()
			out := &passOutcome{}

			carryOutConvergence(c.conv, machine.MachineManifests(t.TempDir()), runDir, "spec", lifecycleNow, out)

			if !c.landed(runDir) || len(out.writes) != 1 || len(out.failures) != 0 {
				t.Errorf("intent landed %v, writes %q, failures %v; want the intent as the one write", c.landed(runDir), out.writes, out.failures)
			}
		})
	}
}

// An intent that cannot be written changes the condition to
// StagingFailed and leaves a failure for the loop to retry.
func TestCarryOutConvergenceReportsAFailedIntent(t *testing.T) {
	cases := []struct {
		name string
		conv convergence
	}{
		{"a reboot", convergence{requestReboot: true}},
		{"a live module load", convergence{requestLoad: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := &passOutcome{}

			condition := carryOutConvergence(c.conv, machine.MachineManifests(t.TempDir()), filepath.Join(t.TempDir(), "missing"), "spec", lifecycleNow, out)

			if condition.Reason != "StagingFailed" || len(out.failures) != 1 {
				t.Errorf("condition %+v, failures %v; want StagingFailed and one failure", condition, out.failures)
			}
		})
	}
}

// A withdrawal or a cleared rejection that cannot write leaves the
// condition as the decision gave it, because the cluster's copy still
// matches this boot, and leaves a failure for the loop to retry.
func TestCarryOutConvergenceRetriesAFailedWithdrawal(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte("kind: Cluster\n")); err != nil {
		t.Fatalf("staging the first document: %v", err)
	}
	if err := store.Reject(machine.Rejection{Hash: "stale", Reason: "the test says so", RejectedAt: lifecycleNow}); err != nil {
		t.Fatalf("recording the rejection: %v", err)
	}
	if err := store.WriteStaged([]byte("kind: Cluster\nmetadata: {name: newer}\n")); err != nil {
		t.Fatalf("staging the newer document: %v", err)
	}
	readOnly(t, filepath.Join(root, "cluster"))
	conv := convergence{
		condition:      converged("ClusterConverged", "Converged", "current"),
		withdraw:       true,
		clearRejection: true,
	}
	out := &passOutcome{}

	condition := carryOutConvergence(conv, store, t.TempDir(), "cluster document", lifecycleNow, out)

	if condition.Reason != "Converged" || len(out.failures) != 2 {
		t.Errorf("condition %+v, failures %v; want Converged and two failures", condition, out.failures)
	}
}
