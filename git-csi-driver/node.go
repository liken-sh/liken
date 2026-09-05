package main

// node.go holds the CSI Node service. Two calls answer, and the rest
// name the plan that fills them in.

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// node answers the Node service. Until plan 03 it knows only which
// node it runs on.
type node struct {
	csi.UnimplementedNodeServer
	nodeID string
}

// NodeGetInfo names the node and no topology. A checkout is made on
// whichever node publishes it, so no node is closer to a volume than
// another.
func (n *node) NodeGetInfo(
	context.Context, *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: n.nodeID}, nil
}

// NodeGetCapabilities declares what the kubelet may ask of this node.
// STAGE_UNSTAGE_VOLUME makes the kubelet stage a persistent volume
// once per node before it publishes it per pod. GET_VOLUME_STATS makes
// it poll NodeGetVolumeStats, and VOLUME_CONDITION makes it read the
// condition in that answer. The calls arrive with plan 03; declaring
// them now means the kubelet's behavior does not change when they do.
func (n *node) NodeGetCapabilities(
	context.Context, *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	declared := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_VOLUME_CONDITION,
	}
	capabilities := make([]*csi.NodeServiceCapability, 0, len(declared))
	for _, rpc := range declared {
		capabilities = append(capabilities, &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{Type: rpc},
			},
		})
	}
	return &csi.NodeGetCapabilitiesResponse{Capabilities: capabilities}, nil
}

func (n *node) NodeStageVolume(
	context.Context, *csi.NodeStageVolumeRequest,
) (*csi.NodeStageVolumeResponse, error) {
	return nil, unimplemented("NodeStageVolume", "plan 03")
}

func (n *node) NodeUnstageVolume(
	context.Context, *csi.NodeUnstageVolumeRequest,
) (*csi.NodeUnstageVolumeResponse, error) {
	return nil, unimplemented("NodeUnstageVolume", "plan 03")
}

func (n *node) NodePublishVolume(
	context.Context, *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	return nil, unimplemented("NodePublishVolume", "plan 03")
}

func (n *node) NodeUnpublishVolume(
	context.Context, *csi.NodeUnpublishVolumeRequest,
) (*csi.NodeUnpublishVolumeResponse, error) {
	return nil, unimplemented("NodeUnpublishVolume", "plan 03")
}

func (n *node) NodeGetVolumeStats(
	context.Context, *csi.NodeGetVolumeStatsRequest,
) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, unimplemented("NodeGetVolumeStats", "plan 03")
}

func (n *node) NodeExpandVolume(
	context.Context, *csi.NodeExpandVolumeRequest,
) (*csi.NodeExpandVolumeResponse, error) {
	return nil, unimplemented("NodeExpandVolume", "never; git volumes have no size")
}

// unimplemented answers a call the driver does not serve yet. The
// message names the plan that adds it, so a reader of the log learns
// when the call will work, not only that it does not.
func unimplemented(rpc, when string) error {
	return status.Errorf(codes.Unimplemented, "%s: %s", rpc, when)
}
