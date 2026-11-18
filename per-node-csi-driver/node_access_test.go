package main

import (
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
)

func TestNodeGetCapabilitiesDeclaresVolumeStatsAndSingleNodeMultiWriter(t *testing.T) {
	d := testDriver(t)
	response, err := d.NodeGetCapabilities(t.Context(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("getting the node capabilities: %v", err)
	}
	declared := []csi.NodeServiceCapability_RPC_Type{}
	for _, capability := range response.GetCapabilities() {
		declared = append(declared, capability.GetRpc().GetType())
	}
	want := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}
	if len(declared) != len(want) || declared[0] != want[0] || declared[1] != want[1] {
		t.Errorf("the node capabilities are %v, want %v", declared, want)
	}
}

// writing builds a NodePublishVolume request for a mounted filesystem
// with the given access mode.
func writing(target string, mode csi.VolumeCapability_AccessMode_Mode) *csi.NodePublishVolumeRequest {
	request := publishing("example-store", target, aPod("writer", "pod-uid-1"))
	request.VolumeCapability = &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: mode},
	}
	return request
}

// The kubelet sends MULTI_NODE_MULTI_WRITER for ReadWriteMany,
// SINGLE_NODE_SINGLE_WRITER for ReadWriteOncePod, and
// SINGLE_NODE_MULTI_WRITER for ReadWriteOnce. A kubelet that does not
// support SINGLE_NODE_MULTI_WRITER sends SINGLE_NODE_WRITER for both
// ReadWriteOnce and ReadWriteOncePod. Each mode must get the same
// read-write bind mount of this node's copy.
func TestPublishBindsTheCopyReadWriteForEveryWriteMode(t *testing.T) {
	for _, mode := range []csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			d := testDriver(t)
			target := filepath.Join(t.TempDir(), "mount")
			if _, err := d.NodePublishVolume(t.Context(), writing(target, mode)); err != nil {
				t.Fatalf("publishing the volume: %v", err)
			}
			want := mountCall{
				source: d.store.copyPath("example-store"),
				target: target,
				flags:  unix.MS_BIND,
			}
			if len(d.mounts.mounts) != 1 || d.mounts.mounts[0] != want {
				t.Errorf("the driver made the mounts %v, want only %v", d.mounts.mounts, want)
			}
		})
	}
}
