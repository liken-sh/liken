package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StageRequest is the stage call the kubelet makes for a persistent volume
// of the URL.
func stageRequest(t *testing.T, id, url string, extra map[string]string) *csi.NodeStageVolumeRequest {
	t.Helper()
	attributes := map[string]string{"url": url}
	for key, value := range extra {
		attributes[key] = value
	}
	return &csi.NodeStageVolumeRequest{
		VolumeId:          id,
		StagingTargetPath: filepath.Join(t.TempDir(), "staging"),
		VolumeContext:     attributes,
		VolumeCapability:  writeableCapability(csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER),
	}
}

func writeableCapability(mode csi.VolumeCapability_AccessMode_Mode) *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: mode},
	}
}

// PersistentPublish is the publish call the kubelet makes after the stage,
// which carries the pod and the same attributes.
func persistentPublish(t *testing.T, staged *csi.NodeStageVolumeRequest) *csi.NodePublishVolumeRequest {
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

// WriteableVolume stages and publishes one writeable volume and answers
// what the node holds for it.
func writeableVolume(
	t *testing.T, answering *node, id, url string,
) (*volume, *csi.NodePublishVolumeRequest) {
	t.Helper()
	staged := stageRequest(t, id, url, nil)
	if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	request := persistentPublish(t, staged)
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	return answering.volumes[id], request
}

func TestNodeStageVolumeRefusesACallThatNamesTooLittle(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name    string
		request *csi.NodeStageVolumeRequest
		says    string
	}{
		{
			name:    "no volume",
			request: &csi.NodeStageVolumeRequest{StagingTargetPath: "/staging"},
			says:    "volume_id: the call names no volume",
		},
		{
			name: "a volume id that is a path",
			request: &csi.NodeStageVolumeRequest{
				VolumeId: "../escape", StagingTargetPath: "/staging",
			},
			says: "volume_id: a volume id is one path element",
		},
		{
			name:    "no staging path",
			request: &csi.NodeStageVolumeRequest{VolumeId: "config"},
			says:    "staging_target_path: the call names no path",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := answering.NodeStageVolume(t.Context(), c.request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("NodeStageVolume answered %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.says {
				t.Errorf("NodeStageVolume said %q, want %q", got, c.says)
			}
		})
	}
}

func TestNodeStageVolumeRefusesEveryAccessModeButOneWriter(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	for _, mode := range []csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_UNKNOWN,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			request := stageRequest(t, "config", "file:///nowhere", nil)
			request.VolumeCapability = writeableCapability(mode)
			_, err := answering.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("NodeStageVolume answered %v, want %v", got, codes.InvalidArgument)
			}
			if !strings.Contains(status.Convert(err).Message(), "ReadWriteOncePod") {
				t.Errorf("NodeStageVolume said %q, want ReadWriteOncePod named",
					status.Convert(err).Message())
			}
		})
	}
}

func TestNodeStageVolumeRefusesAReadOnlyAttribute(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	for attribute, value := range map[string]string{
		"pull": "5m", "depth": "1", "offline": "allowStale",
	} {
		t.Run(attribute, func(t *testing.T) {
			request := stageRequest(t, "config", "file:///nowhere",
				map[string]string{attribute: value})
			_, err := answering.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("NodeStageVolume answered %v, want %v", got, codes.InvalidArgument)
			}
			if !strings.HasPrefix(status.Convert(err).Message(), attribute+":") {
				t.Errorf("NodeStageVolume said %q, want %s named", status.Convert(err).Message(), attribute)
			}
		})
	}
}

func TestNodeStageVolumeRefusesWhatItCannotRead(t *testing.T) {
	answering, _ := testNode(t, io.Discard)

	unknown := stageRequest(t, "config", "file:///nowhere", map[string]string{"branch": "main"})
	if got := status.Code(mustFail(t, answering, unknown)); got != codes.InvalidArgument {
		t.Errorf("NodeStageVolume answered %v for an unknown attribute, want %v",
			got, codes.InvalidArgument)
	}

	secret := stageRequest(t, "config", "file:///nowhere", nil)
	secret.Secrets = map[string]string{"password": "hunter2"}
	if got := status.Code(mustFail(t, answering, secret)); got != codes.InvalidArgument {
		t.Errorf("NodeStageVolume answered %v for a Secret with no credential, want %v",
			got, codes.InvalidArgument)
	}
}

// MustFail runs the stage and fails the test when it works.
func mustFail(t *testing.T, answering *node, request *csi.NodeStageVolumeRequest) error {
	t.Helper()
	_, err := answering.NodeStageVolume(t.Context(), request)
	if err == nil {
		t.Fatal("NodeStageVolume answered no error")
	}
	return err
}

