package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stageRequest builds the stage call that the kubelet sends for a
// persistent writeable volume of the repository at url.
func stageRequest(t testing.TB, id, url string, extra map[string]string) *csi.NodeStageVolumeRequest {
	t.Helper()
	attributes := map[string]string{"url": url}
	for key, value := range extra {
		attributes[key] = value
	}
	return &csi.NodeStageVolumeRequest{
		VolumeId:          id,
		StagingTargetPath: filepath.Join(t.TempDir(), "staging"),
		VolumeContext:     attributes,
		VolumeCapability:  capabilityOf(csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER),
	}
}

func capabilityOf(mode csi.VolumeCapability_AccessMode_Mode) *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: mode},
	}
}

// persistentPublish builds the publish call that the kubelet sends after
// the stage. It has the same volume context plus the pod's name,
// namespace, and UID.
func persistentPublish(t testing.TB, staged *csi.NodeStageVolumeRequest) *csi.NodePublishVolumeRequest {
	t.Helper()
	attributes := map[string]string{
		podNameKey:      "writer",
		podNamespaceKey: "home",
		podUIDKey:       "9b1c",
	}
	for key, value := range staged.GetVolumeContext() {
		attributes[key] = value
	}
	return &csi.NodePublishVolumeRequest{
		VolumeId:          staged.GetVolumeId(),
		StagingTargetPath: staged.GetStagingTargetPath(),
		TargetPath:        filepath.Join(t.TempDir(), "mount"),
		VolumeContext:     attributes,
		VolumeCapability:  staged.GetVolumeCapability(),
	}
}

// stagedWriteable stages and publishes one writeable volume, and returns
// the node's volume and the publish request.
func stagedWriteable(
	t *testing.T, n *node, id, url string,
) (*volume, *csi.NodePublishVolumeRequest) {
	t.Helper()
	staged := stageRequest(t, id, url, nil)
	if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("calling NodeStageVolume: %v", err)
	}
	request := persistentPublish(t, staged)
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("calling NodePublishVolume: %v", err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.volumes[id], request
}

func TestNodeStageVolumeRefusesACallThatNamesTooLittle(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name        string
		request     *csi.NodeStageVolumeRequest
		wantMessage string
	}{
		{
			name:        "no volume",
			request:     &csi.NodeStageVolumeRequest{StagingTargetPath: "/staging"},
			wantMessage: "volume_id is empty",
		},
		{
			name: "a volume id that is a path",
			request: &csi.NodeStageVolumeRequest{
				VolumeId: "../escape", StagingTargetPath: "/staging",
			},
			wantMessage: "volume_id: a volume id is one path element",
		},
		{
			name:        "no staging path",
			request:     &csi.NodeStageVolumeRequest{VolumeId: "config"},
			wantMessage: "staging_target_path is empty",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := n.NodeStageVolume(t.Context(), c.request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("the status code of NodeStageVolume is %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.wantMessage {
				t.Errorf("the message of NodeStageVolume is %q, want %q", got, c.wantMessage)
			}
		})
	}
}

func TestNodeStageVolumeRefusesAnUnsupportedAccessMode(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	for _, mode := range []csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_UNKNOWN,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			request := stageRequest(t, "config", "file:///nowhere", nil)
			request.VolumeCapability = capabilityOf(mode)
			_, err := n.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("the status code of NodeStageVolume is %v, want %v", got, codes.InvalidArgument)
			}
			for _, named := range []string{"ReadWriteOncePod", "ReadOnlyMany"} {
				if !strings.Contains(status.Convert(err).Message(), named) {
					t.Errorf("the message of NodeStageVolume is %q, want it to contain %s",
						status.Convert(err).Message(), named)
				}
			}
		})
	}
}

