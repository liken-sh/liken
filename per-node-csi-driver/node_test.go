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

// quietLogger discards the driver's log, for a test that reads no log
// line.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// driver is the node service under test with the two fakes a test
// reads: the mount calls the driver made, and the API server its
// Events land in.
type driver struct {
	*node
	mounts *recordedMounts
	posted *eventstest.Events
}

// testDriver builds a node whose store is a directory of the test's own
// and whose mount table is empty.
func testDriver(t *testing.T) *driver {
	t.Helper()
	return driverIn(t, t.TempDir())
}

// driverIn is testDriver with the store path named, so a test can hand
// the driver a store it cannot write.
func driverIn(t *testing.T, root string) *driver {
	t.Helper()
	posting, posted := postingTo(t, quietLogger())
	calls := &recordedMounts{}
	answering := newNode(
		&config{nodeID: "node-1", store: root},
		posting,
		newMetrics(),
		quietLogger(),
	)
	answering.mounts = calls
	// A mount table that does not exist reads as empty, so a fresh driver
	// finds none of its own targets still mounted.
	answering.mountinfo = filepath.Join(t.TempDir(), "mountinfo")
	return &driver{node: answering, mounts: calls, posted: posted}
}

// aPod is the pod the kubelet names in a volume context.
func aPod(name, uid string) podReference {
	return podReference{name: name, namespace: "example", uid: uid}
}

// publishing builds one NodePublishVolume request from the pod, for the
// handle at the target.
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

// onlyReason fails the test unless the fake API server holds exactly
// one Event, a Warning with the wanted reason. It waits for the
// recorder to write its queue first, so the test runs in a synctest
// bubble.
func onlyReason(t *testing.T, posted *eventstest.Events, want string) {
	t.Helper()
	synctest.Wait()
	held := posted.List()
	if len(held) != 1 || held[0].Reason != want || held[0].Type != kevents.TypeWarning {
		t.Errorf("the pod carries %+v, want one Warning %s", held, want)
	}
}

func TestNodeGetInfoNamesTheNodeAndNoTopology(t *testing.T) {
	answering := testDriver(t)
	info, err := answering.NodeGetInfo(t.Context(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo: %v", err)
	}
	if info.GetNodeId() != "node-1" {
		t.Errorf("NodeGetInfo named %q, want node-1", info.GetNodeId())
	}
	if info.GetAccessibleTopology() != nil {
		t.Errorf("NodeGetInfo answered topology %v, want none", info.GetAccessibleTopology())
	}
}

func TestPublishMakesTheCopyAndBindsItUnderThePod(t *testing.T) {
	answering := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	info, err := os.Stat(answering.store.copyPath("example-store"))
	if err != nil {
		t.Fatalf("the copy was not made: %v", err)
	}
	if info.Mode().Perm() != copyMode {
		t.Errorf("the copy has mode %o, want %o", info.Mode().Perm(), copyMode)
	}
	if len(answering.mounts.mounts) != 1 {
		t.Fatalf("the driver made %v, want one bind", answering.mounts.mounts)
	}
	made := answering.mounts.mounts[0]
	if made.source != answering.store.copyPath("example-store") || made.target != target {
		t.Errorf("the driver bound %s onto %s, want the copy onto the target", made.source, made.target)
	}
	if made.flags != unix.MS_BIND {
		t.Errorf("the driver bound with flags %d, want a read-write bind", made.flags)
	}
}

func TestPublishBindsReadOnlyWhenTheRequestAsks(t *testing.T) {
	answering := testDriver(t)
	request := publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1"))
	request.Readonly = true
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if len(answering.mounts.mounts) != 2 {
		t.Fatalf("the driver made %v, want a bind and a read-only remount", answering.mounts.mounts)
	}
}

func TestPublishKeepsWhatTheCopyAlreadyHolds(t *testing.T) {
	answering := testDriver(t)
	if err := answering.store.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	standing := filepath.Join(answering.store.copyPath("example-store"), "one")
	if err := os.WriteFile(standing, []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"),
			aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	content, err := os.ReadFile(standing)
	if err != nil || string(content) != "hello" {
		t.Errorf("the file reads %q with %v, want hello", content, err)
	}
}

func TestPublishRefusesAHandleTheDriverCannotPutUnderTheStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		_, err := answering.NodePublishVolume(t.Context(),
			publishing("Bad/Handle", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("NodePublishVolume answered %v, want InvalidArgument", err)
		}
		onlyReason(t, answering.posted, reasonRefused)
	})
}

func TestPublishRefusesACallWithNoTargetPath(t *testing.T) {
	answering := testDriver(t)
	_, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", "", aPod("reader", "pod-uid-1")))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("NodePublishVolume answered %v, want InvalidArgument", err)
	}
}