func TestNodeStageVolumeMakesTheWorkTreeFromTheRef(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)

	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}

	answering.mu.Lock()
	staged, found := answering.staged["config"]
	answering.mu.Unlock()
	if !found {
		t.Fatal("the node holds no staged volume")
	}
	if !staged.writeable || staged.staging != request.StagingTargetPath {
		t.Errorf("the volume is %+v, want a writeable volume staged at %s",
			staged, request.StagingTargetPath)
	}
	if got := readTree(t, staged.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the tree holds %v", got)
	}
	abnormal, message := staged.report()
	if abnormal {
		t.Errorf("a first stage reported %q", message)
	}
}

func TestNodeStageVolumeLeavesATreeItAlreadyMade(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)
	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	answering.mu.Lock()
	staged := answering.staged["config"]
	answering.mu.Unlock()
	writeFiles(t, staged.tree, map[string]string{"a.txt": "the pod wrote this"})

	// The driver restarted and the kubelet staged the volume again, with
	// the ref moved under it.
	moved := commitFiles(t, source, map[string]string{"a.txt": "two"})
	answering.mu.Lock()
	delete(answering.staged, "config")
	answering.mu.Unlock()
	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("NodeStageVolume again: %v", err)
	}

	answering.mu.Lock()
	again := answering.staged["config"]
	answering.mu.Unlock()
	want := map[string]string{"a.txt": "the pod wrote this"}
	if got := readTree(t, again.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	abnormal, message := again.report()
	if !abnormal {
		t.Fatalf("a ref that moved reported %q", message)
	}
	if !strings.Contains(message, short(moved)) || !strings.Contains(message, "upstream moved") {
		t.Errorf("the condition says %q, want both commits named", message)
	}
}

func TestNodeStageVolumeAnswersTheSameCallTwice(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)
	for range 2 {
		if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
			t.Fatalf("NodeStageVolume: %v", err)
		}
	}

	elsewhere := stageRequest(t, "config", fileURL(source), nil)
	_, err := answering.NodeStageVolume(t.Context(), elsewhere)
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("NodeStageVolume answered %v, want %v", got, codes.FailedPrecondition)
	}
}

func TestNodeStageVolumeRefusesAForgeItCannotReach(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	request := stageRequest(t, "config", fileURL(filepath.Join(t.TempDir(), "gone")), nil)
	_, err := answering.NodeStageVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Unavailable)
	}
}

func TestNodeStageVolumeReportsWhatItCannotWrite(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node, request *csi.NodeStageVolumeRequest)
	}{
		{
			name: "a store it cannot write",
			stand: func(t *testing.T, answering *node, _ *csi.NodeStageVolumeRequest) {
				file := filepath.Join(t.TempDir(), "file")
				writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
				answering.store = newStore(file)
			},
		},
		{
			name: "a repository it cannot make",
			stand: func(t *testing.T, answering *node, _ *csi.NodeStageVolumeRequest) {
				if err := os.MkdirAll(answering.store.root, 0o755); err != nil {
					t.Fatalf("making the store: %v", err)
				}
				writeFiles(t, answering.store.root, map[string]string{"repos": ""})
			},
		},
		{
			name: "a work tree that is already a file",
			stand: func(t *testing.T, answering *node, _ *csi.NodeStageVolumeRequest) {
				directory := answering.store.volumeDir("config")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatalf("making the volume directory: %v", err)
				}
				writeFiles(t, directory, map[string]string{"tree": ""})
			},
		},
		{
			name: "a key it cannot write",
			stand: func(t *testing.T, answering *node, request *csi.NodeStageVolumeRequest) {
				request.Secrets = map[string]string{privateKeyKey: "KEY"}
				directory := answering.store.volumeDir("config")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatalf("making the volume directory: %v", err)
				}
				readOnlyDir(t, directory)
			},
		},
		{
			name: "a ref that names no commit",
			stand: func(t *testing.T, _ *node, request *csi.NodeStageVolumeRequest) {
				source := strings.TrimPrefix(request.VolumeContext["url"], "file://")
				request.VolumeContext["ref"] = blobRef(t, source)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request := stageRequest(t, "config", fileURL(source), nil)
			c.stand(t, answering, request)

			_, err := answering.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestNodeStageVolumeReportsAHeadItCannotRead(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)
	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}

	// A git directory with a HEAD and nothing else is what a stage that
	// was cut off leaves.
	answering.mu.Lock()
	delete(answering.staged, "config")
	answering.mu.Unlock()
	gitDir := filepath.Join(answering.store.volumeDir("config"), "git")
	if err := os.RemoveAll(filepath.Join(gitDir, "refs")); err != nil {
		t.Fatalf("removing the refs: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(gitDir, "objects")); err != nil {
		t.Fatalf("removing the objects: %v", err)
	}

	_, err := answering.NodeStageVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Internal)
	}
}

