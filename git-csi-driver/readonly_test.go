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
	corev1 "k8s.io/api/core/v1"
)

// readOnlyStage returns the NodeStageVolume request that the kubelet
// sends for a claim with the ReadOnlyMany access mode.
func readOnlyStage(t testing.TB, id, url string, extra map[string]string) *csi.NodeStageVolumeRequest {
	t.Helper()
	request := stageRequest(t, id, url, extra)
	request.VolumeCapability = capabilityOf(
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY)
	return request
}

// readOnlyPublish returns the NodePublishVolume request that one pod
// causes for a staged read-only claim, with a target path of its own.
func readOnlyPublish(
	t testing.TB, staged *csi.NodeStageVolumeRequest, pod string,
) *csi.NodePublishVolumeRequest {
	t.Helper()
	request := persistentPublish(t, staged)
	request.VolumeContext[podNameKey] = pod
	request.VolumeContext[podUIDKey] = pod
	request.Readonly = true
	return request
}

// stagedReadOnly stages one read-only claim and returns the stage
// request and the volume the node recorded for it.
func stagedReadOnly(
	t *testing.T, n *node, id, url string, extra map[string]string,
) (*csi.NodeStageVolumeRequest, *volume) {
	t.Helper()
	staged := readOnlyStage(t, id, url, extra)
	if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return staged, n.staged[id]
}

// publishedTo publishes a staged read-only claim for one more pod.
func publishedTo(
	t *testing.T, n *node, staged *csi.NodeStageVolumeRequest, pod string,
) *csi.NodePublishVolumeRequest {
	t.Helper()
	request := readOnlyPublish(t, staged, pod)
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume for %s: %v", pod, err)
	}
	return request
}

func TestAReadOnlyStagePlacesTheTreeAndFollowsIt(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, vol := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "1h", "depth": "1", "offline": "allowStale"})

	if vol.kind != readOnlyClaim {
		t.Fatalf("the volume is %v, want a read-only claim", vol.kind)
	}
	if got := readTree(t, vol.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the tree has %v, want the commit", got)
	}
	if _, err := os.Stat(filepath.Join(vol.directory, "git")); err == nil {
		t.Error("a read-only claim made a git directory")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.watchers) != 0 || len(n.armingCancels) != 0 {
		t.Errorf("the node has %d watches and %d arming loops, want none of either",
			len(n.watchers), len(n.armingCancels))
	}
	if len(n.followers) != 1 {
		t.Errorf("the node has %d fetch loops, want 1", len(n.followers))
	}
}

func TestAReadOnlyStageAcceptsTheAttributesOfAnInlineVolume(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, vol := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "never", "offline": "allowStale"})

	if vol.attributes.pull.follows() || vol.attributes.offline != offlineAllowStale {
		t.Errorf("the volume is %+v, want pull never and offline allowStale", vol.attributes)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.followers) != 0 {
		t.Errorf("a volume with pull never joined %d fetch loops, want none",
			len(n.followers))
	}
}

func TestAReadOnlyStageRefusesAForgeItCannotReach(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "franchises", "")
		staged := readOnlyStage(t, "franchises",
			fileURL(filepath.Join(t.TempDir(), "gone")), map[string]string{"pull": "never"})

		_, err := n.NodeStageVolume(t.Context(), staged)
		if got := status.Code(err); got != codes.Unavailable {
			t.Fatalf("NodeStageVolume returned %v, want %v", got, codes.Unavailable)
		}
		if _, err := os.Stat(n.store.volumeDir("franchises")); err == nil {
			t.Error("a refused stage left the volume's directory behind")
		}
		posted := eventsOf(t, n)
		if len(posted) != 1 || posted[0].Reason != reasonRefused ||
			posted[0].InvolvedObject.Kind != "PersistentVolumeClaim" {
			t.Errorf("the refused stage posted %v, want one refusal on the claim", posted)
		}
	})
}

func TestAReadOnlyClaimBindsEveryPodOnTheNode(t *testing.T) {
	n, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, vol := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "never"})

	first := publishedTo(t, n, staged, "reader-a")
	second := publishedTo(t, n, staged, "reader-b")
	want := []string{first.TargetPath, second.TargetPath}
	if got := vol.boundTargets(); !sameStrings(got, want) {
		t.Errorf("the volume is bound at %v, want %v", got, want)
	}
	for _, mount := range calls.mounts {
		if mount.source != vol.tree {
			t.Errorf("the driver bound %s, want the staged tree %s", mount.source, vol.tree)
		}
	}
	if calls.mounts[1].flags&unix.MS_RDONLY == 0 {
		t.Errorf("the bind is %+v, want it read-only", calls.mounts[1])
	}

	binds := len(calls.mounts)
	if _, err := n.NodePublishVolume(t.Context(), first); err != nil {
		t.Fatalf("a repeated NodePublishVolume: %v", err)
	}
	if len(calls.mounts) != binds || len(vol.boundTargets()) != 2 {
		t.Errorf("a repeated publish made %d mounts and left %d targets, want 0 and 2",
			len(calls.mounts)-binds, len(vol.boundTargets()))
	}
}

