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

// remoteCommit clones the remote, commits the files, and pushes to the
// branch the volume follows, as a person would. It returns the new
// commit.
func remoteCommit(t *testing.T, remote string, files map[string]string) string {
	t.Helper()
	clone := t.TempDir()
	git(t, clone, "clone", "--quiet", remote, clone)
	commit := commitFiles(t, clone, files)
	git(t, clone, "push", "--quiet", "origin", "main")
	return commit
}

// stagedVolume stages one writeable volume and returns it with the
// stage request, so a test can repeat the call the kubelet makes.
func stagedVolume(
	t *testing.T, n *node, id, url string,
) (*volume, *csi.NodeStageVolumeRequest) {
	t.Helper()
	request := stageRequest(t, id, url, nil)
	if _, err := n.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("staging the volume: %v", err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.staged[id], request
}

// restaged repeats the stage call the kubelet makes when the pod
// starts again, which is when the driver reconciles.
func restaged(t *testing.T, n *node, request *csi.NodeStageVolumeRequest) *volume {
	t.Helper()
	id := request.GetVolumeId()
	n.mu.Lock()
	// disarm stops the old volume's loops, as an unstage does. Without
	// it, the old loop keeps writing the gauges of the new volume.
	if old, found := n.staged[id]; found {
		n.disarm(old)
	}
	delete(n.staged, id)
	n.mu.Unlock()
	if _, err := n.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("staging the volume again: %v", err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.staged[id]
}

// driverCommit makes the commit the driver makes after a pod writes,
// with no class and the driver as author.
func driverCommit(t *testing.T, n *node, vol *volume, files map[string]string) {
	t.Helper()
	writeFiles(t, vol.tree, files)
	n.commit(t.Context(), vol, defaultPolicy())
}

func TestAStageReconcilesTheTreeWithUpstream(t *testing.T) {
	for _, c := range []struct {
		name     string
		local    map[string]string
		upstream map[string]string
		tree     map[string]string
		unpushed int
	}{
		{
			name: "a tree equal to upstream",
			tree: map[string]string{"a.txt": "one"},
		},
		{
			name:     "a tree behind upstream",
			upstream: map[string]string{"b.txt": "two"},
			tree:     map[string]string{"a.txt": "one", "b.txt": "two"},
		},
		{
			name:     "a tree ahead of upstream",
			local:    map[string]string{"c.txt": "three"},
			tree:     map[string]string{"a.txt": "one", "c.txt": "three"},
			unpushed: 1,
		},
		{
			name:     "a tree that moved beside upstream",
			local:    map[string]string{"c.txt": "three"},
			upstream: map[string]string{"b.txt": "two"},
			tree:     map[string]string{"a.txt": "one", "b.txt": "two", "c.txt": "three"},
			unpushed: 1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			remote := bareRemote(t, map[string]string{"a.txt": "one"})
			n, _ := testNode(t, io.Discard)
			vol, request := stagedVolume(t, n, "config", fileURL(remote))
			if c.local != nil {
				driverCommit(t, n, vol, c.local)
			}
			if c.upstream != nil {
				remoteCommit(t, remote, c.upstream)
			}

			again := restaged(t, n, request)

			if got := readTree(t, again.tree); !sameTree(got, c.tree) {
				t.Errorf("the tree has %v, want %v", got, c.tree)
			}
			count, _, err := again.work.unpushed(t.Context(), "main")
			if err != nil {
				t.Fatalf("counting the unpushed commits: %v", err)
			}
			if count != c.unpushed {
				t.Errorf("the tree has %d unpushed commits, want %d", count, c.unpushed)
			}
			if abnormal, message := again.health(); abnormal {
				t.Errorf("the reconciled volume reported %q, want nothing", message)
			}
		})
	}
}

func TestAStageLeavesATreeWithUncommittedWritesAlone(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	n, _ := testNode(t, io.Discard)
	vol, request := stagedVolume(t, n, "config", fileURL(remote))
	writeFiles(t, vol.tree, map[string]string{"draft.txt": "unsaved"})
	remoteCommit(t, remote, map[string]string{"b.txt": "two"})

	again := restaged(t, n, request)

	want := map[string]string{"a.txt": "one", "draft.txt": "unsaved"}
	if got := readTree(t, again.tree); !sameTree(got, want) {
		t.Errorf("the tree has %v, want %v", got, want)
	}
	abnormal, message := again.health()
	if !abnormal || !strings.Contains(message, "1 uncommitted paths") {
		t.Errorf("the volume reported %v %q, want the uncommitted paths", abnormal, message)
	}
}

func TestARebaseThatConflictsKeepsTheLocalCommitsAndDiverges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "config", "")
		vol, request := stagedVolume(t, n, "config", fileURL(remote))
		driverCommit(t, n, vol, map[string]string{"a.txt": "the pod wrote this"})
		remoteCommit(t, remote, map[string]string{"a.txt": "two"})

		again := restaged(t, n, request)

		want := map[string]string{"a.txt": "the pod wrote this"}
		if got := readTree(t, again.tree); !sameTree(got, want) {
			t.Errorf("the tree has %v, want %v", got, want)
		}
		if got := again.sideBranch(); got != "main.config" {
			t.Errorf("the volume pushes to %q, want main.config", got)
		}
		if got := again.work.divergedBranch(t.Context()); got != "main.config" {
			t.Errorf("the git directory records %q, want main.config", got)
		}
		abnormal, message := again.health()
		if !abnormal || !strings.Contains(message, "main.config") || !strings.Contains(message, "Diverged") {
			t.Errorf("the health report is %v, %q, want Diverged with both branches", abnormal, message)
		}
		if got := reasonsOf(t, n); !strings.Contains(got, reasonDiverged) {
			t.Errorf("the events are %q, want %s in them", got, reasonDiverged)
		}
	})
}

