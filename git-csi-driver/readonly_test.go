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
	corev1 "k8s.io/api/core/v1"
)

// readOnlyStage is the stage call the kubelet makes for a claim whose
// access mode is ReadOnlyMany.
func readOnlyStage(t *testing.T, id, url string, extra map[string]string) *csi.NodeStageVolumeRequest {
	t.Helper()
	request := stageRequest(t, id, url, extra)
	request.VolumeCapability = capabilityOf(
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY)
	return request
}

// readOnlyPublish is the publish call one pod makes for a staged
// read-only claim, at a target of its own.
func readOnlyPublish(
	t *testing.T, staged *csi.NodeStageVolumeRequest, pod string,
) *csi.NodePublishVolumeRequest {
	t.Helper()
	request := persistentPublish(t, staged)
	request.VolumeContext[podNameKey] = pod
	request.VolumeContext[podUIDKey] = pod
	request.Readonly = true
	return request
}

// stagedReadOnly stages one read-only claim and answers the stage call
// and what the node holds for it.
func stagedReadOnly(
	t *testing.T, answering *node, id, url string, extra map[string]string,
) (*csi.NodeStageVolumeRequest, *volume) {
	t.Helper()
	staged := readOnlyStage(t, id, url, extra)
	if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	return staged, answering.staged[id]
}

// publishedTo publishes a staged read-only claim under one more pod.
func publishedTo(
	t *testing.T, answering *node, staged *csi.NodeStageVolumeRequest, pod string,
) *csi.NodePublishVolumeRequest {
	t.Helper()
	request := readOnlyPublish(t, staged, pod)
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume for %s: %v", pod, err)
	}
	return request
}

func TestAReadOnlyStagePlacesTheTreeAndFollowsIt(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, held := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "1h", "depth": "1", "offline": "allowStale"})

	if held.kind != readOnlyClaim {
		t.Fatalf("the volume is %v, want a read-only claim", held.kind)
	}
	if got := readTree(t, held.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the tree holds %v, want the commit", got)
	}
	if _, err := os.Stat(filepath.Join(held.directory, "git")); err == nil {
		t.Error("a read-only claim made a git directory")
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	if len(answering.watchers) != 0 || len(answering.armings) != 0 {
		t.Errorf("the node holds %d watches and %d arming loops, want none of either",
			len(answering.watchers), len(answering.armings))
	}
	if len(answering.followers) != 1 {
		t.Errorf("the node holds %d fetch loops, want 1", len(answering.followers))
	}
}

func TestAReadOnlyStageTakesTheAttributesAnInlineVolumeTakes(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, held := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "never", "offline": "allowStale"})

	if held.attributes.pull != 0 || held.attributes.offline != offlineAllowStale {
		t.Errorf("the volume is %+v, want pull never and offline allowStale", held.attributes)
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	if len(answering.followers) != 0 {
		t.Errorf("a volume with pull never joined %d fetch loops, want none",
			len(answering.followers))
	}
}

func TestAReadOnlyStageRefusesAForgeItCannotReach(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "franchises", "")
	staged := readOnlyStage(t, "franchises",
		fileURL(filepath.Join(t.TempDir(), "gone")), map[string]string{"pull": "never"})

	_, err := answering.NodeStageVolume(t.Context(), staged)
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("NodeStageVolume answered %v, want %v", got, codes.Unavailable)
	}
	if _, err := os.Stat(answering.store.volumeDir("franchises")); err == nil {
		t.Error("a refused stage left the volume's directory behind")
	}
	posted := eventsOf(t, answering)
	if len(posted) != 1 || posted[0].Reason != reasonRefused ||
		posted[0].InvolvedObject.Kind != "PersistentVolumeClaim" {
		t.Errorf("the refused stage posted %v, want one refusal on the claim", posted)
	}
}

func TestAReadOnlyClaimBindsEveryPodOnTheNode(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, held := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "never"})

	first := publishedTo(t, answering, staged, "reader-a")
	second := publishedTo(t, answering, staged, "reader-b")
	want := []string{first.TargetPath, second.TargetPath}
	if got := held.boundTargets(); !sameStrings(got, want) {
		t.Errorf("the volume is bound at %v, want %v", got, want)
	}
	for _, mount := range calls.mounts {
		if mount.source != held.tree {
			t.Errorf("the driver bound %s, want the staged tree %s", mount.source, held.tree)
		}
	}
	if calls.mounts[1].flags&unix.MS_RDONLY == 0 {
		t.Errorf("the bind is %+v, want it read-only", calls.mounts[1])
	}

	binds := len(calls.mounts)
	if _, err := answering.NodePublishVolume(t.Context(), first); err != nil {
		t.Fatalf("a repeated NodePublishVolume: %v", err)
	}
	if len(calls.mounts) != binds || len(held.boundTargets()) != 2 {
		t.Errorf("a repeated publish made %d mounts and left %d targets, want 0 and 2",
			len(calls.mounts)-binds, len(held.boundTargets()))
	}
}