func TestNodeUnstageVolumeRefusesACallThatNamesTooLittle(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name    string
		request *csi.NodeUnstageVolumeRequest
		says    string
	}{
		{
			name:    "no volume",
			request: &csi.NodeUnstageVolumeRequest{StagingTargetPath: "/staging"},
			says:    "volume_id: the call names no volume",
		},
		{
			name:    "no staging path",
			request: &csi.NodeUnstageVolumeRequest{VolumeId: "config"},
			says:    "staging_target_path: the call names no path",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := answering.NodeUnstageVolume(t.Context(), c.request)
			if got := status.Convert(err).Message(); got != c.says {
				t.Errorf("NodeUnstageVolume said %q, want %q", got, c.says)
			}
		})
	}
}

func TestNodeUnstageVolumeKeepsTheWorkTree(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)
	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}

	for range 2 {
		if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
			VolumeId: "config", StagingTargetPath: request.StagingTargetPath,
		}); err != nil {
			t.Fatalf("NodeUnstageVolume: %v", err)
		}
	}

	answering.mu.Lock()
	staged := len(answering.staged)
	answering.mu.Unlock()
	if staged != 0 {
		t.Errorf("the node holds %d staged volumes, want 0", staged)
	}
	tree := filepath.Join(answering.store.volumeDir("config"), "tree")
	if got := readTree(t, tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the work tree holds %v after the unstage, want the checkout", got)
	}
}

func TestNodePublishVolumeBindsTheWorkTreeReadWrite(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, request := writeableVolume(t, answering, "config", fileURL(source))

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
		t.Errorf("the volume names the pod %+v, want the pod of the publish", got)
	}
}

func TestNodePublishVolumeAnswersTheSameWriteableCallTwice(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged := stageRequest(t, "config", fileURL(source), nil)
	if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	request := persistentPublish(t, staged)
	for range 2 {
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
	}
	if len(calls.mounts) != 1 {
		t.Errorf("two publishes made %v, want one bind", calls.mounts)
	}

	elsewhere := persistentPublish(t, staged)
	_, err := answering.NodePublishVolume(t.Context(), elsewhere)
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("NodePublishVolume answered %v, want %v", got, codes.FailedPrecondition)
	}
}

func TestNodePublishVolumeTakesTheSecretOfThePublish(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged := stageRequest(t, "config", fileURL(source), nil)
	if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	request := persistentPublish(t, staged)
	request.Secrets = map[string]string{tokenKey: "a token"}
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	answering.mu.Lock()
	published := answering.volumes["config"]
	answering.mu.Unlock()
	if published.credentials == nil || published.credentials.token != "a token" {
		t.Errorf("the volume holds %+v, want the token of the publish", published.credentials)
	}
}

func TestNodePublishVolumeReportsWhatItCannotBind(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, calls *recordedMounts, request *csi.NodePublishVolumeRequest)
	}{
		{
			name: "a target path under a file",
			stand: func(t *testing.T, _ *recordedMounts, request *csi.NodePublishVolumeRequest) {
				file := filepath.Join(t.TempDir(), "file")
				writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
				request.TargetPath = filepath.Join(file, "mount")
			},
		},
		{
			name: "a mount that stays",
			stand: func(_ *testing.T, calls *recordedMounts, _ *csi.NodePublishVolumeRequest) {
				calls.unmountErr = unix.EPERM
			},
		},
		{
			name: "a bind it cannot make",
			stand: func(_ *testing.T, calls *recordedMounts, _ *csi.NodePublishVolumeRequest) {
				calls.failAt = 1
				calls.mountErr = unix.EPERM
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, calls := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			staged := stageRequest(t, "config", fileURL(source), nil)
			if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
				t.Fatalf("NodeStageVolume: %v", err)
			}
			request := persistentPublish(t, staged)
			c.stand(t, calls, request)

			_, err := answering.NodePublishVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodePublishVolume answered %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestNodeUnpublishVolumeKeepsAWriteableTree(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, request := writeableVolume(t, answering, "config", fileURL(source))
	writeFiles(t, published.tree, map[string]string{"new.txt": "the pod wrote this"})

	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "config", TargetPath: request.TargetPath,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}

	want := map[string]string{"a.txt": "one", "new.txt": "the pod wrote this"}
	if got := readTree(t, published.tree); !sameTree(got, want) {
		t.Errorf("the work tree holds %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(published.directory, recordFile)); err == nil {
		t.Error("the record stayed after the unpublish")
	}
	answering.mu.Lock()
	watching := len(answering.watchers)
	answering.mu.Unlock()
	if watching != 0 {
		t.Errorf("the node holds %d watches after the unpublish, want 0", watching)
	}
}
