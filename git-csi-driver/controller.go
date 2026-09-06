package main

// controller.go holds the CSI Controller service, which
// validates a class and changes nothing.

import (
	"context"
	"log/slog"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// controller is the CSI Controller service. Validating a class reads
// nothing but the class, so this service holds no state. The webhook
// listener beside it holds the client and the Secret cache.
type controller struct {
	csi.UnimplementedControllerServer
}

// clusterClient reads the controller's own credentials from the pod it
// runs in. A controller that finds no cluster still validates a class
// and says once that it serves no webhook, because the sidecar does
// not need the webhook.
func clusterClient(logger *slog.Logger, load func() (*rest.Config, error)) kubernetes.Interface {
	config, err := load()
	if err != nil {
		logger.Warn("no webhook", "reason", err)
		return nil
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Warn("no webhook", "reason", err)
		return nil
	}
	return client
}

// controllerNode exists because the external-resizer sidecar calls
// NodeGetCapabilities on the controller's own socket to learn whether the plugin expands a volume
// on the node, and a socket that serves no Node service answers
// Unimplemented for the service itself, which the sidecar treats as a
// failure and exits on. This is the Node service the controller serves:
// no capability, so nothing expands, and no node of its own.
type controllerNode struct {
	csi.UnimplementedNodeServer
}

// NodeGetCapabilities declares nothing, the answer of a plugin that expands nothing
// and stages nothing on the node where the controller runs.
func (controllerNode) NodeGetCapabilities(
	context.Context, *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{}, nil
}

func (controllerNode) NodeGetInfo(
	context.Context, *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return nil, unimplemented("NodeGetInfo", "the controller plugin holds no node's volumes")
}

// ControllerGetCapabilities declares MODIFY_VOLUME alone, because the
// driver provisions nothing and attaches nothing.
func (c *controller) ControllerGetCapabilities(
	context.Context, *csi.ControllerGetCapabilitiesRequest,
) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: csi.ControllerServiceCapability_RPC_MODIFY_VOLUME,
				},
			},
		}},
	}, nil
}

// ControllerModifyVolume validates the class and changes nothing
// itself, because the node plugin reads the class the claim ends up
// with.
func (c *controller) ControllerModifyVolume(
	_ context.Context, request *csi.ControllerModifyVolumeRequest,
) (*csi.ControllerModifyVolumeResponse, error) {
	if _, err := parsePolicy(request.GetMutableParameters()); err != nil {
		return nil, err
	}
	return &csi.ControllerModifyVolumeResponse{}, nil
}

func (c *controller) CreateVolume(
	context.Context, *csi.CreateVolumeRequest,
) (*csi.CreateVolumeResponse, error) {
	return nil, unimplemented("CreateVolume", "never; a person makes the repository")
}

func (c *controller) DeleteVolume(
	context.Context, *csi.DeleteVolumeRequest,
) (*csi.DeleteVolumeResponse, error) {
	return nil, unimplemented("DeleteVolume", "never; the driver removes no repository")
}

func (c *controller) ControllerPublishVolume(
	context.Context, *csi.ControllerPublishVolumeRequest,
) (*csi.ControllerPublishVolumeResponse, error) {
	return nil, unimplemented("ControllerPublishVolume", "never; there is no attach step")
}

func (c *controller) ControllerUnpublishVolume(
	context.Context, *csi.ControllerUnpublishVolumeRequest,
) (*csi.ControllerUnpublishVolumeResponse, error) {
	return nil, unimplemented("ControllerUnpublishVolume", "never; there is no attach step")
}

func (c *controller) ValidateVolumeCapabilities(
	context.Context, *csi.ValidateVolumeCapabilitiesRequest,
) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	return nil, unimplemented("ValidateVolumeCapabilities",
		"never; the node plugin refuses a mode it cannot serve at stage")
}

func (c *controller) ListVolumes(
	context.Context, *csi.ListVolumesRequest,
) (*csi.ListVolumesResponse, error) {
	return nil, unimplemented("ListVolumes", "never; the controller holds no volume")
}

func (c *controller) GetCapacity(
	context.Context, *csi.GetCapacityRequest,
) (*csi.GetCapacityResponse, error) {
	return nil, unimplemented("GetCapacity", "never; a git volume has no capacity")
}

func (c *controller) CreateSnapshot(
	context.Context, *csi.CreateSnapshotRequest,
) (*csi.CreateSnapshotResponse, error) {
	return nil, unimplemented("CreateSnapshot", "never; the repository is the history")
}

func (c *controller) DeleteSnapshot(
	context.Context, *csi.DeleteSnapshotRequest,
) (*csi.DeleteSnapshotResponse, error) {
	return nil, unimplemented("DeleteSnapshot", "never; the repository is the history")
}

func (c *controller) ListSnapshots(
	context.Context, *csi.ListSnapshotsRequest,
) (*csi.ListSnapshotsResponse, error) {
	return nil, unimplemented("ListSnapshots", "never; the repository is the history")
}

func (c *controller) GetSnapshot(
	context.Context, *csi.GetSnapshotRequest,
) (*csi.GetSnapshotResponse, error) {
	return nil, unimplemented("GetSnapshot", "never; the repository is the history")
}

func (c *controller) ControllerExpandVolume(
	context.Context, *csi.ControllerExpandVolumeRequest,
) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, unimplemented("ControllerExpandVolume", "never; git volumes have no size")
}

func (c *controller) ControllerGetVolume(
	context.Context, *csi.ControllerGetVolumeRequest,
) (*csi.ControllerGetVolumeResponse, error) {
	return nil, unimplemented("ControllerGetVolume", "never; the controller holds no volume")
}
