package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// divergedVolume returns an armed volume that has diverged. A push
// that a rebase fixes would succeed, so the remote refuses every push to
// main for the one push that makes the volume diverge.
func divergedVolume(
	t *testing.T, logs io.Writer,
) (*node, *volume, string, *csi.NodeStageVolumeRequest) {
	t.Helper()
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	n, _ := testNode(t, logs)
	boundVolume(t, n, "config", "config-eager")
	createClass(t, n, "config-eager", nil)
	vol, request := stagedVolume(t, n, "config", fileURL(remote))
	waitForArmed(t, vol, true)
	driverCommit(t, n, vol, map[string]string{"c.txt": "three"})
	remoteCommit(t, remote, map[string]string{"b.txt": "two"})
	declineMain(t, remote)
	n.push(t.Context(), vol)
	acceptMain(t, remote)
	return n, vol, remote, request
}

func branchesOn(t *testing.T, remote string) string {
	t.Helper()
	return git(t, remote, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
}

// mergeSideBranch merges the side branch into main on the remote, as a
// person would on the forge.
func mergeSideBranch(t *testing.T, remote, branch string) {
	t.Helper()
	clone := t.TempDir()
	git(t, clone, "clone", "--quiet", remote, clone)
	git(t, clone, "-c", "user.name=lab", "-c", "user.email=lab@liken.sh",
		"merge", "--quiet", "--no-ff", "-m", "merge", "origin/"+branch)
	git(t, clone, "push", "--quiet", "origin", "main")
}

func TestAPushTheRemoteRejectsDivergesToTheSideBranch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote, _ := divergedVolume(t, io.Discard)

		if got := vol.sideBranch(); got != "main.config" {
			t.Fatalf("the volume pushes to %q, want main.config", got)
		}
		if got := branchesOn(t, remote); !strings.Contains(got, "main.config") {
			t.Errorf("the remote has %q, want the side branch on it", got)
		}
		if got := git(t, remote, "ls-tree", "--name-only", "main.config"); !strings.Contains(got, "c.txt") {
			t.Errorf("the side branch has %q, want the pod's work on it", got)
		}
		count, _, err := vol.work.unpushed(t.Context(), "main")
		if err != nil {
			t.Fatalf("counting the unpushed commits: %v", err)
		}
		if count != 0 {
			t.Errorf("the tree has %d unpushed commits after the push, want 0", count)
		}
		abnormal, message := vol.health()
		if !abnormal || !strings.Contains(message, "Diverged") {
			t.Errorf("the health report is %v, %q, want Diverged", abnormal, message)
		}
		if got := reasonsOf(t, n); !strings.Contains(got, reasonDiverged) {
			t.Errorf("the events are %q, want %s in them", got, reasonDiverged)
		}
		value, found := gaugeOf(t, n.metrics, "git_csi_diverged", "home", "config")
		if !found || value != 1 {
			t.Errorf("git_csi_diverged reads %v (found: %v), want 1", value, found)
		}
	})
}

func TestAVolumeOnItsSideBranchKeepsPushingThere(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote, _ := divergedVolume(t, io.Discard)
		driverCommit(t, n, vol, map[string]string{"d.txt": "four"})

		n.push(t.Context(), vol)

		if got := git(t, remote, "ls-tree", "--name-only", "main.config"); !strings.Contains(got, "d.txt") {
			t.Errorf("the side branch has %q, want the later work on it", got)
		}
		if got := git(t, remote, "ls-tree", "--name-only", "main"); strings.Contains(got, "d.txt") {
			t.Errorf("the ref has %q, want none of the diverged work", got)
		}
	})
}

// waitForGauge waits until every goroutine in the bubble is blocked,
// then fails unless the gauge has a series for the claim. A test of a
// stage needs the wait, because the arming loop finds the claim only
// after the stage returns.
func waitForGauge(t *testing.T, registry *metrics, name, namespace, claim string) float64 {
	t.Helper()
	synctest.Wait()
	value, found := gaugeOf(t, registry, name, namespace, claim)
	if !found {
		t.Fatalf("%s is not on %s/%s", name, namespace, claim)
	}
	return value
}