func TestPublishRefusesAHandleAnotherPodOnThisNodeHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		if _, err := answering.NodePublishVolume(t.Context(),
			publishing("example-store", filepath.Join(t.TempDir(), "first"),
				aPod("first", "pod-uid-1"))); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
		_, err := answering.NodePublishVolume(t.Context(),
			publishing("example-store", filepath.Join(t.TempDir(), "second"),
				aPod("second", "pod-uid-2")))
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("NodePublishVolume answered %v, want FailedPrecondition", err)
		}
		if message := status.Convert(err).Message(); !strings.Contains(message, "first") {
			t.Errorf("the refusal reads %q, want it to name the pod that holds the copy", message)
		}
		onlyReason(t, answering.posted, reasonHeld)
	})
}

func TestPublishRepeatedForTheSamePodAndTargetMountsOnce(t *testing.T) {
	answering := testDriver(t)
	request := publishing("example-store", filepath.Join(t.TempDir(), "mount"),
		aPod("reader", "pod-uid-1"))
	for range 2 {
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
	}
	if len(answering.mounts.mounts) != 1 {
		t.Errorf("the driver made %v, want one bind", answering.mounts.mounts)
	}
}

func TestPublishMovesTheHoldWhenTheSamePodTakesANewTarget(t *testing.T) {
	answering := testDriver(t)
	pod := aPod("reader", "pod-uid-1")
	second := filepath.Join(t.TempDir(), "second")
	for _, target := range []string{filepath.Join(t.TempDir(), "first"), second} {
		if _, err := answering.NodePublishVolume(t.Context(),
			publishing("example-store", target, pod)); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
	}
	standing, held := answering.holder("example-store")
	if !held || standing.Target != second {
		t.Errorf("the hold names %q, want %q", standing.Target, second)
	}
}

func TestPublishReportsACopyItCannotMake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "store")
		if err := os.WriteFile(root, []byte("not a directory\n"), 0o600); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
		answering := driverIn(t, root)
		_, err := answering.NodePublishVolume(t.Context(),
			publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
		if status.Code(err) != codes.Internal {
			t.Fatalf("NodePublishVolume answered %v, want Internal", err)
		}
		onlyReason(t, answering.posted, reasonMountFailed)
	})
}

func TestPublishReportsATargetItCannotMake(t *testing.T) {
	answering := testDriver(t)
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	_, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(blocked, "mount"), aPod("reader", "pod-uid-1")))
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodePublishVolume answered %v, want Internal", err)
	}
}

func TestPublishReportsAnOldMountItCannotTakeAway(t *testing.T) {
	answering := testDriver(t)
	answering.mounts.unmountErr = unix.EPERM
	_, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodePublishVolume answered %v, want Internal", err)
	}
}

func TestPublishReportsABindThatFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		answering.mounts.failAt = 1
		answering.mounts.mountErr = unix.EPERM
		_, err := answering.NodePublishVolume(t.Context(),
			publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
		if status.Code(err) != codes.Internal {
			t.Fatalf("NodePublishVolume answered %v, want Internal", err)
		}
		onlyReason(t, answering.posted, reasonMountFailed)
	})
}

func TestUnpublishTakesTheMountAwayAndLeavesTheCopy(t *testing.T) {
	answering := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: target,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if _, err := os.Stat(answering.store.copyPath("example-store")); err != nil {
		t.Errorf("the copy went with the pod: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("the target answered %v, want it gone", err)
	}
	if _, held := answering.holder("example-store"); held {
		t.Error("the handle is still held, want it given up")
	}
}

func TestUnpublishLetsTheNextPodTakeTheHandle(t *testing.T) {
	answering := testDriver(t)
	first := filepath.Join(t.TempDir(), "first")
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", first, aPod("first", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: first,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "second"),
			aPod("second", "pod-uid-2"))); err != nil {
		t.Errorf("NodePublishVolume: %v", err)
	}
}

func TestUnpublishAnsweredTwiceIsTheSameAnswerTwice(t *testing.T) {
	answering := testDriver(t)
	request := &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: filepath.Join(t.TempDir(), "mount"),
	}
	for range 2 {
		if _, err := answering.NodeUnpublishVolume(t.Context(), request); err != nil {
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
			answering := testDriver(t)
			_, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
				VolumeId: c.handle, TargetPath: c.target,
			})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("NodeUnpublishVolume answered %v, want InvalidArgument", err)
			}
			if message := status.Convert(err).Message(); !strings.Contains(message, c.message) {
				t.Errorf("the refusal reads %q, want it to name %s", message, c.message)
			}
		})
	}
}

