package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/container-storage-interface/spec/lib/go/csi"
	kevents "github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// driver is the node service under test, with the fake mount calls and
// the fake API server that receives its Events.
type driver struct {
	*node
	mounts *recordedMounts
	posted *eventstest.Events
}

// testDriver builds a node with a store in a temporary directory and an
// empty mount table.
func testDriver(t *testing.T) *driver {
	t.Helper()
	return driverIn(t, t.TempDir())
}

// driverIn is passed the store's root, so a test can give the driver a
// store it cannot write, or the store of an earlier driver.
func driverIn(t *testing.T, root string) *driver {
	t.Helper()
	e, posted := fakeClusterEvents(t, quietLogger())
	calls := &recordedMounts{}
	d := newNode(
		&config{nodeID: "node-1", store: root},
		e,
		newMetrics(),
		quietLogger(),
	)
	d.mounts = calls
	// A mount table file that does not exist lists no mounts.
	d.mountinfo = filepath.Join(t.TempDir(), "mountinfo")
	return &driver{node: d, mounts: calls, posted: posted}
}

func aPod(name, uid string) podReference {
	return podReference{name: name, namespace: "example", uid: uid}
}

func publishing(handle, target string, pod podReference) *csi.NodePublishVolumeRequest {
	return &csi.NodePublishVolumeRequest{
		VolumeId:   handle,
		TargetPath: target,
		VolumeContext: map[string]string{
			podNameKey:      pod.name,
			podNamespaceKey: pod.namespace,
			podUIDKey:       pod.uid,
		},
	}
}

// onlyReason fails the test unless the fake API server has exactly one
// Event, a Warning with the wanted reason. It calls synctest.Wait so the
// recorder can post its queue, so it must run inside a synctest bubble.
func onlyReason(t *testing.T, posted *eventstest.Events, want string) {
	t.Helper()
	synctest.Wait()
	got := posted.List()
	if len(got) != 1 || got[0].Reason != want || got[0].Type != kevents.TypeWarning {
		t.Errorf("the pod has %+v, want one Warning %s", got, want)
	}
}

func TestNodeGetInfoReturnsTheNodeIDAndNoTopology(t *testing.T) {
	d := testDriver(t)
	info, err := d.NodeGetInfo(t.Context(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo: %v", err)
	}
	if info.GetNodeId() != "node-1" {
		t.Errorf("NodeGetInfo named %q, want node-1", info.GetNodeId())
	}
	if info.GetAccessibleTopology() != nil {
		t.Errorf("NodeGetInfo returned topology %v, want none", info.GetAccessibleTopology())
	}
}

func TestPublishMakesTheCopyAndBindsItUnderThePod(t *testing.T) {
	d := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	info, err := os.Stat(d.store.copyPath("example-store"))
	if err != nil {
		t.Fatalf("reading the copy after publishing: %v", err)
	}
	if info.Mode().Perm() != copyMode {
		t.Errorf("the copy has mode %o, want %o", info.Mode().Perm(), copyMode)
	}
	if len(d.mounts.mounts) != 1 {
		t.Fatalf("the driver made %v, want one bind", d.mounts.mounts)
	}
	made := d.mounts.mounts[0]
	if made.source != d.store.copyPath("example-store") || made.target != target {
		t.Errorf("the driver bound %s onto %s, want the copy onto the target", made.source, made.target)
	}
	if made.flags != unix.MS_BIND {
		t.Errorf("the driver bound with flags %d, want a read-write bind", made.flags)
	}
}

func TestPublishBindsReadOnlyWhenTheRequestAsks(t *testing.T) {
	d := testDriver(t)
	request := publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1"))
	request.Readonly = true
	if _, err := d.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if len(d.mounts.mounts) != 2 {
		t.Fatalf("the driver made %v, want a bind and a read-only remount", d.mounts.mounts)
	}
}

func TestPublishKeepsTheFilesAlreadyInTheCopy(t *testing.T) {
	d := testDriver(t)
	if err := d.store.makeCopy("example-store"); err != nil {
		t.Fatalf("making the copy: %v", err)
	}
	kept := filepath.Join(d.store.copyPath("example-store"), "one")
	if err := os.WriteFile(kept, []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"),
			aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	content, err := os.ReadFile(kept)
	if err != nil || string(content) != "hello" {
		t.Errorf("the file reads %q with %v, want hello", content, err)
	}
}

func TestPublishRefusesAHandleTheDriverCannotPutUnderTheStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := testDriver(t)
		_, err := d.NodePublishVolume(t.Context(),
			publishing("Bad/Handle", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("NodePublishVolume returned %v, want InvalidArgument", err)
		}
		onlyReason(t, d.posted, reasonRefused)
	})
}