func TestTheAccessModeDecidesTheKind(t *testing.T) {
	for _, c := range []struct {
		mode csi.VolumeCapability_AccessMode_Mode
		want volumeKind
	}{
		{mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER, want: writeableVolume},
		{mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY, want: readOnlyClaim},
		{mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY, want: readOnlyClaim},
	} {
		t.Run(c.mode.String(), func(t *testing.T) {
			got, err := stageKind(capabilityOf(c.mode))
			if err != nil {
				t.Fatalf("calling stageKind: %v", err)
			}
			if got != c.want {
				t.Errorf("stageKind() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNodeStageVolumeRefusesAReadOnlyAttribute(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	for attribute, value := range map[string]string{
		"pull": "5m", "depth": "1", "offline": "allowStale",
	} {
		t.Run(attribute, func(t *testing.T) {
			request := stageRequest(t, "config", "file:///nowhere",
				map[string]string{attribute: value})
			_, err := n.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("the status code of NodeStageVolume is %v, want %v", got, codes.InvalidArgument)
			}
			if !strings.HasPrefix(status.Convert(err).Message(), attribute+":") {
				t.Errorf("the message of NodeStageVolume is %q, want it to contain %s", status.Convert(err).Message(), attribute)
			}
		})
	}
}

func TestNodeStageVolumeRefusesWhatItCannotRead(t *testing.T) {
	n, _ := testNode(t, io.Discard)

	unknown := stageRequest(t, "config", "file:///nowhere", map[string]string{"branch": "main"})
	if got := status.Code(mustFail(t, n, unknown)); got != codes.InvalidArgument {
		t.Errorf("the status code of NodeStageVolume for an unknown attribute is %v, want %v",
			got, codes.InvalidArgument)
	}

	secret := stageRequest(t, "config", "file:///nowhere", nil)
	secret.Secrets = map[string]string{"password": "hunter2"}
	if got := status.Code(mustFail(t, n, secret)); got != codes.InvalidArgument {
		t.Errorf("the status code of NodeStageVolume for a Secret with no credential is %v, want %v",
			got, codes.InvalidArgument)
	}
}

// mustFail runs the stage and fails the test when it succeeds.
func mustFail(t *testing.T, n *node, request *csi.NodeStageVolumeRequest) error {
	t.Helper()
	_, err := n.NodeStageVolume(t.Context(), request)
	if err == nil {
		t.Fatal("NodeStageVolume returned no error, want one")
	}
	return err
}

func TestNodeStageVolumeCreatesTheWorkTreeAtTheRef(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)

	if _, err := n.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("calling NodeStageVolume: %v", err)
	}

	n.mu.Lock()
	staged, found := n.staged["config"]
	n.mu.Unlock()
	if !found {
		t.Fatal("the node has no staged volume config")
	}
	if !staged.writeable() || staged.staging != request.StagingTargetPath {
		t.Errorf("the volume is %+v, want a writeable volume staged at %s",
			staged, request.StagingTargetPath)
	}
	if got := readTree(t, staged.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the work tree is %v, want a.txt with one", got)
	}
	abnormal, message := staged.health()
	if abnormal {
		t.Errorf("report() after a first stage is abnormal with %q, want normal", message)
	}
}

func TestNodeStageVolumeSucceedsWhenTheSameCallRepeats(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)
	for range 2 {
		if _, err := n.NodeStageVolume(t.Context(), request); err != nil {
			t.Fatalf("calling NodeStageVolume: %v", err)
		}
	}

	elsewhere := stageRequest(t, "config", fileURL(source), nil)
	_, err := n.NodeStageVolume(t.Context(), elsewhere)
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("the status code of NodeStageVolume is %v, want %v", got, codes.FailedPrecondition)
	}
}

func TestNodeStageVolumeRefusesAForgeItCannotReach(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	request := stageRequest(t, "config", fileURL(filepath.Join(t.TempDir(), "gone")), nil)
	_, err := n.NodeStageVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("the status code of NodeStageVolume is %v, want %v", got, codes.Unavailable)
	}
}