func TestAReadOnlyClaimKeepsItsOtherTargetsAtAnUnpublish(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, held := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "1h"})
	first := publishedTo(t, answering, staged, "reader-a")
	second := publishedTo(t, answering, staged, "reader-b")

	unpublish(t, answering, "franchises", first.TargetPath)
	if got := held.boundTargets(); !sameStrings(got, []string{second.TargetPath}) {
		t.Errorf("the volume is bound at %v, want %s", got, second.TargetPath)
	}
	if got := recordOf(t, answering, "franchises").Targets; !sameStrings(got, []string{second.TargetPath}) {
		t.Errorf("the record carries %v, want %s", got, second.TargetPath)
	}
	answering.mu.Lock()
	_, standing := answering.volumes["franchises"]
	answering.mu.Unlock()
	if !standing {
		t.Error("the volume left the node while a pod still reads it")
	}

	unpublish(t, answering, "franchises", second.TargetPath)
	answering.mu.Lock()
	_, standing = answering.volumes["franchises"]
	_, held2 := answering.staged["franchises"]
	loops := len(answering.followers)
	answering.mu.Unlock()
	if standing || !held2 {
		t.Errorf("after the last unpublish the volume is published: %v, staged: %v, want false and true",
			standing, held2)
	}
	if loops != 1 {
		t.Errorf("the node holds %d fetch loops after the last unpublish, want 1", loops)
	}
	if _, err := os.Stat(held.tree); err != nil {
		t.Errorf("the staged tree went with the last unpublish: %v", err)
	}
}

func TestTheUnstageOfAReadOnlyClaimTakesTheTreeAway(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, held := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "1h"})
	request := publishedTo(t, answering, staged, "reader-a")
	unpublish(t, answering, "franchises", request.TargetPath)

	if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: "franchises", StagingTargetPath: staged.StagingTargetPath,
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
	if _, err := os.Stat(held.directory); err == nil {
		t.Error("the volume's directory stayed after the unstage")
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	if len(answering.staged) != 0 || len(answering.followers) != 0 {
		t.Errorf("the node holds %d staged volumes and %d fetch loops, want none of either",
			len(answering.staged), len(answering.followers))
	}
}

func TestAnUnstageReportsTheDirectoryItCannotRemove(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, _ := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "never"})
	readOnlyDir(t, filepath.Join(answering.store.root, "volumes"))

	if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: "franchises", StagingTargetPath: staged.StagingTargetPath,
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
	if !strings.Contains(logs.String(), "the volume's directory stayed") {
		t.Errorf("the log is %q, want the directory it could not remove in it", logs)
	}
}

func TestAReadOnlyClaimReportsOnEveryPodAndOnTheClaim(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "franchises", "")
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	staged, held := stagedReadOnly(t, answering, "franchises", url, map[string]string{"pull": "1h"})
	publishedTo(t, answering, staged, "reader-a")
	publishedTo(t, answering, staged, "reader-b")

	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("removing the forge: %v", err)
	}
	loop := followerOf(answering, url)
	loop.refresh(t.Context(), held)

	pods, claims := 0, 0
	for _, posted := range eventsOf(t, answering) {
		if posted.Reason != reasonFailed {
			continue
		}
		switch posted.InvolvedObject.Kind {
		case "Pod":
			pods++
		case "PersistentVolumeClaim":
			claims++
		}
	}
	if pods != 2 || claims != 1 {
		t.Errorf("the failed fetch reached %d pods and %d claims, want 2 and 1", pods, claims)
	}
	abnormal, found := abnormalOf(t, answering.readings, "home", "franchises")
	if !found || abnormal != 1 {
		t.Errorf("git_csi_volume_abnormal reads %v (found: %v), want 1 under the claim's namespace",
			abnormal, found)
	}
}

