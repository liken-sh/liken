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

// divergedVolume is an armed volume whose push the remote
// rejected, which is the one way a volume takes a side branch outside a
// stage.
func divergedVolume(
	t *testing.T, logs io.Writer,
) (*node, *volume, string, *csi.NodeStageVolumeRequest) {
	t.Helper()
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, logs)
	boundVolume(t, answering, "config", "config-eager")
	armingClass(t, answering, "config-eager", nil)
	held, request := stagedVolume(t, answering, "config", fileURL(remote))
	waitForArmed(t, held, true)
	driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
	remoteCommit(t, remote, map[string]string{"b.txt": "two"})
	answering.push(t.Context(), held)
	return answering, held, remote, request
}

// branchesOn is every branch the remote holds.
func branchesOn(t *testing.T, remote string) string {
	t.Helper()
	return git(t, remote, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
}

// mergeSideBranch is the person on the forge: a clone, a merge of
// the side branch into the ref, and a push.
func mergeSideBranch(t *testing.T, remote, branch string) {
	t.Helper()
	clone := t.TempDir()
	git(t, clone, "clone", "--quiet", remote, clone)
	git(t, clone, "-c", "user.name=lab", "-c", "user.email=lab@liken.sh",
		"merge", "--quiet", "--no-ff", "-m", "merge", "origin/"+branch)
	git(t, clone, "push", "--quiet", "origin", "main")
}

func TestAPushTheRemoteRejectsTakesTheSideBranch(t *testing.T) {
	answering, held, remote, _ := divergedVolume(t, io.Discard)

	if got := held.divergedFrom(); got != "main.config" {
		t.Fatalf("the volume pushes to %q, want main.config", got)
	}
	if got := branchesOn(t, remote); !strings.Contains(got, "main.config") {
		t.Errorf("the remote holds %q, want the side branch on it", got)
	}
	if got := git(t, remote, "ls-tree", "--name-only", "main.config"); !strings.Contains(got, "c.txt") {
		t.Errorf("the side branch holds %q, want the pod's work on it", got)
	}
	count, _, err := held.work.unpushed(t.Context(), "main")
	if err != nil {
		t.Fatalf("unpushed: %v", err)
	}
	if count != 0 {
		t.Errorf("the tree holds %d unpushed commits after the push, want 0", count)
	}
	abnormal, message := held.report()
	if !abnormal || !strings.Contains(message, "Diverged") {
		t.Errorf("the condition says %v, %q, want Diverged", abnormal, message)
	}
	if got := reasonsOf(t, answering); !strings.Contains(got, reasonDiverged) {
		t.Errorf("the events are %q, want %s in them", got, reasonDiverged)
	}
	value, found := gaugeOf(t, answering.readings, "git_csi_diverged", "home", "config")
	if !found || value != 1 {
		t.Errorf("git_csi_diverged reads %v (found: %v), want 1", value, found)
	}
}

func TestAVolumeOnItsSideBranchKeepsPushingThere(t *testing.T) {
	answering, held, remote, _ := divergedVolume(t, io.Discard)
	driverCommit(t, answering, held, map[string]string{"d.txt": "four"})

	answering.push(t.Context(), held)

	if got := git(t, remote, "ls-tree", "--name-only", "main.config"); !strings.Contains(got, "d.txt") {
		t.Errorf("the side branch holds %q, want the later work on it", got)
	}
	if got := git(t, remote, "ls-tree", "--name-only", "main"); strings.Contains(got, "d.txt") {
		t.Errorf("the ref holds %q, want none of the diverged work", got)
	}
}

func TestAStageHealsAVolumeUpstreamHasMerged(t *testing.T) {
	answering, _, remote, request := divergedVolume(t, io.Discard)
	mergeSideBranch(t, remote, "main.config")

	again := restaged(t, answering, request)

	want := map[string]string{"a.txt": "one", "b.txt": "two", "c.txt": "three"}
	if got := readTree(t, again.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	if got := again.divergedFrom(); got != "" {
		t.Errorf("the healed volume pushes to %q, want its ref", got)
	}
	if got := again.work.divergedBranch(t.Context()); got != "" {
		t.Errorf("the git directory records %q, want no side branch", got)
	}
	if got := branchesOn(t, remote); strings.Contains(got, "main.config") {
		t.Errorf("the remote holds %q, want the side branch deleted", got)
	}
	if abnormal, message := again.report(); abnormal {
		t.Errorf("the healed volume reported %q", message)
	}
	if got := reasonsOf(t, answering); !strings.Contains(got, reasonHealed) {
		t.Errorf("the events are %q, want %s in them", got, reasonHealed)
	}
	value, found := gaugeOf(t, answering.readings, "git_csi_diverged", "home", "config")
	if !found || value != 0 {
		t.Errorf("git_csi_diverged reads %v (found: %v), want 0", value, found)
	}
	count, _, err := again.work.unpushed(t.Context(), "main")
	if err != nil {
		t.Fatalf("unpushed: %v", err)
	}
	if count != 0 {
		t.Errorf("the healed tree holds %d unpushed commits, want 0", count)
	}
}

func TestAStageHealsAVolumeWhoseSideBranchIsAlreadyDeleted(t *testing.T) {
	logs := &logbook{}
	answering, _, remote, request := divergedVolume(t, logs)
	mergeSideBranch(t, remote, "main.config")
	git(t, remote, "update-ref", "-d", "refs/heads/main.config")

	again := restaged(t, answering, request)

	if got := again.divergedFrom(); got != "" {
		t.Errorf("the healed volume pushes to %q, want its ref", got)
	}
	if !strings.Contains(logs.String(), "the remote holds no side branch") {
		t.Errorf("the log is %q, want the side branch it could not fetch in it", logs)
	}
}

func TestAStageHoldsAVolumeUpstreamHasNotMerged(t *testing.T) {
	answering, _, remote, request := divergedVolume(t, io.Discard)

	again := restaged(t, answering, request)

	if got := again.divergedFrom(); got != "main.config" {
		t.Errorf("the volume pushes to %q, want main.config", got)
	}
	if got := branchesOn(t, remote); !strings.Contains(got, "main.config") {
		t.Errorf("the remote holds %q, want the side branch still on it", got)
	}
}

func TestADivergedStateItCannotWriteIsStillInForce(t *testing.T) {
	logs := &logbook{}
	answering, held, _, _ := divergedVolume(t, logs)
	held.reportHealed()
	lockTheConfig(t, held)

	answering.diverge(t.Context(), held)

	if got := held.divergedFrom(); got != "main.config" {
		t.Errorf("the volume pushes to %q, want main.config", got)
	}
	if !strings.Contains(logs.String(), "the diverged state was not written") {
		t.Errorf("the log is %q, want the state it could not write in it", logs)
	}
}

// lockTheConfig writes the lock file git takes before it changes
// a configuration file, so the next change fails.
func lockTheConfig(t *testing.T, held *volume) {
	t.Helper()
	lock := filepath.Join(held.work.gitDir, "config.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("locking the configuration: %v", err)
	}
}

func TestAHealReportsWhatItCannotWrite(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node, held *volume)
	}{
		{
			name: "a tree it cannot reset",
			stand: func(t *testing.T, _ *node, held *volume) {
				readOnlyDir(t, held.tree)
			},
		},
		{
			name: "a mark it cannot move",
			stand: func(t *testing.T, answering *node, _ *volume) {
				lockTheMark(t, answering, "config")
			},
		},
		{
			name: "a state it cannot clear",
			stand: func(t *testing.T, _ *node, held *volume) {
				lockTheConfig(t, held)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, held, remote, request := divergedVolume(t, io.Discard)
			mergeSideBranch(t, remote, "main.config")
			answering.mu.Lock()
			delete(answering.staged, "config")
			answering.mu.Unlock()
			c.stand(t, answering, held)

			_, err := answering.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestAHealReportsASideBranchItCannotDelete(t *testing.T) {
	logs := &logbook{}
	answering, held, _, _ := divergedVolume(t, logs)
	head, err := held.work.head(t.Context())
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	// The arming loop reads what the volume reports, and that reads the
	// attributes a stage set once, so the loop is stopped before the test
	// points the volume at a forge that is not there. The loop's last
	// pass may still be reading under the volume's lock, so the test
	// writes under the same lock.
	answering.mu.Lock()
	answering.disarm(held)
	answering.mu.Unlock()
	held.mu.Lock()
	held.attributes = &attributes{url: fileURL(filepath.Join(t.TempDir(), "gone")), ref: "main"}
	held.mu.Unlock()

	if err := answering.heal(t.Context(), held, "main.config", head, "main.config"); err != nil {
		t.Fatalf("heal: %v", err)
	}
	if !strings.Contains(logs.String(), "the side branch stayed on the remote") {
		t.Errorf("the log is %q, want the branch it could not delete in it", logs)
	}
	if got := held.divergedFrom(); got != "" {
		t.Errorf("the healed volume pushes to %q, want its ref", got)
	}
}

func TestADeletionWithNoCredentialItCanWriteFails(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	held := &volume{
		id:          "config",
		attributes:  &attributes{url: "file:///nowhere", ref: "main"},
		credentials: &credentials{privateKey: "a key"},
		directory:   filepath.Join(t.TempDir(), "gone"),
	}
	if err := answering.deleteSide(t.Context(), held, "main.config"); err == nil {
		t.Error("deleteSide answered no error for a credential it cannot write")
	}
}

func TestARejectionIsWhatGitSaysAboutIt(t *testing.T) {
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
			name:   "a remote that holds work the tree does not",
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
				t.Errorf("rejectedPush answered %v, want %v", got, c.want)
			}
		})
	}
}