func TestNodeStageVolumeReportsWhatItCannotWrite(t *testing.T) {
	for _, c := range []struct {
		name    string
		prepare func(t *testing.T, n *node, request *csi.NodeStageVolumeRequest)
	}{
		{
			name: "a store it cannot write",
			prepare: func(t *testing.T, n *node, _ *csi.NodeStageVolumeRequest) {
				file := filepath.Join(t.TempDir(), "file")
				writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
				n.store = newStore(file)
			},
		},
		{
			name: "a repository it cannot make",
			prepare: func(t *testing.T, n *node, _ *csi.NodeStageVolumeRequest) {
				if err := os.MkdirAll(n.store.root, 0o755); err != nil {
					t.Fatalf("creating the store: %v", err)
				}
				writeFiles(t, n.store.root, map[string]string{"repos": ""})
			},
		},
		{
			name: "a work tree that is already a file",
			prepare: func(t *testing.T, n *node, _ *csi.NodeStageVolumeRequest) {
				directory := n.store.volumeDir("config")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatalf("creating the volume directory: %v", err)
				}
				writeFiles(t, directory, map[string]string{"tree": ""})
			},
		},
		{
			name: "a key it cannot write",
			prepare: func(t *testing.T, n *node, request *csi.NodeStageVolumeRequest) {
				request.Secrets = map[string]string{privateKeyKey: "KEY"}
				directory := n.store.volumeDir("config")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatalf("creating the volume directory: %v", err)
				}
				readOnlyDir(t, directory)
			},
		},
		{
			name: "a ref that names no commit",
			prepare: func(t *testing.T, _ *node, request *csi.NodeStageVolumeRequest) {
				source := strings.TrimPrefix(request.VolumeContext["url"], "file://")
				request.VolumeContext["ref"] = blobRef(t, source)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request := stageRequest(t, "config", fileURL(source), nil)
			c.prepare(t, n, request)

			_, err := n.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("the status code of NodeStageVolume is %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestNodeStageVolumeReportsAHeadItCannotRead(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)
	if _, err := n.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("calling NodeStageVolume: %v", err)
	}

	// Remove the refs so the git directory has a HEAD and nothing else,
	// which is what an interrupted stage leaves.
	n.mu.Lock()
	delete(n.staged, "config")
	n.mu.Unlock()
	gitDir := filepath.Join(n.store.volumeDir("config"), "git")
	if err := os.RemoveAll(filepath.Join(gitDir, "refs")); err != nil {
		t.Fatalf("removing the refs: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(gitDir, "objects")); err != nil {
		t.Fatalf("removing the objects: %v", err)
	}

	_, err := n.NodeStageVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("the status code of NodeStageVolume is %v, want %v", got, codes.Internal)
	}
}

func TestNodeUnstageVolumeRefusesACallThatNamesTooLittle(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name        string
		request     *csi.NodeUnstageVolumeRequest
		wantMessage string
	}{
		{
			name:        "no volume",
			request:     &csi.NodeUnstageVolumeRequest{StagingTargetPath: "/staging"},
			wantMessage: "volume_id is empty",
		},
		{
			name:        "no staging path",
			request:     &csi.NodeUnstageVolumeRequest{VolumeId: "config"},
			wantMessage: "staging_target_path is empty",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := n.NodeUnstageVolume(t.Context(), c.request)
			if got := status.Convert(err).Message(); got != c.wantMessage {
				t.Errorf("the message of NodeUnstageVolume is %q, want %q", got, c.wantMessage)
			}
		})
	}
}

func TestNodeUnstageVolumeKeepsTheWorkTree(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)
	if _, err := n.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("calling NodeStageVolume: %v", err)
	}

	for range 2 {
		if _, err := n.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
			VolumeId: "config", StagingTargetPath: request.StagingTargetPath,
		}); err != nil {
			t.Fatalf("calling NodeUnstageVolume: %v", err)
		}
	}

	n.mu.Lock()
	staged := len(n.staged)
	n.mu.Unlock()
	if staged != 0 {
		t.Errorf("the node has %d staged volumes after the unstage, want 0", staged)
	}
	tree := filepath.Join(n.store.volumeDir("config"), "tree")
	if got := readTree(t, tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the work tree is %v after the unstage, want the checkout", got)
	}
}

func TestNodePublishVolumeBindsTheWorkTreeReadWrite(t *testing.T) {
	n, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, request := stagedWriteable(t, n, "config", fileURL(source))

	if len(calls.mounts) != 1 {
		t.Fatalf("the publish made %v, want one bind", calls.mounts)
	}
	if calls.mounts[0].source != published.tree || calls.mounts[0].target != request.TargetPath {
		t.Errorf("the bind is %+v, want %s onto %s",
			calls.mounts[0], published.tree, request.TargetPath)
	}
	if calls.mounts[0].flags&unix.MS_RDONLY != 0 {
		t.Errorf("the bind is %+v, want it writeable", calls.mounts[0])
	}
	if got := published.podRef(); got.name != "writer" || got.namespace != "home" {
		t.Errorf("podRef() is %+v, want the pod of the publish", got)
	}
}

func TestNodePublishVolumeSucceedsWhenTheSameWriteableCallRepeats(t *testing.T) {
	n, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged := stageRequest(t, "config", fileURL(source), nil)
	if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("calling NodeStageVolume: %v", err)
	}
	request := persistentPublish(t, staged)
	for range 2 {
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("calling NodePublishVolume: %v", err)
		}
	}
	if len(calls.mounts) != 1 {
		t.Errorf("two publishes made %v, want one bind", calls.mounts)
	}

	elsewhere := persistentPublish(t, staged)
	_, err := n.NodePublishVolume(t.Context(), elsewhere)
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("the status code of NodePublishVolume is %v, want %v", got, codes.FailedPrecondition)
	}
}

