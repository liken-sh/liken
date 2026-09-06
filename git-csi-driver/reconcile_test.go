package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// remoteCommit is a person committing on the forge: a clone, a
// commit, and a push to the branch the volume follows.
func remoteCommit(t *testing.T, remote string, files map[string]string) string {
	t.Helper()
	clone := t.TempDir()
	git(t, clone, "clone", "--quiet", remote, clone)
	commit := commitFiles(t, clone, files)
	git(t, clone, "push", "--quiet", "origin", "main")
	return commit
}

// stagedVolume stages one writeable volume and answers it with
// the call the kubelet would make again.
func stagedVolume(
	t *testing.T, answering *node, id, url string,
) (*volume, *csi.NodeStageVolumeRequest) {
	t.Helper()
	request := stageRequest(t, id, url, nil)
	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	return answering.staged[id], request
}

// restaged is the stage the kubelet makes when the pod starts
// again, which is the one moment the driver reconciles.
func restaged(t *testing.T, answering *node, request *csi.NodeStageVolumeRequest) *volume {
	t.Helper()
	id := request.GetVolumeId()
	answering.mu.Lock()
	// An unstage ends the loops that read the claim, or the old volume's
	// loop would keep writing the gauges the new one reports on.
	if old, found := answering.staged[id]; found {
		answering.disarm(old)
	}
	delete(answering.staged, id)
	answering.mu.Unlock()
	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("NodeStageVolume again: %v", err)
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	return answering.staged[id]
}

// driverCommit is the commit the driver makes for a pod that
// wrote, with no class and the driver's own author.
func driverCommit(t *testing.T, answering *node, held *volume, files map[string]string) {
	t.Helper()
	writeFiles(t, held.tree, files)
	answering.commit(t.Context(), held, defaultPolicy())
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
			answering, _ := testNode(t, io.Discard)
			held, request := stagedVolume(t, answering, "config", fileURL(remote))
			if c.local != nil {
				driverCommit(t, answering, held, c.local)
			}
			if c.upstream != nil {
				remoteCommit(t, remote, c.upstream)
			}

			again := restaged(t, answering, request)

			if got := readTree(t, again.tree); !sameTree(got, c.tree) {
				t.Errorf("the tree holds %v, want %v", got, c.tree)
			}
			count, _, err := again.work.unpushed(t.Context(), "main")
			if err != nil {
				t.Fatalf("unpushed: %v", err)
			}
			if count != c.unpushed {
				t.Errorf("the tree holds %d unpushed commits, want %d", count, c.unpushed)
			}
			if abnormal, message := again.report(); abnormal {
				t.Errorf("the reconciled volume reported %q", message)
			}
		})
	}
}

// A tree with writes no commit carries is never reset or rebased, so an
// unarmed volume's work survives a stage that finds upstream moved.
func TestAStageLeavesATreeWithUncommittedWritesAlone(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, io.Discard)
	held, request := stagedVolume(t, answering, "config", fileURL(remote))
	writeFiles(t, held.tree, map[string]string{"draft.txt": "unsaved"})
	remoteCommit(t, remote, map[string]string{"b.txt": "two"})

	again := restaged(t, answering, request)

	want := map[string]string{"a.txt": "one", "draft.txt": "unsaved"}
	if got := readTree(t, again.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	abnormal, message := again.report()
	if !abnormal || !strings.Contains(message, "1 uncommitted paths") {
		t.Errorf("the volume reported %v %q, want the uncommitted paths", abnormal, message)
	}
}

func TestARebaseThatConflictsKeepsTheLocalCommitsAndDiverges(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "")
	held, request := stagedVolume(t, answering, "config", fileURL(remote))
	driverCommit(t, answering, held, map[string]string{"a.txt": "the pod wrote this"})
	remoteCommit(t, remote, map[string]string{"a.txt": "two"})

	again := restaged(t, answering, request)

	want := map[string]string{"a.txt": "the pod wrote this"}
	if got := readTree(t, again.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	if got := again.divergedFrom(); got != "main.config" {
		t.Errorf("the volume pushes to %q, want main.config", got)
	}
	if got := again.work.divergedBranch(t.Context()); got != "main.config" {
		t.Errorf("the git directory records %q, want main.config", got)
	}
	abnormal, message := again.report()
	if !abnormal || !strings.Contains(message, "main.config") || !strings.Contains(message, "Diverged") {
		t.Errorf("the condition says %v, %q, want Diverged with both branches", abnormal, message)
	}
	if got := reasonsOf(t, answering); !strings.Contains(got, reasonDiverged) {
		t.Errorf("the events are %q, want %s in them", got, reasonDiverged)
	}
}

