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

// stageRequest is the stage call the kubelet makes for a persistent volume
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
		VolumeCapability:  capabilityOf(csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER),
	}
}

func capabilityOf(mode csi.VolumeCapability_AccessMode_Mode) *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: mode},
	}
}

// persistentPublish is the publish call the kubelet makes after the stage,
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

// stagedWriteable stages and publishes one writeable volume and answers
// what the node holds for it.
func stagedWriteable(
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

func TestNodeStageVolumeRefusesEveryAccessModeButTheThreeItServes(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
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
			_, err := answering.NodeStageVolume(t.Context(), request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("NodeStageVolume answered %v, want %v", got, codes.InvalidArgument)
			}
			for _, named := range []string{"ReadWriteOncePod", "ReadOnlyMany"} {
				if !strings.Contains(status.Convert(err).Message(), named) {
					t.Errorf("NodeStageVolume said %q, want %s named",
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
				t.Fatalf("stageKind: %v", err)
			}
			if got != c.want {
				t.Errorf("stageKind answered %v, want %v", got, c.want)
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

// mustFail runs the stage and fails the test when it works.
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
	if !staged.writeable() || staged.staging != request.StagingTargetPath {
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
	published, request := stagedWriteable(t, answering, "config", fileURL(source))

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
	published, request := stagedWriteable(t, answering, "config", fileURL(source))
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

func TestTheUnpublishCommitsAndPushesTheLastOfTheWork(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "config-eager")
	armingClass(t, answering, "config-eager", nil)
	staged := stageRequest(t, "config", fileURL(remote), nil)
	if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	request := persistentPublish(t, staged)
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	answering.mu.Lock()
	published := answering.volumes["config"]
	answering.mu.Unlock()
	waitForArmed(t, published, true)
	writeFiles(t, published.tree, map[string]string{"one.yaml": "1"})

	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "config", TargetPath: request.TargetPath,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want the last of the pod's work", got)
	}
}

func TestTheUnstagePushesWhatTheUnpublishCouldNot(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: "config", StagingTargetPath: held.staging,
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
	if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want the driver's commit", got)
	}
}

func TestARestoreReplaysTheModesAndTheEmptyDirectories(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	first, _ := testNode(t, io.Discard)
	held := armedVolume(t, first, "config", fileURL(remote), nil)
	unwatched(t, first, held)
	writeFiles(t, held.tree, map[string]string{"secret.yaml": "1"})
	if err := os.Chmod(filepath.Join(held.tree, "secret.yaml"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(held.tree, ".storage"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	first.commit(t.Context(), held, held.policyNow())
	first.push(t.Context(), held)

	// A node that has never held the volume is a restore.
	again, _ := testNode(t, io.Discard)
	if _, err := again.NodeStageVolume(t.Context(),
		stageRequest(t, "config", fileURL(remote), nil)); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	again.mu.Lock()
	restored := again.staged["config"]
	again.mu.Unlock()

	secret, err := os.Stat(filepath.Join(restored.tree, "secret.yaml"))
	if err != nil {
		t.Fatalf("the restored tree holds no secret.yaml: %v", err)
	}
	if secret.Mode().Perm() != 0o600 {
		t.Errorf("secret.yaml is %s, want -rw-------", secret.Mode())
	}
	storage, err := os.Stat(filepath.Join(restored.tree, ".storage"))
	if err != nil {
		t.Fatalf("the restored tree holds no empty directory: %v", err)
	}
	if !storage.IsDir() || storage.Mode().Perm() != 0o700 {
		t.Errorf(".storage is %s, want drwx------", storage.Mode())
	}
}

func TestARestoreReportsAMetadataRefItCannotRead(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	// A metadata ref whose tree holds no record is a ref the driver
	// cannot replay.
	git(t, remote, "update-ref", metadataRef, "refs/heads/main")

	logs := &logbook{}
	answering, _ := testNode(t, logs)
	if _, err := answering.NodeStageVolume(t.Context(),
		stageRequest(t, "config", fileURL(remote), nil)); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	if !strings.Contains(logs.String(), "the metadata was not replayed") {
		t.Errorf("the log is %q, want the failed replay in it", logs)
	}
}

func TestARestoreReportsACredentialItCannotWrite(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	answering.restore(t.Context(), &volume{
		id:          "config",
		attributes:  &attributes{url: "file:///nowhere", ref: "main"},
		credentials: &credentials{privateKey: "a key"},
		directory:   filepath.Join(t.TempDir(), "gone"),
	})
	if !strings.Contains(logs.String(), "the metadata was not fetched") {
		t.Errorf("the log is %q, want the failure in it", logs)
	}
}

func TestNodeStageVolumeReportsAMarkItCannotMove(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node, source string)
	}{
		{
			name: "on the first stage of a volume",
			stand: func(t *testing.T, answering *node, _ string) {
				lockTheMark(t, answering, "config")
			},
		},
		{
			name: "on a work tree a driver that made no commits left",
			stand: func(t *testing.T, answering *node, source string) {
				staged := stageRequest(t, "config", fileURL(source), nil)
				if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
					t.Fatalf("NodeStageVolume: %v", err)
				}
				if _, err := answering.NodeUnstageVolume(t.Context(),
					&csi.NodeUnstageVolumeRequest{
						VolumeId: "config", StagingTargetPath: staged.StagingTargetPath,
					}); err != nil {
					t.Fatalf("NodeUnstageVolume: %v", err)
				}
				work := answering.store.workTree(
					answering.store.repository(fileURL(source)), "config")
				if err := os.Remove(
					filepath.Join(work.gitDir, filepath.FromSlash(pushedRef))); err != nil {
					t.Fatalf("removing the mark: %v", err)
				}
				lockTheMark(t, answering, "config")
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			c.stand(t, answering, source)
			_, err := answering.NodeStageVolume(t.Context(),
				stageRequest(t, "config", fileURL(source), nil))
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodeStageVolume answered %v, want %v", got, codes.Internal)
			}
		})
	}
}

// lockTheMark puts a directory where git writes the mark's lock file, so
// the next move of the mark fails.
func lockTheMark(t *testing.T, answering *node, id string) {
	t.Helper()
	lock := filepath.Join(answering.store.volumeDir(id), "git",
		filepath.FromSlash(pushedRef)+".lock")
	if err := os.MkdirAll(lock, 0o755); err != nil {
		t.Fatalf("making the lock directory: %v", err)
	}
}

func TestAStageThatFindsTheMarkLeavesItWhereItIs(t *testing.T) {
	answering, held, _ := pushedVolume(t, io.Discard, nil)
	before := held.work.refCommit(t.Context(), pushedRef)
	if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: "config", StagingTargetPath: held.staging,
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
	if after := held.work.refCommit(t.Context(), pushedRef); after == before {
		t.Error("the unstage pushed nothing, and the mark did not move")
	}
}