func TestPublishRefusesACallWithNoTargetPath(t *testing.T) {
	d := testDriver(t)
	_, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", "", aPod("reader", "pod-uid-1")))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("NodePublishVolume returned %v, want InvalidArgument", err)
	}
}

func TestPublishGivesASecondPodOnThisNodeTheSameCopy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := testDriver(t)
		for _, pod := range []podReference{aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2")} {
			if _, err := d.NodePublishVolume(t.Context(),
				publishing("example-store", filepath.Join(t.TempDir(), pod.name), pod)); err != nil {
				t.Fatalf("NodePublishVolume for %s: %v", pod.name, err)
			}
		}
		copied := d.store.copyPath("example-store")
		if len(d.mounts.mounts) != 2 ||
			d.mounts.mounts[0].source != copied || d.mounts.mounts[1].source != copied {
			t.Errorf("the driver made %v, want two binds of %s", d.mounts.mounts, copied)
		}
		if records := d.recordsOf("example-store"); len(records) != 2 {
			t.Errorf("the handle has records %+v, want one for each pod", records)
		}
		synctest.Wait()
		if posted := d.posted.List(); len(posted) != 0 {
			t.Errorf("the pods have Events %+v, want none", posted)
		}
	})
}

func TestPublishRepeatedForTheSamePodAndTargetMountsOnce(t *testing.T) {
	d := testDriver(t)
	request := publishing("example-store", filepath.Join(t.TempDir(), "mount"),
		aPod("reader", "pod-uid-1"))
	for range 2 {
		if _, err := d.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
	}
	if len(d.mounts.mounts) != 1 {
		t.Errorf("the driver made %v, want one bind", d.mounts.mounts)
	}
}

func TestPublishRecordsEveryTargetOfTheSamePod(t *testing.T) {
	d := testDriver(t)
	pod := aPod("reader", "pod-uid-1")
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for _, target := range []string{first, second} {
		if _, err := d.NodePublishVolume(t.Context(),
			publishing("example-store", target, pod)); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
	}
	records := d.recordsOf("example-store")
	if len(records) != 2 || records[0].Target != first || records[1].Target != second {
		t.Errorf("the handle has records %+v, want %s and %s", records, first, second)
	}
}

func TestPublishReportsACopyItCannotMake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "store")
		if err := os.WriteFile(root, []byte("not a directory\n"), 0o600); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
		d := driverIn(t, root)
		_, err := d.NodePublishVolume(t.Context(),
			publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
		if status.Code(err) != codes.Internal {
			t.Fatalf("NodePublishVolume returned %v, want Internal", err)
		}
		onlyReason(t, d.posted, reasonMountFailed)
	})
}

func TestPublishReportsATargetItCannotMake(t *testing.T) {
	d := testDriver(t)
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	_, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(blocked, "mount"), aPod("reader", "pod-uid-1")))
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodePublishVolume returned %v, want Internal", err)
	}
}

func TestPublishReportsAnOldMountItCannotTakeAway(t *testing.T) {
	d := testDriver(t)
	d.mounts.unmountErr = unix.EPERM
	_, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodePublishVolume returned %v, want Internal", err)
	}
}

func TestPublishReportsABindThatFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := testDriver(t)
		d.mounts.failAt = 1
		d.mounts.mountErr = unix.EPERM
		_, err := d.NodePublishVolume(t.Context(),
			publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
		if status.Code(err) != codes.Internal {
			t.Fatalf("NodePublishVolume returned %v, want Internal", err)
		}
		onlyReason(t, d.posted, reasonMountFailed)
	})
}