func TestAReadOnlyClaimKeepsItsOtherTargetsAtAnUnpublish(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, vol := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "1h"})
	first := publishedTo(t, n, staged, "reader-a")
	second := publishedTo(t, n, staged, "reader-b")

	unpublish(t, n, "franchises", first.TargetPath)
	if got := vol.boundTargets(); !sameStrings(got, []string{second.TargetPath}) {
		t.Errorf("the volume is bound at %v, want %s", got, second.TargetPath)
	}
	if got := recordOf(t, n, "franchises").Targets; !sameStrings(got, []string{second.TargetPath}) {
		t.Errorf("the record has %v, want %s", got, second.TargetPath)
	}
	n.mu.Lock()
	_, published := n.volumes["franchises"]
	n.mu.Unlock()
	if !published {
		t.Error("the volume left the node while a pod still reads it")
	}

	unpublish(t, n, "franchises", second.TargetPath)
	n.mu.Lock()
	_, published = n.volumes["franchises"]
	_, isStaged := n.staged["franchises"]
	loops := len(n.followers)
	n.mu.Unlock()
	if published || !isStaged {
		t.Errorf("after the last unpublish the volume is published: %v, staged: %v, want false and true",
			published, isStaged)
	}
	if loops != 1 {
		t.Errorf("the node has %d fetch loops after the last unpublish, want 1", loops)
	}
	if _, err := os.Stat(vol.tree); err != nil {
		t.Errorf("the staged tree went with the last unpublish: %v", err)
	}
}

func TestTheUnstageOfAReadOnlyClaimRemovesTheTree(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, vol := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "1h"})
	request := publishedTo(t, n, staged, "reader-a")
	unpublish(t, n, "franchises", request.TargetPath)

	if _, err := n.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: "franchises", StagingTargetPath: staged.StagingTargetPath,
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
	if _, err := os.Stat(vol.directory); err == nil {
		t.Error("the volume's directory stayed after the unstage")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.staged) != 0 || len(n.followers) != 0 {
		t.Errorf("the node has %d staged volumes and %d fetch loops, want none of either",
			len(n.staged), len(n.followers))
	}
}

func TestAnUnstageReportsTheDirectoryItCannotRemove(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, _ := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "never"})
	readOnlyDir(t, filepath.Join(n.store.root, "volumes"))

	if _, err := n.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: "franchises", StagingTargetPath: staged.StagingTargetPath,
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
	if !strings.Contains(logs.String(), "The volume directory was not removed") {
		t.Errorf("the log is %q, want it to report the volume directory that was not removed", logs)
	}
}

func TestAReadOnlyClaimReportsOnEveryPodAndOnTheClaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "franchises", "")
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		staged, vol := stagedReadOnly(t, n, "franchises", url, map[string]string{"pull": "1h"})
		publishedTo(t, n, staged, "reader-a")
		publishedTo(t, n, staged, "reader-b")

		if err := os.RemoveAll(source); err != nil {
			t.Fatalf("removing the forge: %v", err)
		}
		// Refresh the loop that the node created, because a loop records
		// health only for the volumes it has.
		n.mu.Lock()
		loop := n.loopOf(vol)
		n.mu.Unlock()
		loop.refresh(t.Context(), vol)

		pods, claims := 0, 0
		for _, posted := range eventsOf(t, n) {
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
		abnormal, found := abnormalOf(t, n.metrics, "home", "franchises")
		if !found || abnormal != 1 {
			t.Errorf("git_csi_volume_abnormal reads %v (found: %v), want 1 under the claim's namespace",
				abnormal, found)
		}
	})
}

func TestAStaleReadOnlyStageReportsOnTheClaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "franchises", "")
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		stagedReadOnly(t, n, "first", url, map[string]string{"pull": "never"})
		if err := os.RemoveAll(source); err != nil {
			t.Fatalf("removing the forge: %v", err)
		}

		stagedReadOnly(t, n, "franchises", url,
			map[string]string{"pull": "never", "offline": "allowStale"})
		posted := eventsOf(t, n)
		if len(posted) != 1 || posted[0].Reason != reasonStale ||
			posted[0].InvolvedObject.Kind != "PersistentVolumeClaim" {
			t.Errorf("the stale stage posted %v, want one stale event on the claim", posted)
		}
	})
}

