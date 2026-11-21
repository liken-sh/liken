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

// stagedPaths stages and publishes a writeable volume with the paths
// attribute, and returns the node's volume and the stage request.
func stagedPaths(
	t *testing.T, n *node, id, url, paths string,
) (*volume, *csi.NodeStageVolumeRequest) {
	t.Helper()
	staged := stageRequest(t, id, url, map[string]string{"paths": paths})
	if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("calling NodeStageVolume: %v", err)
	}
	if _, err := n.NodePublishVolume(t.Context(), persistentPublish(t, staged)); err != nil {
		t.Fatalf("calling NodePublishVolume: %v", err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.volumes[id], staged
}

// armedPaths is stagedPaths for a volume whose claim names a class with
// the parameters. It waits until the class has armed the volume, and
// stops the volume's loops, so the test runs each commit and push.
func armedPaths(
	t *testing.T, n *node, url, paths string, parameters map[string]string,
) (*volume, *csi.NodeStageVolumeRequest) {
	t.Helper()
	boundVolume(t, n, "config", "config-eager")
	createClass(t, n, "config-eager", parameters)
	vol, staged := stagedPaths(t, n, "config", url, paths)
	waitForArmed(t, vol, true)
	unwatched(t, n, vol)
	return vol, staged
}

// withPaths returns a copy of the stage request with other paths, as a
// new PersistentVolume with the same volumeHandle sends. An empty value
// removes the attribute.
func withPaths(request *csi.NodeStageVolumeRequest, paths string) *csi.NodeStageVolumeRequest {
	context := map[string]string{}
	for key, value := range request.GetVolumeContext() {
		context[key] = value
	}
	delete(context, "paths")
	if paths != "" {
		context["paths"] = paths
	}
	return &csi.NodeStageVolumeRequest{
		VolumeId:          request.GetVolumeId(),
		StagingTargetPath: request.GetStagingTargetPath(),
		VolumeContext:     context,
		VolumeCapability:  request.GetVolumeCapability(),
	}
}

// appRemote is a configuration repository with a directory for each
// application and files at the root.
func appRemote(t *testing.T) string {
	t.Helper()
	return bareRemote(t, map[string]string{
		"README.md":         "root",
		"app-a/config.yaml": "a",
		"app-b/config.yaml": "b",
	})
}

func TestAWriteableVolumeStagesAnEntryTheCommitDoesNotHave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol, _ := stagedPaths(t, n, "config", fileURL(appRemote(t)), "apps/new")

		if got := readTree(t, vol.tree); len(got) != 0 {
			t.Errorf("the tree has %v, want nothing for an entry the commit does not have", got)
		}
	})
}

// The kubelet posts a refused stage on the pod as FailedMount, so the
// error is the whole report.
func TestAWriteableVolumeRefusesAnEntryOutsideTheRepository(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	staged := stageRequest(t, "config", fileURL(appRemote(t)), map[string]string{"paths": "app-a,../x"})

	_, err := n.NodeStageVolume(t.Context(), staged)

	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("the stage returned %v, want InvalidArgument", err)
	}
	if want := `paths: "../x" has an empty, . or .. component`; status.Convert(err).Message() != want {
		t.Errorf("the message is %q, want %q", status.Convert(err).Message(), want)
	}
	if _, err := os.Stat(n.store.volumeDir("config")); err == nil {
		t.Error("the refused stage made a volume directory, want nothing on the node")
	}
}

func TestAWriteOutsideThePathsIsNeverCommitted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol, _ := armedPaths(t, n, fileURL(appRemote(t)), "app-a", nil)
		before := vol.work.refCommit(t.Context(), "HEAD")
		writeFiles(t, vol.tree, map[string]string{
			"README.md":         "a tracked file outside",
			"app-b/config.yaml": "another tracked file outside",
			"log.txt":           "an untracked file outside",
		})

		n.commit(t.Context(), vol, vol.policyNow())

		if after := vol.work.refCommit(t.Context(), "HEAD"); after != before {
			t.Errorf("the tree moved to %s, want no commit from writes outside the paths", after)
		}
		if found, _ := vol.work.pending(t.Context()); len(found) != 0 {
			t.Errorf("the tree has %v pending, want nothing", found)
		}
	})
}

func TestAnArmedVolumeWithPathsPushesOnlyChangesInsideThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := appRemote(t)
		n, _ := testNode(t, io.Discard)
		vol, _ := armedPaths(t, n, fileURL(remote), "app-a", nil)
		writeFiles(t, vol.tree, map[string]string{
			"app-a/new.yaml": "inside",
			"README.md":      "outside",
			"log.txt":        "outside",
		})

		n.commit(t.Context(), vol, vol.policyNow())
		n.push(t.Context(), vol)

		if got := git(t, remote, "log", "--format=%B", "-1", "main"); got != "Update 1 paths\n\napp-a/new.yaml\n\n" {
			t.Errorf("the remote's last commit is %q, want only app-a/new.yaml", got)
		}
		if got := git(t, remote, "show", "main:README.md"); got != "root" {
			t.Errorf("the remote's README.md is %q, want it unchanged", got)
		}
		if got := git(t, remote, "ls-tree", "--name-only", "main"); strings.Contains(got, "log.txt") {
			t.Errorf("the remote's tree is %q, want no log.txt", got)
		}
	})
}