// reasonsOf returns the reason of every Event the node posted.
func reasonsOf(t *testing.T, n *node) string {
	t.Helper()
	reasons := []string{}
	for _, one := range eventsOf(t, n) {
		reasons = append(reasons, one.Reason)
	}
	return strings.Join(reasons, " ")
}

func TestARebaseThatCannotStartDiverges(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, vol *volume)
	}{
		{
			name: "a scratch work tree git will not add",
			setup: func(t *testing.T, vol *volume) {
				if err := os.WriteFile(
					filepath.Join(vol.work.gitDir, "worktrees"), nil, 0o600); err != nil {
					t.Fatalf("replacing the worktrees directory with a file: %v", err)
				}
			},
		},
		{
			name: "a branch the driver cannot move",
			setup: func(t *testing.T, vol *volume) {
				if err := os.MkdirAll(filepath.Join(vol.work.gitDir,
					"refs", "heads", "main.lock"), 0o755); err != nil {
					t.Fatalf("creating the lock directory: %v", err)
				}
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			remote := bareRemote(t, map[string]string{"a.txt": "one"})
			logs := &logbook{}
			n, _ := testNode(t, logs)
			vol, request := stagedVolume(t, n, "config", fileURL(remote))
			driverCommit(t, n, vol, map[string]string{"c.txt": "three"})
			remoteCommit(t, remote, map[string]string{"b.txt": "two"})
			c.setup(t, vol)

			again := restaged(t, n, request)

			if got := again.sideBranch(); got != "main.config" {
				t.Errorf("the volume pushes to %q, want main.config", got)
			}
			if !strings.Contains(logs.String(), "The rebase of the tree onto upstream did not complete") {
				t.Errorf("the log is %q, want the rebase that did not complete", logs)
			}
			if !strings.Contains(logs.String(), "claim of the volume was not found") {
				t.Errorf("the log is %q, want the claim it could not find in it", logs)
			}
		})
	}
}

func TestAStageRebasesBesideTheTreeAndLeavesNothing(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	n, _ := testNode(t, io.Discard)
	vol, request := stagedVolume(t, n, "config", fileURL(remote))
	driverCommit(t, n, vol, map[string]string{"c.txt": "three"})
	remoteCommit(t, remote, map[string]string{"b.txt": "two"})

	again := restaged(t, n, request)

	want := map[string]string{"a.txt": "one", "b.txt": "two", "c.txt": "three"}
	if got := readTree(t, again.tree); !sameTree(got, want) {
		t.Errorf("the tree has %v, want %v", got, want)
	}
	if got := again.sideBranch(); got != "" {
		t.Errorf("the volume pushes to %q, want the ref", got)
	}
	if _, err := os.Stat(filepath.Join(again.directory, scratchTree)); !os.IsNotExist(err) {
		t.Errorf("the volume directory has a scratch work tree: %v", err)
	}
}

