package main

// controller.go implements the CSI Controller service, which validates a
// class and answers the delete of a volume. It changes no volume itself.

import (
	"context"
	"log/slog"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
// NodeGetCapabilities on the controller's socket to check whether the
// plugin expands volumes. A socket without a Node service returns
// Unimplemented, which the sidecar treats as a failure and exits on.
// This Node service declares no capability, so nothing expands and the
// controller claims no node of its own.
type controllerNode struct {
	csi.UnimplementedNodeServer
}

// NodeGetCapabilities declares no capability. The controller expands no
// volume and stages nothing on the node where it runs.
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

// ControllerGetCapabilities declares MODIFY_VOLUME, for a class
// change, and CREATE_DELETE_VOLUME, because csi-provisioner deletes a
// released PersistentVolume of the Delete reclaim policy only for a
// driver that declares it. The driver attaches nothing, and CreateVolume
// refuses every call.
func (c *controller) ControllerGetCapabilities(
	context.Context, *csi.ControllerGetCapabilitiesRequest,
) (*csi.ControllerGetCapabilitiesResponse, error) {
	declared := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_MODIFY_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	}
	answer := &csi.ControllerGetCapabilitiesResponse{}
	for _, one := range declared {
		answer.Capabilities = append(answer.Capabilities, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{Type: one},
			},
		})
	}
	return answer, nil
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

// CreateVolume refuses, because a person writes each PersistentVolume
// with the repository it mounts. csi-provisioner calls it only for a
// claim whose StorageClass names this driver, and posts the refusal on
// that claim.
func (c *controller) CreateVolume(
	context.Context, *csi.CreateVolumeRequest,
) (*csi.CreateVolumeResponse, error) {
	return nil, unimplemented("CreateVolume",
		"never; a person writes each PersistentVolume of the driver")
}

// DeleteVolume answers success for every volume, and removes nothing
// here. The data of a git volume is the work tree on each node that
// staged it, and each node plugin removes its own when it reads that
// the PersistentVolume is deleted. The remote repository is never the
// volume's to delete. csi-provisioner deletes the PersistentVolume once
// this call answers.
func (c *controller) DeleteVolume(
	_ context.Context, request *csi.DeleteVolumeRequest,
) (*csi.DeleteVolumeResponse, error) {
	if request.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	}
	return &csi.DeleteVolumeResponse{}, nil
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
