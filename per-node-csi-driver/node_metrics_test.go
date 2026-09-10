package main

// node_metrics_test.go proves layer 3: a mount raises pernodecsi_volumes,
// a mount that fails draws one count on pernodecsi_mount_failures_total,
// and a scrape reads the registry without changing it.

import (
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAPublishRaisesTheVolumesGauge(t *testing.T) {
	answering := testDriver(t)
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if got := testutil.ToFloat64(answering.readings.volumes); got != 1 {
		t.Errorf("pernodecsi_volumes reads %v, want 1", got)
	}
}

func TestAnUnpublishLowersTheVolumesGauge(t *testing.T) {
	answering := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if _, err := answering.NodeUnpublishVolume(t.Context(),
		&csi.NodeUnpublishVolumeRequest{VolumeId: "example-store", TargetPath: target}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if got := testutil.ToFloat64(answering.readings.volumes); got != 0 {
		t.Errorf("pernodecsi_volumes reads %v, want 0 once the pod has unpublished", got)
	}
}

func TestASecondVolumeOnTheSameNodeAddsToTheGauge(t *testing.T) {
	answering := testDriver(t)
	for i, handle := range []string{"example-store", "second-store"} {
		target := filepath.Join(t.TempDir(), "mount")
		if _, err := answering.NodePublishVolume(t.Context(),
			publishing(handle, target, aPod("reader", "pod-uid-1"))); err != nil {
			t.Fatalf("NodePublishVolume %d: %v", i, err)
		}
	}
	if got := testutil.ToFloat64(answering.readings.volumes); got != 2 {
		t.Errorf("pernodecsi_volumes reads %v, want 2", got)
	}
}

func TestAFailedMountDrawsOneMountFailureCount(t *testing.T) {
	answering := testDriver(t)
	answering.mounts.failAt = 1
	answering.mounts.mountErr = unix.EPERM
	_, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodePublishVolume answered %v, want Internal", err)
	}
	if got := testutil.ToFloat64(answering.readings.mountFailures); got != 1 {
		t.Errorf("pernodecsi_mount_failures_total reads %v, want 1", got)
	}
	// The mount never took a hold, so nothing raises the volumes gauge
	// for a publish that failed.
	if got := testutil.ToFloat64(answering.readings.volumes); got != 0 {
		t.Errorf("pernodecsi_volumes reads %v, want 0 for a publish that failed", got)
	}
}

func TestRepeatedScrapesLeaveTheCountersUnchanged(t *testing.T) {
	answering := testDriver(t)
	answering.mounts.failAt = 1
	answering.mounts.mountErr = unix.EPERM
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1"))); status.Code(err) != codes.Internal {
		t.Fatalf("NodePublishVolume answered %v, want Internal", err)
	}

	// A scrape reads the registry, and reading is not itself an event
	// this driver counts. Two scrapes must find the same count.
	for i := range 2 {
		if got := testutil.ToFloat64(answering.readings.mountFailures); got != 1 {
			t.Errorf("pernodecsi_mount_failures_total reads %v on scrape %d, want 1", got, i+1)
		}
		scrape(t, answering.readings)
	}
}