func TestUnpublishReportsAMountItCannotTakeAway(t *testing.T) {
	answering := testDriver(t)
	answering.mounts.unmountErr = unix.EPERM
	_, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: filepath.Join(t.TempDir(), "mount"),
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnpublishVolume answered %v, want Internal", err)
	}
}

func TestUnpublishRefusesATargetThatStillHoldsTheCopysFiles(t *testing.T) {
	answering := testDriver(t)
	// A mount the kernel did not detach still shows the copy's files at
	// the target, so an unpublish that removed the target recursively
	// would take the node's data with it.
	target := filepath.Join(t.TempDir(), "mount")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("making the target: %v", err)
	}
	standing := filepath.Join(target, "greeting")
	if err := os.WriteFile(standing, []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	_, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: target,
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnpublishVolume answered %v, want Internal", err)
	}
	content, err := os.ReadFile(standing)
	if err != nil || string(content) != "hello" {
		t.Errorf("the file reads %q with %v, want hello", content, err)
	}
}

func TestUnpublishingARefusedPodsTargetLeavesTheHoldStanding(t *testing.T) {
	answering := testDriver(t)
	// The kernel holds no mount at the refused pod's target, and an
	// unmount of a path that is not a mount answers EINVAL.
	answering.mounts.unmountErr = unix.EINVAL
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")

	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", first, aPod("first", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", second, aPod("second", "pod-uid-2"))); err == nil {
		t.Fatal("NodePublishVolume answered no error for the second pod, want one")
	}
	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: second,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}

	standing, held := answering.holder("example-store")
	if !held || standing.Target != first {
		t.Fatalf("the hold names %+v, want the first pod at %s", standing, first)
	}

	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "example-store", TargetPath: first,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if _, held := answering.holder("example-store"); held {
		t.Error("the handle is still held, want it given up")
	}
}

func TestVolumeStatsAnswerTheCopysBytesAndTheNodesFilesystem(t *testing.T) {
	answering := testDriver(t)
	if err := answering.store.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(answering.store.copyPath("example-store"), "one"),
		[]byte("hello"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	answer, err := answering.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "example-store", VolumePath: "/kubelet/mount",
	})
	if err != nil {
		t.Fatalf("NodeGetVolumeStats: %v", err)
	}
	usage := answer.GetUsage()
	if len(usage) != 1 {
		t.Fatalf("NodeGetVolumeStats answered %v, want one usage", usage)
	}
	if usage[0].GetUnit() != csi.VolumeUsage_BYTES {
		t.Errorf("NodeGetVolumeStats answered unit %v, want BYTES", usage[0].GetUnit())
	}
	if usage[0].GetUsed() != 5 {
		t.Errorf("NodeGetVolumeStats answered %d used, want 5", usage[0].GetUsed())
	}
	if usage[0].GetTotal() <= 0 || usage[0].GetAvailable() > usage[0].GetTotal() {
		t.Errorf("NodeGetVolumeStats answered %d of %d",
			usage[0].GetAvailable(), usage[0].GetTotal())
	}
}

func TestVolumeStatsPutTheCopysBytesOnTheGauge(t *testing.T) {
	answering := testDriver(t)
	if err := answering.store.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(answering.store.copyPath("example-store"), "one"),
		[]byte("hello"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if _, err := answering.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "example-store", VolumePath: "/kubelet/mount",
	}); err != nil {
		t.Fatalf("NodeGetVolumeStats: %v", err)
	}
	if got := gaugeValue(t, answering.readings, "example-store"); got != 5 {
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
			answering := testDriver(t)
			_, err := answering.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
				VolumeId: c.handle, VolumePath: c.path,
			})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("NodeGetVolumeStats answered %v, want InvalidArgument", err)
			}
			if message := status.Convert(err).Message(); !strings.Contains(message, c.message) {
				t.Errorf("the refusal reads %q, want it to name %s", message, c.message)
			}
		})
	}
}

func TestVolumeStatsAnswerNotFoundForAVolumeThisNodeHasNoCopyOf(t *testing.T) {
	answering := testDriver(t)
	_, err := answering.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "example-store", VolumePath: "/kubelet/mount",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("NodeGetVolumeStats answered %v, want NotFound", err)
	}
}

func TestVolumeStatsReportAFilesystemTheKernelRefused(t *testing.T) {
	answering := testDriver(t)
	if err := answering.store.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	answering.space = func(string) (int64, int64, error) { return 0, 0, unix.EACCES }
	_, err := answering.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "example-store", VolumePath: "/kubelet/mount",
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeGetVolumeStats answered %v, want Internal", err)
	}
}

func TestNodeExpandVolumeIsNotServed(t *testing.T) {
	answering := testDriver(t)
	_, err := answering.NodeExpandVolume(t.Context(), &csi.NodeExpandVolumeRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("NodeExpandVolume answered %v, want Unimplemented", err)
	}
}