func TestUnpublishTakesTheMountAwayAndLeavesTheCopy(t *testing.T) {
	d := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if _, err := d.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: target,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if _, err := os.Stat(d.store.copyPath("example-store")); err != nil {
		t.Errorf("the copy is gone after the unpublish, want it kept: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("the target returned %v, want it gone", err)
	}
	if records := d.recordsOf("example-store"); len(records) != 0 {
		t.Errorf("the handle has records %+v, want none", records)
	}
}

func TestPublishSucceedsForANewPodAfterTheFirstPodUnpublished(t *testing.T) {
	d := testDriver(t)
	first := filepath.Join(t.TempDir(), "first")
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", first, aPod("first", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if _, err := d.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: first,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "second"),
			aPod("second", "pod-uid-2"))); err != nil {
		t.Errorf("NodePublishVolume: %v", err)
	}
}

func TestUnpublishRepeatedForTheSameTargetSucceeds(t *testing.T) {
	d := testDriver(t)
	request := &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: filepath.Join(t.TempDir(), "mount"),
	}
	for range 2 {
		if _, err := d.NodeUnpublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodeUnpublishVolume: %v", err)
		}
	}
}

func TestUnpublishRefusesACallThatNamesNothing(t *testing.T) {
	for _, c := range []struct {
		name    string
		handle  string
		target  string
		message string
	}{
		{name: "no volume", target: "/kubelet/mount", message: "volume_id"},
		{name: "no target", handle: "example-store", message: "target_path"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := testDriver(t)
			_, err := d.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
				VolumeId: c.handle, TargetPath: c.target,
			})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("NodeUnpublishVolume returned %v, want InvalidArgument", err)
			}
			if message := status.Convert(err).Message(); !strings.Contains(message, c.message) {
				t.Errorf("the refusal reads %q, want it to name %s", message, c.message)
			}
		})
	}
}

func TestUnpublishReportsAMountItCannotTakeAway(t *testing.T) {
	d := testDriver(t)
	d.mounts.unmountErr = unix.EPERM
	_, err := d.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: filepath.Join(t.TempDir(), "mount"),
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnpublishVolume returned %v, want Internal", err)
	}
}

func TestUnpublishRefusesATargetThatStillHasTheCopysFiles(t *testing.T) {
	d := testDriver(t)
	// Files at the target mean the copy is still mounted there, so a
	// recursive delete would delete the copy's files.
	target := filepath.Join(t.TempDir(), "mount")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("making the target: %v", err)
	}
	kept := filepath.Join(target, "greeting")
	if err := os.WriteFile(kept, []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	_, err := d.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: target,
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnpublishVolume returned %v, want Internal", err)
	}
	content, err := os.ReadFile(kept)
	if err != nil || string(content) != "hello" {
		t.Errorf("the file reads %q with %v, want hello", content, err)
	}
}

func TestUnpublishOfOnePodKeepsTheCopyForTheOther(t *testing.T) {
	d := testDriver(t)
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for target, pod := range map[string]podReference{
		first: aPod("first", "pod-uid-1"), second: aPod("second", "pod-uid-2"),
	} {
		if _, err := d.NodePublishVolume(t.Context(),
			publishing("example-store", target, pod)); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
	}
	if _, err := d.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: first,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	records := d.recordsOf("example-store")
	if len(records) != 1 || records[0].Target != second {
		t.Errorf("the handle has records %+v, want the second pod's alone", records)
	}
	if !d.publishedHandles()["example-store"] {
		t.Error("the sweep would delete the copy, want it kept for the second pod")
	}
}