func TestAStageReturnsAVolumeToItsRefWhenUpstreamHasMerged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _, remote, request := divergedVolume(t, io.Discard)
		mergeSideBranch(t, remote, "main.config")

		again := restaged(t, n, request)

		want := map[string]string{"a.txt": "one", "b.txt": "two", "c.txt": "three"}
		if got := readTree(t, again.tree); !sameTree(got, want) {
			t.Errorf("the tree has %v, want %v", got, want)
		}
		if got := again.sideBranch(); got != "" {
			t.Errorf("the volume on its ref again pushes to %q, want its ref", got)
		}
		if got := again.work.divergedBranch(t.Context()); got != "" {
			t.Errorf("the git directory records %q, want no side branch", got)
		}
		if got := branchesOn(t, remote); strings.Contains(got, "main.config") {
			t.Errorf("the remote has %q, want the side branch deleted", got)
		}
		if abnormal, message := again.health(); abnormal {
			t.Errorf("the volume on its ref again reports the message %q, want none", message)
		}
		if got := reasonsOf(t, n); !strings.Contains(got, reasonHealed) {
			t.Errorf("the events are %q, want %s in them", got, reasonHealed)
		}
		if value := waitForGauge(t, n.metrics, "git_csi_diverged", "home", "config"); value != 0 {
			t.Errorf("git_csi_diverged reads %v, want 0", value)
		}
		count, _, err := again.work.unpushed(t.Context(), "main")
		if err != nil {
			t.Fatalf("counting the unpushed commits: %v", err)
		}
		if count != 0 {
			t.Errorf("the tree on its ref again has %d unpushed commits, want 0", count)
		}
	})
}

func TestAStageReturnsAVolumeToItsRefWhenItsSideBranchIsAlreadyDeleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, _, remote, request := divergedVolume(t, logs)
		mergeSideBranch(t, remote, "main.config")
		git(t, remote, "update-ref", "-d", "refs/heads/main.config")

		again := restaged(t, n, request)

		if got := again.sideBranch(); got != "" {
			t.Errorf("the volume on its ref again pushes to %q, want its ref", got)
		}
		if !strings.Contains(logs.String(), "The side branch was not fetched from the remote") {
			t.Errorf("the log is %q, want the side branch it could not fetch in it", logs)
		}
	})
}

func TestAStageKeepsAVolumeDivergedWhenUpstreamHasNotMerged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _, remote, request := divergedVolume(t, io.Discard)

		again := restaged(t, n, request)

		if got := again.sideBranch(); got != "main.config" {
			t.Errorf("the volume pushes to %q, want main.config", got)
		}
		if got := branchesOn(t, remote); !strings.Contains(got, "main.config") {
			t.Errorf("the remote has %q, want the side branch still on it", got)
		}
	})
}

func TestADivergedStateItCannotWriteIsStillInForce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, vol, _, _ := divergedVolume(t, logs)
		vol.reportReturnedToRef()
		lockTheConfig(t, vol)

		n.diverge(t.Context(), vol)

		if got := vol.sideBranch(); got != "main.config" {
			t.Errorf("the volume pushes to %q, want main.config", got)
		}
		if !strings.Contains(logs.String(), "The diverged record was not written to the volume git config") {
			t.Errorf("the log is %q, want the state it could not write in it", logs)
		}
	})
}

// lockTheConfig creates config.lock in the git directory, so the next
// git config write fails.
func lockTheConfig(t *testing.T, vol *volume) {
	t.Helper()
	lock := filepath.Join(vol.work.gitDir, "config.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("locking the configuration: %v", err)
	}
}

