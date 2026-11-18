package main

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
	d := testDriver(t)
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("publishing the volume: %v", err)
	}
	if got := testutil.ToFloat64(d.metrics.volumes); got != 1 {
		t.Errorf("per_node_csi_volumes is %v, want 1", got)
	}
}

func TestAnUnpublishLowersTheVolumesGauge(t *testing.T) {
	d := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("publishing the volume: %v", err)
	}
	if _, err := d.NodeUnpublishVolume(t.Context(),
		&csi.NodeUnpublishVolumeRequest{VolumeId: "example-store", TargetPath: target}); err != nil {
		t.Fatalf("unpublishing the volume: %v", err)
	}
	if got := testutil.ToFloat64(d.metrics.volumes); got != 0 {
		t.Errorf("per_node_csi_volumes is %v, want 0 once the pod has unpublished", got)
	}
}

func TestASecondVolumeOnTheSameNodeAddsToTheGauge(t *testing.T) {
	d := testDriver(t)
	for i, handle := range []string{"example-store", "second-store"} {
		target := filepath.Join(t.TempDir(), "mount")
		if _, err := d.NodePublishVolume(t.Context(),
			publishing(handle, target, aPod("reader", "pod-uid-1"))); err != nil {
			t.Fatalf("publishing volume %d: %v", i, err)
		}
	}
	if got := testutil.ToFloat64(d.metrics.volumes); got != 2 {
		t.Errorf("per_node_csi_volumes is %v, want 2", got)
	}
}

func TestAFailedMountDrawsOneMountFailureCount(t *testing.T) {
	d := testDriver(t)
	d.mounts.failAt = 1
	d.mounts.mountErr = unix.EPERM
	_, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1")))
	if status.Code(err) != codes.Internal {
		t.Fatalf("publishing the volume returned %v, want Internal", err)
	}
	if got := testutil.ToFloat64(d.metrics.mountFailures); got != 1 {
		t.Errorf("per_node_csi_mount_failures_total is %v, want 1", got)
	}
	if got := testutil.ToFloat64(d.metrics.volumes); got != 0 {
		t.Errorf("per_node_csi_volumes is %v, want 0 for a publish that failed", got)
	}
}

func TestRepeatedScrapesLeaveTheCountersUnchanged(t *testing.T) {
	d := testDriver(t)
	d.mounts.failAt = 1
	d.mounts.mountErr = unix.EPERM
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"), aPod("reader", "pod-uid-1"))); status.Code(err) != codes.Internal {
		t.Fatalf("publishing the volume returned %v, want Internal", err)
	}

	// A scrape must read the counter without changing it.
	for i := range 2 {
		if got := testutil.ToFloat64(d.metrics.mountFailures); got != 1 {
			t.Errorf("per_node_csi_mount_failures_total is %v on scrape %d, want 1", got, i+1)
		}
		scrape(t, d.metrics)
	}
}