func TestADriverOutsideAClusterNamesNoClaim(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	n.arming.client = nil
	vol := &volume{id: "config"}
	if got := n.claimFor(t.Context(), vol); got != (claimReference{}) {
		t.Errorf("the volume names the claim %+v, want none", got)
	}
}

func TestAReconcileFailsWhenItCannotUpdatePushedRef(t *testing.T) {
	for _, c := range []struct {
		name  string
		local map[string]string
	}{
		{name: "after it moves to upstream"},
		{name: "after a rebase", local: map[string]string{"c.txt": "three"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			remote := bareRemote(t, map[string]string{"a.txt": "one"})
			n, _ := testNode(t, io.Discard)
			vol, request := stagedVolume(t, n, "config", fileURL(remote))
			if c.local != nil {
				driverCommit(t, n, vol, c.local)
			}
			remoteCommit(t, remote, map[string]string{"b.txt": "two"})
			n.mu.Lock()
			delete(n.staged, "config")
			n.mu.Unlock()
			lockPushedRef(t, n, "config")

			_, err := n.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodeStageVolume returned %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestAReconcileReportsATreeItCannotReset(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	n, _ := testNode(t, io.Discard)
	_, request := stagedVolume(t, n, "config", fileURL(remote))
	remoteCommit(t, remote, map[string]string{"a.txt": "two"})
	n.mu.Lock()
	delete(n.staged, "config")
	n.mu.Unlock()
	readOnlyDir(t, filepath.Join(n.store.volumeDir("config"), "tree"))

	_, err := n.NodeStageVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodeStageVolume returned %v, want %v", got, codes.Internal)
	}
}

func TestAStageThatFindsTheRefDeletedKeepsTheTreeAndPushesNothing(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	n, _ := testNode(t, io.Discard)
	vol, request := stagedVolume(t, n, "config", fileURL(remote))
	driverCommit(t, n, vol, map[string]string{"c.txt": "three"})
	git(t, remote, "update-ref", "-d", "refs/heads/main")

	again := restaged(t, n, request)

	want := map[string]string{"a.txt": "one", "c.txt": "three"}
	if got := readTree(t, again.tree); !sameTree(got, want) {
		t.Errorf("the tree has %v, want %v", got, want)
	}
	abnormal, message := again.health()
	if !abnormal || !strings.Contains(message, "RefDeleted") {
		t.Errorf("the health report is %v, %q, want RefDeleted", abnormal, message)
	}

	n.push(t.Context(), again)
	if _, err := os.Stat(filepath.Join(remote, "refs", "heads", "main")); err == nil {
		t.Error("the driver put the deleted ref back on the remote")
	}
	if _, message := again.health(); !strings.Contains(message, "RefDeleted") {
		t.Errorf("the health report is %q after the push, want RefDeleted", message)
	}
}

func TestAFirstStageOfADeletedRefIsRefused(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	git(t, remote, "update-ref", "-d", "refs/heads/main")
	n, _ := testNode(t, io.Discard)

	_, err := n.NodeStageVolume(t.Context(),
		stageRequest(t, "config", fileURL(remote), nil))
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("NodeStageVolume returned %v, want %v", got, codes.Unavailable)
	}
}

func TestAStageThatFindsTheRefDeletedReportsAHeadItCannotRead(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	n, _ := testNode(t, io.Discard)
	_, request := stagedVolume(t, n, "config", fileURL(remote))
	git(t, remote, "update-ref", "-d", "refs/heads/main")
	n.mu.Lock()
	delete(n.staged, "config")
	n.mu.Unlock()
	// A stage that was interrupted can leave a git directory with its
	// refs missing.
	gitDir := filepath.Join(n.store.volumeDir("config"), "git")
	if err := os.RemoveAll(filepath.Join(gitDir, "refs")); err != nil {
		t.Fatalf("removing the refs: %v", err)
	}

	_, err := n.NodeStageVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodeStageVolume returned %v, want %v", got, codes.Internal)
	}
}