func TestAStaleReadOnlyStageReportsOnTheClaim(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "franchises", "")
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	stagedReadOnly(t, answering, "first", url, map[string]string{"pull": "never"})
	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("removing the forge: %v", err)
	}

	stagedReadOnly(t, answering, "franchises", url,
		map[string]string{"pull": "never", "offline": "allowStale"})
	posted := eventsOf(t, answering)
	if len(posted) != 1 || posted[0].Reason != reasonStale ||
		posted[0].InvolvedObject.Kind != "PersistentVolumeClaim" {
		t.Errorf("the stale stage posted %v, want one stale event on the claim", posted)
	}
}

func TestAReadOnlyPublishReportsWhatItCannotBind(t *testing.T) {
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
			boundVolume(t, answering, "franchises", "")
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			staged, _ := stagedReadOnly(t, answering, "franchises", fileURL(source),
				map[string]string{"pull": "never"})
			request := readOnlyPublish(t, staged, "reader-a")
			c.stand(t, calls, request)

			_, err := answering.NodePublishVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.Internal)
			}
			claimed := false
			for _, posted := range eventsOf(t, answering) {
				if posted.Reason == reasonRefused &&
					posted.InvolvedObject.Kind == "PersistentVolumeClaim" {
					claimed = true
				}
			}
			if !claimed {
				t.Errorf("the refused publish posted %v, want a refusal on the claim",
					eventsOf(t, answering))
			}
		})
	}
}

// The container runtime binds a volume into the pod read-write unless
// the pod asks for read-only, whatever the driver's own bind says, so
// a publish that does not ask is refused before anything is bound.
func TestAReadOnlyClaimRefusesAPodThatDoesNotAskForReadOnly(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	boundVolume(t, answering, "franchises", "")
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, _ := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "never"})
	request := readOnlyPublish(t, staged, "reader-a")
	request.Readonly = false
	before := len(calls.mounts)

	_, err := answering.NodePublishVolume(t.Context(), request)

	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.InvalidArgument)
	}
	if !strings.Contains(err.Error(), "readOnly") {
		t.Errorf("the refusal reads %q, want it to name readOnly", err)
	}
	if len(calls.mounts) != before {
		t.Errorf("the refused publish made %d mounts, want none", len(calls.mounts)-before)
	}
	onPod := false
	for _, posted := range eventsOf(t, answering) {
		if posted.Reason == reasonRefused && posted.InvolvedObject.Kind == "Pod" {
			onPod = true
		}
	}
	if !onPod {
		t.Errorf("the refused publish posted %v, want a refusal on the pod", eventsOf(t, answering))
	}
}

func TestADriverThatRestartsTakesBackAReadOnlyClaim(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "franchises", "")
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, _ := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "1h"})
	first := publishedTo(t, answering, staged, "reader-a")
	second := publishedTo(t, answering, staged, "reader-b")

	held := recordOf(t, answering, "franchises")
	if held.Staging != staged.StagingTargetPath || held.Kind != readOnlyKind {
		t.Errorf("the record is %+v, want a read-only claim staged at %s",
			held, staged.StagingTargetPath)
	}

	again := restartedWith(t, answering, map[string]bool{first.TargetPath: true})
	again.mu.Lock()
	defer again.mu.Unlock()
	resumed, standing := again.volumes["franchises"]
	if !standing {
		t.Fatal("the driver took back no read-only claim")
	}
	if got := resumed.boundTargets(); !sameStrings(got, []string{first.TargetPath}) {
		t.Errorf("the resumed volume is bound at %v, want the one target still mounted %s",
			got, first.TargetPath)
	}
	if _, ok := again.staged["franchises"]; !ok {
		t.Error("the resumed volume is published and not staged")
	}
	if got := recordOf(t, again, "franchises").Targets; !sameStrings(got, []string{first.TargetPath}) {
		t.Errorf("the record carries %v, want the one target still mounted %s",
			got, first.TargetPath)
	}
	if len(again.followers) != 1 {
		t.Errorf("the driver holds %d fetch loops, want 1", len(again.followers))
	}
	if resumed.claimNow().name != "config" {
		t.Errorf("the resumed volume names the claim %+v, want home/config", resumed.claimNow())
	}
	if again.mounted(second.TargetPath) {
		t.Error("the test's mount table holds the target the pod gave up")
	}
}