func TestAReturnToTheRefReportsAGitConfigItCannotWrite(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, n *node, vol *volume)
	}{
		{
			name: "a tree it cannot reset",
			setup: func(t *testing.T, _ *node, vol *volume) {
				// A reset that has to restore a deleted file fails on
				// a read-only tree.
				if err := os.Remove(filepath.Join(vol.tree, "b.txt")); err != nil {
					t.Fatalf("removing the file: %v", err)
				}
				readOnlyDir(t, vol.tree)
			},
		},
		{
			name: "a pushedRef it cannot update",
			setup: func(t *testing.T, n *node, _ *volume) {
				lockPushedRef(t, n, "config")
			},
		},
		{
			name: "a state it cannot clear",
			setup: func(t *testing.T, _ *node, vol *volume) {
				lockTheConfig(t, vol)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, vol, remote, request := divergedVolume(t, io.Discard)
				mergeSideBranch(t, remote, "main.config")
				n.mu.Lock()
				delete(n.staged, "config")
				n.mu.Unlock()
				c.setup(t, n, vol)

				_, err := n.NodeStageVolume(t.Context(), request)
				if got := status.Code(err); got != codes.Internal {
					t.Errorf("NodeStageVolume returned %v, want %v", got, codes.Internal)
				}
			})
		})
	}
}

func TestAReturnToTheRefLogsASideBranchItCannotDelete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, vol, _, _ := divergedVolume(t, logs)
		head, err := vol.work.head(t.Context())
		if err != nil {
			t.Fatalf("reading HEAD: %v", err)
		}
		// The arming loop reads the volume's attributes, so the test
		// stops it before pointing the volume at a remote that does not
		// exist, and writes under the volume's lock in case the loop's
		// last pass still holds it.
		n.mu.Lock()
		n.disarm(vol)
		n.mu.Unlock()
		vol.mu.Lock()
		vol.attributes = &attributes{url: fileURL(filepath.Join(t.TempDir(), "gone")), ref: "main"}
		vol.mu.Unlock()

		if err := n.resetToUpstream(t.Context(), vol, "main.config", head, "main.config"); err != nil {
			t.Fatalf("healing the volume: %v", err)
		}
		if !strings.Contains(logs.String(), "The side branch was not deleted from the remote") {
			t.Errorf("the log is %q, want the branch it could not delete in it", logs)
		}
		if got := vol.sideBranch(); got != "" {
			t.Errorf("the volume on its ref again pushes to %q, want its ref", got)
		}
	})
}

func TestADeletionWithNoCredentialItCanWriteFails(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	vol := &volume{
		id:          "config",
		attributes:  &attributes{url: "file:///nowhere", ref: "main"},
		credentials: &credentials{privateKey: "a key"},
		directory:   filepath.Join(t.TempDir(), "gone"),
	}
	if err := n.deleteSide(t.Context(), vol, "main.config"); err == nil {
		t.Error("deleteSide returned no error for a credential it cannot write, want one")
	}
}

func TestARejectionIsFoundInGitsStderr(t *testing.T) {
	for _, c := range []struct {
		name   string
		stderr string
		want   bool
	}{
		{
			name:   "a branch behind its remote counterpart",
			stderr: " ! [rejected]        main -> main (non-fast-forward)\nhint: a hint\n",
			want:   true,
		},
		{
			name:   "a remote that has work the tree does not",
			stderr: " ! [rejected] main -> main (fetch first)\n",
			want:   true,
		},
		{
			name:   "a forge that is not there",
			stderr: "fatal: could not read from remote repository\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := rejectedPush(gitOutput{stderr: c.stderr}); got != c.want {
				t.Errorf("rejectedPush returned %v, want %v", got, c.want)
			}
		})
	}
}