func TestAReadOnlyPublishReportsWhatItCannotBind(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, calls *recordedMounts, request *csi.NodePublishVolumeRequest)
	}{
		{
			name: "a target path under a file",
			setup: func(t *testing.T, _ *recordedMounts, request *csi.NodePublishVolumeRequest) {
				file := filepath.Join(t.TempDir(), "file")
				writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
				request.TargetPath = filepath.Join(file, "mount")
			},
		},
		{
			name: "a mount that stays",
			setup: func(_ *testing.T, calls *recordedMounts, _ *csi.NodePublishVolumeRequest) {
				calls.unmountErr = unix.EPERM
			},
		},
		{
			name: "a bind it cannot make",
			setup: func(_ *testing.T, calls *recordedMounts, _ *csi.NodePublishVolumeRequest) {
				calls.failAt = 1
				calls.mountErr = unix.EPERM
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, calls := testNode(t, io.Discard)
				boundVolume(t, n, "franchises", "")
				source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
				staged, _ := stagedReadOnly(t, n, "franchises", fileURL(source),
					map[string]string{"pull": "never"})
				request := readOnlyPublish(t, staged, "reader-a")
				c.setup(t, calls, request)

				_, err := n.NodePublishVolume(t.Context(), request)
				if got := status.Code(err); got != codes.Internal {
					t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.Internal)
				}
				claimed := false
				for _, posted := range eventsOf(t, n) {
					if posted.Reason == reasonRefused &&
						posted.InvolvedObject.Kind == "PersistentVolumeClaim" {
						claimed = true
					}
				}
				if !claimed {
					t.Errorf("the refused publish posted %v, want a refusal on the claim",
						eventsOf(t, n))
				}
			})
		})
	}
}

func TestAReadOnlyClaimRefusesAPodThatDoesNotAskForReadOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, calls := testNode(t, io.Discard)
		boundVolume(t, n, "franchises", "")
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		staged, _ := stagedReadOnly(t, n, "franchises", fileURL(source),
			map[string]string{"pull": "never"})
		request := readOnlyPublish(t, staged, "reader-a")
		request.Readonly = false
		before := len(calls.mounts)

		_, err := n.NodePublishVolume(t.Context(), request)

		if got := status.Code(err); got != codes.InvalidArgument {
			t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.InvalidArgument)
		}
		if !strings.Contains(err.Error(), "readOnly") {
			t.Errorf("the refusal reads %q, want it to name readOnly", err)
		}
		if len(calls.mounts) != before {
			t.Errorf("the refused publish made %d mounts, want none", len(calls.mounts)-before)
		}
		onPod := false
		for _, posted := range eventsOf(t, n) {
			if posted.Reason == reasonRefused && posted.InvolvedObject.Kind == "Pod" {
				onPod = true
			}
		}
		if !onPod {
			t.Errorf("the refused publish posted %v, want a refusal on the pod", eventsOf(t, n))
		}
	})
}

func TestARestartedDriverResumesAReadOnlyClaim(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	boundVolume(t, n, "franchises", "")
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, _ := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "1h"})
	first := publishedTo(t, n, staged, "reader-a")
	second := publishedTo(t, n, staged, "reader-b")

	rec := recordOf(t, n, "franchises")
	if rec.Staging != staged.StagingTargetPath || rec.Kind != readOnlyKind {
		t.Errorf("the record is %+v, want a read-only claim staged at %s",
			rec, staged.StagingTargetPath)
	}

	again := restartedWith(t, n, map[string]bool{first.TargetPath: true})
	again.mu.Lock()
	defer again.mu.Unlock()
	resumed, published := again.volumes["franchises"]
	if !published {
		t.Fatal("the driver resumed no read-only claim")
	}
	if got := resumed.boundTargets(); !sameStrings(got, []string{first.TargetPath}) {
		t.Errorf("the resumed volume is bound at %v, want the one target still mounted %s",
			got, first.TargetPath)
	}
	if _, ok := again.staged["franchises"]; !ok {
		t.Error("the resumed volume is published and not staged")
	}
	if got := recordOf(t, again, "franchises").Targets; !sameStrings(got, []string{first.TargetPath}) {
		t.Errorf("the record has %v, want the one target still mounted %s",
			got, first.TargetPath)
	}
	if len(again.followers) != 1 {
		t.Errorf("the driver has %d fetch loops, want 1", len(again.followers))
	}
	if resumed.claimNow().name != "config" {
		t.Errorf("the resumed volume names the claim %+v, want home/config", resumed.claimNow())
	}
	if again.mounted(second.TargetPath) {
		t.Error("the test's mount table has the target the pod unmounted")
	}
}