func TestARebaseOntoChangesOutsideThePathsKeepsThemOutOfTheTree(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := appRemote(t)
		n, _ := testNode(t, io.Discard)
		vol, _ := armedPaths(t, n, fileURL(remote), "app-a", nil)
		writeFiles(t, vol.tree, map[string]string{"app-a/new.yaml": "inside"})
		n.commit(t.Context(), vol, vol.policyNow())
		remoteCommit(t, remote, map[string]string{"app-b/config.yaml": "b2", "app-c/new.yaml": "c"})

		n.push(t.Context(), vol)

		if got := len(eventsWithReason(t, n, reasonRebased)); got != 2 {
			t.Errorf("the push posted %d rebases, want one on the pod and one on the claim", got)
		}
		want := map[string]string{"app-a/config.yaml": "a", "app-a/new.yaml": "inside"}
		if got := readTree(t, vol.tree); !sameTree(got, want) {
			t.Errorf("the tree has %v, want only the files inside the paths", got)
		}
		if got := git(t, remote, "show", "main:app-b/config.yaml"); got != "b2" {
			t.Errorf("the remote's app-b/config.yaml is %q, want the other writer's", got)
		}
		if got := git(t, remote, "show", "main:app-a/new.yaml"); got != "inside" {
			t.Errorf("the remote's app-a/new.yaml is %q, want this volume's", got)
		}
		if found, _ := vol.work.pending(t.Context()); len(found) != 0 {
			t.Errorf("the tree has %v pending after the rebase, want nothing", found)
		}
	})
}

func TestAPodThatDeletesItsWholeEntryStagesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := appRemote(t)
		n, _ := testNode(t, io.Discard)
		vol, request := armedPaths(t, n, fileURL(remote), "app-a", nil)
		if err := os.RemoveAll(filepath.Join(vol.tree, "app-a")); err != nil {
			t.Fatalf("deleting the entry: %v", err)
		}
		n.commit(t.Context(), vol, vol.policyNow())
		n.push(t.Context(), vol)

		again := restaged(t, n, request)

		if got := readTree(t, again.tree); len(got) != 0 {
			t.Errorf("the tree has %v, want nothing", got)
		}
		if got := git(t, remote, "ls-tree", "-r", "--name-only", "main"); got != "README.md\napp-b/config.yaml\n" {
			t.Errorf("the remote has %q, want app-a deleted", got)
		}
	})
}

func TestAKeptTreeStagedWithOtherPaths(t *testing.T) {
	for _, c := range []struct {
		name    string
		written map[string]string
		code    codes.Code
		want    map[string]string
	}{
		{
			name: "a clean tree",
			code: codes.OK,
			want: map[string]string{"app-b/config.yaml": "b"},
		},
		{
			name:    "a pending change inside the old paths",
			written: map[string]string{"app-a/draft.yaml": "unsaved"},
			code:    codes.FailedPrecondition,
			want:    map[string]string{"app-a/config.yaml": "a", "app-a/draft.yaml": "unsaved"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, _ := testNode(t, io.Discard)
				vol, request := stagedPaths(t, n, "config", fileURL(appRemote(t)), "app-a")
				writeFiles(t, vol.tree, c.written)
				n.mu.Lock()
				n.disarm(vol)
				delete(n.staged, "config")
				n.mu.Unlock()

				_, err := n.NodeStageVolume(t.Context(), withPaths(request, "app-b"))

				if got := status.Code(err); got != c.code {
					t.Fatalf("the stage returned %v, want %v", err, c.code)
				}
				if got := readTree(t, vol.tree); !sameTree(got, c.want) {
					t.Errorf("the tree has %v, want %v", got, c.want)
				}
			})
		})
	}
}

func TestAResumedVolumeKeepsToItsPaths(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol, _ := stagedPaths(t, n, "config", fileURL(appRemote(t)), "app-a")
		unwatched(t, n, vol)
		writeFiles(t, vol.tree, map[string]string{"log.txt": "outside"})

		again := restarted(t, n, true)

		again.mu.Lock()
		resumed := again.volumes["config"]
		again.mu.Unlock()
		if found, _ := resumed.work.pending(t.Context()); len(found) != 0 {
			t.Errorf("the resumed tree has %v pending, want nothing", found)
		}
	})
}