func TestAReadOnlyClaimNoPodStillReadsGoesWithItsDirectory(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, _ := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "1h"})
	publishedTo(t, answering, staged, "reader-a")

	again := restartedWith(t, answering, map[string]bool{})
	if _, err := os.Stat(answering.store.volumeDir("franchises")); err == nil {
		t.Error("the directory of a claim no pod still reads stayed")
	}
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.volumes) != 0 || len(again.staged) != 0 {
		t.Errorf("the driver took back %d published and %d staged volumes, want none of either",
			len(again.volumes), len(again.staged))
	}
}

// restartedWith is a second driver on the same store whose mount table
// holds the paths the map names.
func restartedWith(t *testing.T, answering *node, mounted map[string]bool) *node {
	t.Helper()
	again, _ := testNode(t, io.Discard)
	again.store = answering.store
	again.events = answering.events
	again.arms.client = answering.arms.client
	again.mounted = func(path string) bool { return mounted[path] }
	again.resume(t.Context())
	return again
}

// sameStrings reports whether the two lists hold the same paths in the
// same order.
func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// unpublish takes one target away and fails the test when the call
// does.
func unpublish(t *testing.T, answering *node, id, target string) {
	t.Helper()
	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: id, TargetPath: target,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
}

func TestTheRecordNamesTheKindOfEveryVolume(t *testing.T) {
	for _, c := range []struct {
		name    string
		held    *record
		kind    volumeKind
		targets []string
	}{
		{
			name:    "an inline volume",
			held:    &record{Kind: inlineKind, Target: "/mount"},
			kind:    inlineVolume,
			targets: []string{"/mount"},
		},
		{
			name:    "a read-only claim",
			held:    &record{Kind: readOnlyKind, Targets: []string{"/a", "/b"}},
			kind:    readOnlyClaim,
			targets: []string{"/a", "/b"},
		},
		{
			name:    "a writeable volume",
			held:    &record{Kind: writeableKind, Target: "/mount"},
			kind:    writeableVolume,
			targets: []string{"/mount"},
		},
		{
			// A record from a driver that wrote no kind.
			name:    "an ephemeral record with no kind",
			held:    &record{Ephemeral: true, Target: "/mount"},
			kind:    inlineVolume,
			targets: []string{"/mount"},
		},
		{
			name:    "a record with no kind that is not ephemeral",
			held:    &record{Target: "/mount"},
			kind:    writeableVolume,
			targets: []string{"/mount"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.held.kind(); got != c.kind {
				t.Errorf("the record is kind %v, want %v", got, c.kind)
			}
			if got := c.held.targetPaths(); !sameStrings(got, c.targets) {
				t.Errorf("the record names %v, want %v", got, c.targets)
			}
		})
	}
}

func TestAReadOnlyClaimTellsThePodsItIsPublishedTo(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	held := &volume{
		id:      "franchises",
		kind:    readOnlyClaim,
		targets: map[string]podReference{},
		claim:   claimReference{namespace: "home", name: "config"},
	}
	held.bind("/a", podReference{name: "reader-a", namespace: "home"})

	answering.tell(t.Context(), held, corev1.EventTypeWarning, reasonStale, "the node's copy")
	posted := eventsOf(t, answering)
	if len(posted) != 2 {
		t.Fatalf("the volume told %d objects, want the pod and the claim", len(posted))
	}
}

func TestADriverOutsideAClusterStagesAReadOnlyClaim(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.arms.client = nil
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, held := stagedReadOnly(t, answering, "franchises", fileURL(source),
		map[string]string{"pull": "never"})

	if held.claimNow().name != "" {
		t.Errorf("the volume names the claim %+v, want none", held.claimNow())
	}
	if got := readTree(t, held.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the tree holds %v, want the commit", got)
	}
}

func TestAReadOnlyStageReportsTheDirectoryItCannotMake(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "franchises", "")
	volumes := filepath.Join(answering.store.root, "volumes")
	if err := os.MkdirAll(volumes, 0o700); err != nil {
		t.Fatalf("making the store: %v", err)
	}
	readOnlyDir(t, volumes)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})

	staged := readOnlyStage(t, "franchises", fileURL(source), map[string]string{"pull": "never"})
	_, err := answering.NodeStageVolume(t.Context(), staged)
	if got := status.Code(err); got != codes.Internal {
		t.Fatalf("NodeStageVolume answered %v, want %v", got, codes.Internal)
	}
	posted := eventsOf(t, answering)
	if len(posted) != 1 || posted[0].InvolvedObject.Kind != "PersistentVolumeClaim" {
		t.Errorf("the refused stage posted %v, want one refusal on the claim", posted)
	}
}
