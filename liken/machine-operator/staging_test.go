package main

// The staging half of convergence, tested against real files:
// carrying out a convergence decision against a manifest store, and
// reading the staged document's identity back out of one.

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
		t.Errorf("the decision's condition comes back: %s", condition.Reason)
	}
	staged, err := store.LoadStaged()
	if err != nil || string(staged) != "kind: Cluster\n" {
		t.Errorf("the manifest should be staged: %q, %v", staged, err)
	}
}

func TestCarryOutConvergenceWithdrawsAndClears(t *testing.T) {
	store := machine.ClusterManifests(t.TempDir())
	if err := store.WriteStaged([]byte("kind: Cluster\n")); err != nil {
		t.Fatal(err)
	}
	if err := store.Reject(machine.Rejection{Hash: "stale", Reason: "the test says so", RejectedAt: lifecycleNow}); err != nil {
		t.Fatal(err)
	}
	// The rejection moved the staged document aside, so a newer one is
	// staged beside the record.
	if err := store.WriteStaged([]byte("kind: Cluster\nmetadata: {name: newer}\n")); err != nil {
		t.Fatal(err)
	}
	conv := convergence{
		condition:      converged("ClusterConverged", "Converged", "current"),
		withdraw:       true,
		clearRejection: true,
	}
	carryOutConvergence(conv, store, t.TempDir(), "cluster document", lifecycleNow, nil)
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged copy should be withdrawn")
	}
	if rejection, _ := store.LoadRejection(); rejection != nil {
		t.Error("the rejection record should be cleared")
	}
}

func TestCarryOutConvergenceReportsAFailedStaging(t *testing.T) {
	// A store rooted somewhere unwritable cannot stage. The
	// condition downgrades to StagingFailed, instead of reporting a
	// reboot that will not happen.
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
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
		t.Errorf("got %s", condition.Reason)
	}
}

func TestCarryOutConvergenceReportsAFailedRestartRequest(t *testing.T) {
	// The intent channel directory belongs to init to create. On a
	// machine where it is missing, the request must downgrade to
	// StagingFailed, instead of reporting a restart that will not
	// happen. This is the same behavior a failed staging takes.
	store := machine.RegistryCredentialsStore(t.TempDir())
	conv := convergence{
		condition:      notConverged("CredentialsConverged", "RestartRequested", "restarting"),
		requestRestart: true,
		hash:           "abc123",
	}
	condition := carryOutConvergence(conv, store, filepath.Join(t.TempDir(), "missing"), "registry credentials", lifecycleNow, nil)
	if condition.Reason != "StagingFailed" {
		t.Errorf("got %s", condition.Reason)
	}
}

func TestReadStagedHashOfAnEmptyStore(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	if hash := readStagedHash(store); hash != "" {
		t.Errorf("an empty store stages nothing: %q", hash)
	}
}

func TestReadStagedHashIsTheStagedBytesIdentity(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	raw := []byte("kind: Machine\nmetadata: {name: liken-dev}\n")
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	if hash := readStagedHash(store); hash != machine.ManifestHash(raw) {
		t.Errorf("got %q", hash)
	}
}

func TestReadStagedHashHashesUnparseableBytesToo(t *testing.T) {
	// The idempotence check compares bytes, not parsed meaning, so
	// staged bytes that fail to parse still get an identity.
	store := machine.MachineManifests(t.TempDir())
	raw := []byte(":: this is not yaml ::")
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	if hash := readStagedHash(store); hash != machine.ManifestHash(raw) {
		t.Errorf("got %q", hash)
	}
}

// Each kind of disruption a decision asks for lands as the intent file
// init acts on, and counts as a write.
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

// An intent that cannot be written downgrades the condition to
// StagingFailed and leaves a failure for the loop to retry, so the
// condition never reports a disruption that will not happen.
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
func TestCarryOutConvergenceRetriesAFailedTidy(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte("kind: Cluster\n")); err != nil {
		t.Fatal(err)
	}
	if err := store.Reject(machine.Rejection{Hash: "stale", Reason: "the test says so", RejectedAt: lifecycleNow}); err != nil {
		t.Fatal(err)
	}
	// The rejection moved the staged document aside, so a newer one is
	// staged beside the record.
	if err := store.WriteStaged([]byte("kind: Cluster\nmetadata: {name: newer}\n")); err != nil {
		t.Fatal(err)
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