func TestAReadOnlyClaimNoPodStillReadsGoesWithItsDirectory(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	staged, _ := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "1h"})
	publishedTo(t, n, staged, "reader-a")

	again := restartedWith(t, n, map[string]bool{})
	if _, err := os.Stat(n.store.volumeDir("franchises")); err == nil {
		t.Error("the directory of a claim no pod still reads stayed")
	}
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.volumes) != 0 || len(again.staged) != 0 {
		t.Errorf("the driver resumed %d published and %d staged volumes, want none of either",
			len(again.volumes), len(again.staged))
	}
}

// restartedWith returns a second driver on the same store, as after a
// restart, whose mount table already contains the paths in the map.
func restartedWith(t *testing.T, n *node, mounted map[string]bool) *node {
	t.Helper()
	again, _ := testNode(t, io.Discard)
	again.store = n.store
	again.events = n.events
	again.arming.client = n.arming.client
	again.mounted = func(path string) bool { return mounted[path] }
	again.resume(t.Context())
	return again
}

// sameStrings reports whether two lists have the same strings in the
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

func unpublish(t *testing.T, n *node, id, target string) {
	t.Helper()
	if _, err := n.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: id, TargetPath: target,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
}

func TestTheRecordNamesTheKindOfEveryVolume(t *testing.T) {
	for _, c := range []struct {
		name    string
		rec     *record
		kind    volumeKind
		targets []string
	}{
		{
			name:    "an inline volume",
			rec:     &record{Kind: inlineKind, Target: "/mount"},
			kind:    inlineVolume,
			targets: []string{"/mount"},
		},
		{
			name:    "a read-only claim",
			rec:     &record{Kind: readOnlyKind, Targets: []string{"/a", "/b"}},
			kind:    readOnlyClaim,
			targets: []string{"/a", "/b"},
		},
		{
			name:    "a writeable volume",
			rec:     &record{Kind: writeableKind, Target: "/mount"},
			kind:    writeableVolume,
			targets: []string{"/mount"},
		},
		{
			name:    "an ephemeral record with no kind",
			rec:     &record{Ephemeral: true, Target: "/mount"},
			kind:    inlineVolume,
			targets: []string{"/mount"},
		},
		{
			name:    "a record with no kind that is not ephemeral",
			rec:     &record{Target: "/mount"},
			kind:    writeableVolume,
			targets: []string{"/mount"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rec.kind(); got != c.kind {
				t.Errorf("the record is kind %v, want %v", got, c.kind)
			}
			if got := c.rec.targetPaths(); !sameStrings(got, c.targets) {
				t.Errorf("the record names %v, want %v", got, c.targets)
			}
		})
	}
}

func TestAReadOnlyClaimPostsEventsOnThePodsItIsPublishedTo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol := &volume{
			id:      "franchises",
			kind:    readOnlyClaim,
			targets: map[string]podReference{},
			claim:   claimReference{namespace: "home", name: "config"},
		}
		vol.bind("/a", podReference{name: "reader-a", namespace: "home"})

		n.postVolumeEvent(vol, corev1.EventTypeWarning, reasonStale, "the node's copy")
		posted := eventsOf(t, n)
		if len(posted) != 2 {
			t.Fatalf("the volume posted Events on %d objects, want the pod and the claim", len(posted))
		}
	})
}

func TestADriverOutsideAClusterStagesAReadOnlyClaim(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	n.arming.client = nil
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, vol := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "never"})

	if vol.claimNow().name != "" {
		t.Errorf("the volume names the claim %+v, want none", vol.claimNow())
	}
	if got := readTree(t, vol.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the tree has %v, want the commit", got)
	}
}

func TestAReadOnlyStageReportsTheDirectoryItCannotMake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		boundVolume(t, n, "franchises", "")
		volumes := filepath.Join(n.store.root, "volumes")
		if err := os.MkdirAll(volumes, 0o700); err != nil {
			t.Fatalf("making the store: %v", err)
		}
		readOnlyDir(t, volumes)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})

		staged := readOnlyStage(t, "franchises", fileURL(source), map[string]string{"pull": "never"})
		_, err := n.NodeStageVolume(t.Context(), staged)
		if got := status.Code(err); got != codes.Internal {
			t.Fatalf("NodeStageVolume returned %v, want %v", got, codes.Internal)
		}
		posted := eventsOf(t, n)
		if len(posted) != 1 || posted[0].InvolvedObject.Kind != "PersistentVolumeClaim" {
			t.Errorf("the refused stage posted %v, want one refusal on the claim", posted)
		}
	})
}