func TestUnpublishOfATargetThatIsNotMountedLeavesTheOtherRecords(t *testing.T) {
	d := testDriver(t)
	// The kubelet unpublishes the target of a failed publish, where
	// nothing is mounted, so the unmount returns EINVAL.
	d.mounts.unmountErr = unix.EINVAL
	mounted := filepath.Join(t.TempDir(), "mounted")
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", mounted, aPod("first", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if _, err := d.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: filepath.Join(t.TempDir(), "failed"),
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	kept := d.recordsOf("example-store")
	if len(kept) != 1 || kept[0].Target != mounted {
		t.Errorf("the handle has records %+v, want only the record at %s", kept, mounted)
	}
}

func TestVolumeStatsReturnTheCopysBytesAndTheNodesFilesystem(t *testing.T) {
	d := testDriver(t)
	if err := d.store.makeCopy("example-store"); err != nil {
		t.Fatalf("making the copy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d.store.copyPath("example-store"), "one"),
		[]byte("hello"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	response, err := d.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "example-store", VolumePath: "/kubelet/mount",
	})
	if err != nil {
		t.Fatalf("NodeGetVolumeStats: %v", err)
	}
	usage := response.GetUsage()
	if len(usage) != 1 {
		t.Fatalf("NodeGetVolumeStats returned %v, want one usage", usage)
	}
	if usage[0].GetUnit() != csi.VolumeUsage_BYTES {
		t.Errorf("NodeGetVolumeStats returned unit %v, want BYTES", usage[0].GetUnit())
	}
	if usage[0].GetUsed() != 5 {
		t.Errorf("NodeGetVolumeStats returned %d used, want 5", usage[0].GetUsed())
	}
	if usage[0].GetTotal() <= 0 || usage[0].GetAvailable() > usage[0].GetTotal() {
		t.Errorf("NodeGetVolumeStats returned %d available of %d total, want a positive total and no more available than total",
			usage[0].GetAvailable(), usage[0].GetTotal())
	}
}

func TestVolumeStatsPutTheCopysBytesOnTheGauge(t *testing.T) {
	d := testDriver(t)
	if err := d.store.makeCopy("example-store"); err != nil {
		t.Fatalf("making the copy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d.store.copyPath("example-store"), "one"),
		[]byte("hello"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if _, err := d.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "example-store", VolumePath: "/kubelet/mount",
	}); err != nil {
		t.Fatalf("NodeGetVolumeStats: %v", err)
	}
	if got := gaugeValue(t, d.metrics, "example-store"); got != 5 {
		t.Errorf("per_node_csi_copy_bytes reads %v, want 5", got)
	}
}

func TestVolumeStatsRefuseACallThatNamesNothing(t *testing.T) {
	for _, c := range []struct {
		name    string
		handle  string
		path    string
		message string
	}{
		{name: "no volume", path: "/kubelet/mount", message: "volume_id"},
		{name: "no path", handle: "example-store", message: "volume_path"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := testDriver(t)
			_, err := d.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
				VolumeId: c.handle, VolumePath: c.path,
			})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("NodeGetVolumeStats returned %v, want InvalidArgument", err)
			}
			if message := status.Convert(err).Message(); !strings.Contains(message, c.message) {
				t.Errorf("the refusal reads %q, want it to name %s", message, c.message)
			}
		})
	}
}

func TestVolumeStatsReturnNotFoundForAVolumeThisNodeHasNoCopyOf(t *testing.T) {
	d := testDriver(t)
	_, err := d.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "example-store", VolumePath: "/kubelet/mount",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("NodeGetVolumeStats returned %v, want NotFound", err)
	}
}

func TestVolumeStatsReportAFilesystemTheKernelRefused(t *testing.T) {
	d := testDriver(t)
	if err := d.store.makeCopy("example-store"); err != nil {
		t.Fatalf("making the copy: %v", err)
	}
	d.space = func(string) (int64, int64, error) { return 0, 0, unix.EACCES }
	_, err := d.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "example-store", VolumePath: "/kubelet/mount",
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeGetVolumeStats returned %v, want Internal", err)
	}
}

func TestNodeExpandVolumeIsNotServed(t *testing.T) {
	d := testDriver(t)
	_, err := d.NodeExpandVolume(t.Context(), &csi.NodeExpandVolumeRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("NodeExpandVolume returned %v, want Unimplemented", err)
	}
}