func TestNodePublishVolumeUsesTheSecretOfThePublish(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged := stageRequest(t, "config", fileURL(source), nil)
	if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("calling NodeStageVolume: %v", err)
	}
	request := persistentPublish(t, staged)
	request.Secrets = map[string]string{tokenKey: "a token"}
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("calling NodePublishVolume: %v", err)
	}

	n.mu.Lock()
	published := n.volumes["config"]
	n.mu.Unlock()
	if published.credentials == nil || published.credentials.token != "a token" {
		t.Errorf("the credentials of the volume are %+v, want the token of the publish", published.credentials)
	}
}

func TestNodePublishVolumeReportsWhatItCannotBind(t *testing.T) {
	for _, c := range []struct {
		name    string
		prepare func(t *testing.T, calls *recordedMounts, request *csi.NodePublishVolumeRequest)
	}{
		{
			name: "a target path under a file",
			prepare: func(t *testing.T, _ *recordedMounts, request *csi.NodePublishVolumeRequest) {
				file := filepath.Join(t.TempDir(), "file")
				writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
				request.TargetPath = filepath.Join(file, "mount")
			},
		},
		{
			name: "a mount that stays",
			prepare: func(_ *testing.T, calls *recordedMounts, _ *csi.NodePublishVolumeRequest) {
				calls.unmountErr = unix.EPERM
			},
		},
		{
			name: "a bind it cannot make",
			prepare: func(_ *testing.T, calls *recordedMounts, _ *csi.NodePublishVolumeRequest) {
				calls.failAt = 1
				calls.mountErr = unix.EPERM
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, calls := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			staged := stageRequest(t, "config", fileURL(source), nil)
			if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
				t.Fatalf("calling NodeStageVolume: %v", err)
			}
			request := persistentPublish(t, staged)
			c.prepare(t, calls, request)

			_, err := n.NodePublishVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("the status code of NodePublishVolume is %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestNodeUnpublishVolumeKeepsAWriteableTree(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, request := stagedWriteable(t, n, "config", fileURL(source))
	writeFiles(t, published.tree, map[string]string{"new.txt": "the pod wrote this"})

	if _, err := n.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "config", TargetPath: request.TargetPath,
	}); err != nil {
		t.Fatalf("calling NodeUnpublishVolume: %v", err)
	}

	want := map[string]string{"a.txt": "one", "new.txt": "the pod wrote this"}
	if got := readTree(t, published.tree); !sameTree(got, want) {
		t.Errorf("the work tree has %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(published.directory, recordFile)); err == nil {
		t.Error("the record stayed after the unpublish")
	}
	n.mu.Lock()
	watching := len(n.watchers)
	n.mu.Unlock()
	if watching != 0 {
		t.Errorf("the node has %d watches after the unpublish, want 0", watching)
	}
}

func TestTheUnpublishCommitsAndPushesTheLastOfTheWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "config", "config-eager")
		createClass(t, n, "config-eager", nil)
		staged := stageRequest(t, "config", fileURL(remote), nil)
		if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("calling NodeStageVolume: %v", err)
		}
		request := persistentPublish(t, staged)
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("calling NodePublishVolume: %v", err)
		}
		n.mu.Lock()
		published := n.volumes["config"]
		n.mu.Unlock()
		waitForArmed(t, published, true)
		writeFiles(t, published.tree, map[string]string{"one.yaml": "1"})

		if _, err := n.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
			VolumeId: "config", TargetPath: request.TargetPath,
		}); err != nil {
			t.Fatalf("calling NodeUnpublishVolume: %v", err)
		}
		if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
			t.Errorf("the remote's main is at %q, want the last of the pod's work", got)
		}
	})
}

func TestTheUnstagePushesWhatTheUnpublishCouldNot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		if _, err := n.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
			VolumeId: "config", StagingTargetPath: vol.staging,
		}); err != nil {
			t.Fatalf("calling NodeUnstageVolume: %v", err)
		}
		if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
			t.Errorf("the remote's main is at %q, want the driver's commit", got)
		}
	})
}