func TestADivergedVolumeReturnsToItsRefAtItsNextPush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote, _ := divergedVolume(t, io.Discard)
		mergeSideBranch(t, remote, "main.config")
		driverCommit(t, n, vol, map[string]string{"d.txt": "four"})

		n.push(t.Context(), vol)

		if got := vol.sideBranch(); got != "" {
			t.Errorf("the volume on its ref again pushes to %q, want its ref", got)
		}
		if got := vol.work.divergedBranch(t.Context()); got != "" {
			t.Errorf("the git directory records %q, want no side branch", got)
		}
		if got := branchesOn(t, remote); strings.Contains(got, "main.config") {
			t.Errorf("the remote has %q, want the side branch deleted", got)
		}
		if got := git(t, remote, "ls-tree", "--name-only", "main"); !strings.Contains(got, "d.txt") {
			t.Errorf("the ref has %q, want the work the pod wrote after the merge", got)
		}
		want := map[string]string{"a.txt": "one", "b.txt": "two", "c.txt": "three", "d.txt": "four"}
		if got := readTree(t, vol.tree); !sameTree(got, want) {
			t.Errorf("the tree has %v, want %v", got, want)
		}
		if got := reasonsOf(t, n); !strings.Contains(got, reasonHealed) {
			t.Errorf("the events are %q, want %s in them", got, reasonHealed)
		}
		value, found := gaugeOf(t, n.metrics, "git_csi_diverged", "home", "config")
		if !found || value != 0 {
			t.Errorf("git_csi_diverged reads %v (found: %v), want 0", value, found)
		}
		count, _, err := vol.work.unpushed(t.Context(), "main")
		if err != nil {
			t.Fatalf("counting the unpushed commits: %v", err)
		}
		if count != 0 {
			t.Errorf("the tree on its ref again has %d unpushed commits, want 0", count)
		}
	})
}

func TestADivergedVolumeReturnsToItsRefWhenItsSideBranchIsAlreadyDeleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote, _ := divergedVolume(t, io.Discard)
		mergeSideBranch(t, remote, "main.config")
		git(t, remote, "update-ref", "-d", "refs/heads/main.config")
		driverCommit(t, n, vol, map[string]string{"d.txt": "four"})

		n.push(t.Context(), vol)

		if got := vol.sideBranch(); got != "" {
			t.Errorf("the volume on its ref again pushes to %q, want its ref", got)
		}
		if got := git(t, remote, "ls-tree", "--name-only", "main"); !strings.Contains(got, "d.txt") {
			t.Errorf("the ref has %q, want the work the pod wrote after the merge", got)
		}
	})
}

func TestADivergedVolumeThatCannotReturnToItsRefKeepsPushingToItsSideBranch(t *testing.T) {
	for _, c := range []struct {
		name   string
		merged bool
		setup  func(t *testing.T, n *node, vol *volume)
	}{
		{
			name:  "an upstream that does not hold the work",
			setup: func(*testing.T, *node, *volume) {},
		},
		{
			name:   "a fetch the store cannot hold",
			merged: true,
			setup: func(t *testing.T, n *node, vol *volume) {
				where := filepath.Join(n.store.repository(vol.attributes.url).dir,
					"FETCH_HEAD")
				if err := os.Remove(where); err != nil {
					t.Fatalf("removing the fetch record: %v", err)
				}
				if err := os.MkdirAll(where, 0o755); err != nil {
					t.Fatalf("making the fetch directory: %v", err)
				}
			},
		},
		{
			name:   "a scratch work tree git will not add",
			merged: true,
			setup: func(t *testing.T, _ *node, vol *volume) {
				if err := os.WriteFile(
					filepath.Join(vol.work.gitDir, "worktrees"), nil, 0o600); err != nil {
					t.Fatalf("writing the worktrees directory as a file: %v", err)
				}
			},
		},
		{
			name:   "a state it cannot clear",
			merged: true,
			setup: func(t *testing.T, _ *node, vol *volume) {
				lockTheConfig(t, vol)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, vol, remote, _ := divergedVolume(t, io.Discard)
				if c.merged {
					mergeSideBranch(t, remote, "main.config")
				}
				driverCommit(t, n, vol, map[string]string{"d.txt": "four"})
				c.setup(t, n, vol)

				n.push(t.Context(), vol)

				if got := vol.sideBranch(); got != "main.config" {
					t.Errorf("the volume pushes to %q, want main.config", got)
				}
				if got := git(t, remote, "ls-tree", "--name-only", "main.config"); !strings.Contains(got, "d.txt") {
					t.Errorf("the side branch has %q, want the later work on it", got)
				}
			})
		})
	}
}
