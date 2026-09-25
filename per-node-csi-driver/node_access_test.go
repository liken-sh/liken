package main

import (
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
)

func TestNodeGetCapabilitiesDeclaresVolumeStatsAndSingleNodeMultiWriter(t *testing.T) {
	answering := testDriver(t)
	answer, err := answering.NodeGetCapabilities(t.Context(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("NodeGetCapabilities: %v", err)
	}
	declared := []csi.NodeServiceCapability_RPC_Type{}
	for _, capability := range answer.GetCapabilities() {
		declared = append(declared, capability.GetRpc().GetType())
	}
	want := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}
	if len(declared) != len(want) || declared[0] != want[0] || declared[1] != want[1] {
		t.Errorf("NodeGetCapabilities declared %v, want %v", declared, want)
	}
}

// writing builds a NodePublishVolume request that names a mounted
// filesystem with the access mode, as the kubelet sends it.
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
// SINGLE_NODE_MULTI_WRITER for ReadWriteOnce. An older kubelet sends
// SINGLE_NODE_WRITER for both of the last two. Each mode gets the same
// read-write bind of this node's copy.
func TestPublishBindsTheCopyReadWriteForEveryWriteMode(t *testing.T) {
	for _, mode := range []csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			answering := testDriver(t)
			target := filepath.Join(t.TempDir(), "mount")
			if _, err := answering.NodePublishVolume(t.Context(), writing(target, mode)); err != nil {
				t.Fatalf("NodePublishVolume: %v", err)
			}
			want := mountCall{
				source: answering.store.copyPath("example-store"),
				target: target,
				flags:  unix.MS_BIND,
			}
			if len(answering.mounts.mounts) != 1 || answering.mounts.mounts[0] != want {
				t.Errorf("the driver made %v, want %v alone", answering.mounts.mounts, want)
			}
		})
	}
}