func TestARestoreReplaysTheModesAndTheEmptyDirectories(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		first, _ := testNode(t, io.Discard)
		vol := armedVolume(t, first, "config", fileURL(remote), nil)
		unwatched(t, first, vol)
		writeFiles(t, vol.tree, map[string]string{"secret.yaml": "1"})
		if err := os.Chmod(filepath.Join(vol.tree, "secret.yaml"), 0o600); err != nil {
			t.Fatalf("changing the mode of the file: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(vol.tree, ".storage"), 0o700); err != nil {
			t.Fatalf("creating the directory: %v", err)
		}
		first.commit(t.Context(), vol, vol.policyNow())
		first.push(t.Context(), vol)

		// A second node has no work tree for the volume, so its stage
		// creates one and restores the modes from the remote.
		again, _ := testNode(t, io.Discard)
		if _, err := again.NodeStageVolume(t.Context(),
			stageRequest(t, "config", fileURL(remote), nil)); err != nil {
			t.Fatalf("calling NodeStageVolume: %v", err)
		}
		again.mu.Lock()
		restored := again.staged["config"]
		again.mu.Unlock()

		secret, err := os.Stat(filepath.Join(restored.tree, "secret.yaml"))
		if err != nil {
			t.Fatalf("the restored tree has no secret.yaml: %v", err)
		}
		if secret.Mode().Perm() != 0o600 {
			t.Errorf("secret.yaml is %s, want -rw-------", secret.Mode())
		}
		storage, err := os.Stat(filepath.Join(restored.tree, ".storage"))
		if err != nil {
			t.Fatalf("the restored tree has no empty directory: %v", err)
		}
		if !storage.IsDir() || storage.Mode().Perm() != 0o700 {
			t.Errorf(".storage is %s, want drwx------", storage.Mode())
		}
	})
}

func TestARestoreReportsAMetadataRefItCannotRead(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	// The ref points at a tree with no metadata record, so the driver
	// cannot replay it.
	git(t, remote, "update-ref", metadataRef, "refs/heads/main")

	logs := &logbook{}
	n, _ := testNode(t, logs)
	if _, err := n.NodeStageVolume(t.Context(),
		stageRequest(t, "config", fileURL(remote), nil)); err != nil {
		t.Fatalf("calling NodeStageVolume: %v", err)
	}
	if !strings.Contains(logs.String(), "was not replayed") {
		t.Errorf("the log is %q, want it to say the metadata was not replayed", logs)
	}
}

func TestARestoreReportsACredentialItCannotWrite(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	n.restore(t.Context(), &volume{
		id:          "config",
		attributes:  &attributes{url: "file:///nowhere", ref: "main"},
		credentials: &credentials{privateKey: "a key"},
		directory:   filepath.Join(t.TempDir(), "gone"),
	})
	if !strings.Contains(logs.String(), "credential was not written") {
		t.Errorf("the log is %q, want it to say the credential was not written", logs)
	}
}

func TestNodeStageVolumeFailsWhenItCannotUpdatePushedRef(t *testing.T) {
	for _, c := range []struct {
		name    string
		prepare func(t *testing.T, n *node, source string)
	}{
		{
			name: "on the first stage of a volume",
			prepare: func(t *testing.T, n *node, _ string) {
				lockPushedRef(t, n, "config")
			},
		},
		{
			name: "on a work tree that has no pushedRef",
			prepare: func(t *testing.T, n *node, source string) {
				staged := stageRequest(t, "config", fileURL(source), nil)
				if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
					t.Fatalf("calling NodeStageVolume: %v", err)
				}
				if _, err := n.NodeUnstageVolume(t.Context(),
					&csi.NodeUnstageVolumeRequest{
						VolumeId: "config", StagingTargetPath: staged.StagingTargetPath,
					}); err != nil {
					t.Fatalf("calling NodeUnstageVolume: %v", err)
				}
				work := n.store.workTree(
					n.store.repository(fileURL(source)), "config")
				if err := os.Remove(
					filepath.Join(work.gitDir, filepath.FromSlash(pushedRef))); err != nil {
					t.Fatalf("removing pushedRef: %v", err)
				}
				lockPushedRef(t, n, "config")
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			c.prepare(t, n, source)
			_, err := n.NodeStageVolume(t.Context(),
				stageRequest(t, "config", fileURL(source), nil))
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("the status code of NodeStageVolume is %v, want %v", got, codes.Internal)
			}
		})
	}
}

// lockPushedRef creates a directory where git writes the lock file of
// pushedRef, so the next update of pushedRef fails.
func lockPushedRef(t *testing.T, n *node, id string) {
	t.Helper()
	lock := filepath.Join(n.store.volumeDir(id), "git",
		filepath.FromSlash(pushedRef)+".lock")
	if err := os.MkdirAll(lock, 0o755); err != nil {
		t.Fatalf("creating the lock directory: %v", err)
	}
}

func TestNodeUnstageVolumePushesAndMovesPushedRef(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, _ := committedVolume(t, io.Discard, nil)
		before := vol.work.refCommit(t.Context(), pushedRef)
		if _, err := n.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
			VolumeId: "config", StagingTargetPath: vol.staging,
		}); err != nil {
			t.Fatalf("calling NodeUnstageVolume: %v", err)
		}
		if after := vol.work.refCommit(t.Context(), pushedRef); after == before {
			t.Error("the unstage pushed nothing, and pushedRef did not move")
		}
	})
}