// reasonsOf is every reason the node posted, which is what a
// person reading kubectl describe sees.
func reasonsOf(t *testing.T, answering *node) string {
	t.Helper()
	reasons := []string{}
	for _, one := range eventsOf(t, answering) {
		reasons = append(reasons, one.Reason)
	}
	return strings.Join(reasons, " ")
}

func TestARebaseThatCannotStartDiverges(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	held, request := stagedVolume(t, answering, "config", fileURL(remote))
	driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
	remoteCommit(t, remote, map[string]string{"b.txt": "two"})
	// A rebase left half done by a driver that died is a rebase no new
	// rebase will start beside.
	if err := os.MkdirAll(filepath.Join(held.work.gitDir, "rebase-merge"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	again := restaged(t, answering, request)

	if got := again.divergedFrom(); got != "main.config" {
		t.Errorf("the volume pushes to %q, want main.config", got)
	}
	if !strings.Contains(logs.String(), "the rebase was aborted") {
		t.Errorf("the log is %q, want the aborted rebase in it", logs)
	}
	if !strings.Contains(logs.String(), "the volume names no claim") {
		t.Errorf("the log is %q, want the claim it could not find in it", logs)
	}
}

func TestADriverOutsideAClusterNamesNoClaim(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.arms.client = nil
	held := &volume{id: "config"}
	if got := answering.claimFor(t.Context(), held); got != (claimReference{}) {
		t.Errorf("the volume names the claim %+v, want none", got)
	}
}

func TestAReconcileReportsAMarkItCannotMove(t *testing.T) {
	for _, c := range []struct {
		name  string
		local map[string]string
	}{
		{name: "after it takes upstream"},
		{name: "after a rebase", local: map[string]string{"c.txt": "three"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			remote := bareRemote(t, map[string]string{"a.txt": "one"})
			answering, _ := testNode(t, io.Discard)
			held, request := stagedVolume(t, answering, "config", fileURL(remote))
			if c.local != nil {
				driverCommit(t, answering, held, c.local)
			}
			remoteCommit(t, remote, map[string]string{"b.txt": "two"})
			answering.mu.Lock()
			delete(answering.staged, "config")
			answering.mu.Unlock()
			lockTheMark(t, answering, "config")

			_, err := answering.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestAReconcileReportsATreeItCannotReset(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, io.Discard)
	_, request := stagedVolume(t, answering, "config", fileURL(remote))
	remoteCommit(t, remote, map[string]string{"a.txt": "two"})
	answering.mu.Lock()
	delete(answering.staged, "config")
	answering.mu.Unlock()
	readOnlyDir(t, filepath.Join(answering.store.volumeDir("config"), "tree"))

	_, err := answering.NodeStageVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Internal)
	}
}

func TestAStageThatFindsTheRefDeletedKeepsTheTreeAndPushesNothing(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, io.Discard)
	held, request := stagedVolume(t, answering, "config", fileURL(remote))
	driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
	git(t, remote, "update-ref", "-d", "refs/heads/main")

	again := restaged(t, answering, request)

	want := map[string]string{"a.txt": "one", "c.txt": "three"}
	if got := readTree(t, again.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	abnormal, message := again.report()
	if !abnormal || !strings.Contains(message, "RefDeleted") {
		t.Errorf("the condition says %v, %q, want RefDeleted", abnormal, message)
	}

	answering.push(t.Context(), again)
	if _, err := os.Stat(filepath.Join(remote, "refs", "heads", "main")); err == nil {
		t.Error("the driver put the deleted ref back on the remote")
	}
	if _, message := again.report(); !strings.Contains(message, "RefDeleted") {
		t.Errorf("the condition says %q after the push, want RefDeleted", message)
	}
}

func TestAFirstStageOfADeletedRefIsRefused(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	git(t, remote, "update-ref", "-d", "refs/heads/main")
	answering, _ := testNode(t, io.Discard)

	_, err := answering.NodeStageVolume(t.Context(),
		stageRequest(t, "config", fileURL(remote), nil))
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Unavailable)
	}
}

func TestAStageThatFindsTheRefDeletedReportsAHeadItCannotRead(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, io.Discard)
	_, request := stagedVolume(t, answering, "config", fileURL(remote))
	git(t, remote, "update-ref", "-d", "refs/heads/main")
	answering.mu.Lock()
	delete(answering.staged, "config")
	answering.mu.Unlock()
	// A git directory whose refs are gone is what a stage that was
	// cut off leaves.
	gitDir := filepath.Join(answering.store.volumeDir("config"), "git")
	if err := os.RemoveAll(filepath.Join(gitDir, "refs")); err != nil {
		t.Fatalf("removing the refs: %v", err)
	}

	_, err := answering.NodeStageVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Internal)
	}
}
