package main

// controller.go implements the CSI Controller service. It checks the
// parameters of a new VolumeAttributesClass and accepts DeleteVolume. It
// does not create or change a volume.

import (
	"context"
	"log/slog"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// controller keeps no state, because it checks only the parameters in
// each request. The webhook in the same process has the cluster client
// and the Secret cache.
type controller struct {
	csi.UnimplementedControllerServer
}

// clusterClient builds the webhook's client from the configuration that
// load returns. When there is no cluster, it logs a warning and returns
// nil, and the webhook then cannot read Secrets. The CSI Controller
// service does not use this client.
func clusterClient(logger *slog.Logger, load func() (*rest.Config, error)) kubernetes.Interface {
	config, err := load()
	if err != nil {
		logger.Warn("The webhook has no cluster client and cannot read Secrets", "error", err)
		return nil
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Warn("The webhook has no cluster client and cannot read Secrets", "error", err)
		return nil
	}
	return client
}

// controllerNode serves the Node service on the controller's socket,
// because the external-resizer sidecar calls NodeGetCapabilities there
// and exits when the call returns Unimplemented. It declares no
// capability.
type controllerNode struct {
	csi.UnimplementedNodeServer
}

func (controllerNode) NodeGetCapabilities(
	context.Context, *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{}, nil
}

func (controllerNode) NodeGetInfo(
	context.Context, *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return nil, unimplemented("NodeGetInfo", "the controller plugin mounts no volumes")
}

// ControllerGetCapabilities declares MODIFY_VOLUME, for a change of
// VolumeAttributesClass, and CREATE_DELETE_VOLUME, because csi-provisioner
// deletes a released PersistentVolume whose reclaim policy is Delete
// only for a driver that declares it. CreateVolume still refuses every
// call.
func (c *controller) ControllerGetCapabilities(
	context.Context, *csi.ControllerGetCapabilitiesRequest,
) (*csi.ControllerGetCapabilitiesResponse, error) {
	declared := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_MODIFY_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	}
	response := &csi.ControllerGetCapabilitiesResponse{}
	for _, capability := range declared {
		response.Capabilities = append(response.Capabilities, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{Type: capability},
			},
		})
	}
	return response, nil
}

// ControllerModifyVolume checks the new class's parameters and changes
// nothing, because each node plugin reads the class from the claim.
func (c *controller) ControllerModifyVolume(
	_ context.Context, request *csi.ControllerModifyVolumeRequest,
) (*csi.ControllerModifyVolumeResponse, error) {
	if _, err := parsePolicy(request.GetMutableParameters()); err != nil {
		return nil, err
	}
	return &csi.ControllerModifyVolumeResponse{}, nil
}

// CreateVolume refuses every call, because a person writes each
// PersistentVolume with the repository it mounts. csi-provisioner calls
// it only for a claim whose StorageClass uses this driver, and posts the
// error as an Event on that claim.
func (c *controller) CreateVolume(
	context.Context, *csi.CreateVolumeRequest,
) (*csi.CreateVolumeResponse, error) {
	return nil, unimplemented("CreateVolume",
		"a person writes each PersistentVolume of the driver, so the controller creates none")
}

// DeleteVolume succeeds and deletes nothing, so that csi-provisioner
// then deletes the PersistentVolume. Each node plugin removes its own
// work tree when its watch receives the PersistentVolume's deletion.
// The driver never deletes the remote repository.
func (c *controller) DeleteVolume(
	_ context.Context, request *csi.DeleteVolumeRequest,
) (*csi.DeleteVolumeResponse, error) {
	if request.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id is empty")
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (c *controller) ControllerPublishVolume(
	context.Context, *csi.ControllerPublishVolumeRequest,
) (*csi.ControllerPublishVolumeResponse, error) {
	return nil, unimplemented("ControllerPublishVolume", "the driver has no attach step")
}

func (c *controller) ControllerUnpublishVolume(
	context.Context, *csi.ControllerUnpublishVolumeRequest,
) (*csi.ControllerUnpublishVolumeResponse, error) {
	return nil, unimplemented("ControllerUnpublishVolume", "the driver has no attach step")
}

func (c *controller) ValidateVolumeCapabilities(
	context.Context, *csi.ValidateVolumeCapabilitiesRequest,
) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	return nil, unimplemented("ValidateVolumeCapabilities",
		"the node plugin refuses a mode it cannot serve when it stages the volume")
}

func (c *controller) ListVolumes(
	context.Context, *csi.ListVolumesRequest,
) (*csi.ListVolumesResponse, error) {
	return nil, unimplemented("ListVolumes", "the controller keeps no volume")
}

func (c *controller) GetCapacity(
	context.Context, *csi.GetCapacityRequest,
) (*csi.GetCapacityResponse, error) {
	return nil, unimplemented("GetCapacity", "a git volume has no capacity")
}

func (c *controller) CreateSnapshot(
	context.Context, *csi.CreateSnapshotRequest,
) (*csi.CreateSnapshotResponse, error) {
	return nil, unimplemented("CreateSnapshot", "git keeps the history in the repository, so the driver takes no snapshots")
}

func (c *controller) DeleteSnapshot(
	context.Context, *csi.DeleteSnapshotRequest,
) (*csi.DeleteSnapshotResponse, error) {
	return nil, unimplemented("DeleteSnapshot", "git keeps the history in the repository, so the driver takes no snapshots")
}

func (c *controller) ListSnapshots(
	context.Context, *csi.ListSnapshotsRequest,
) (*csi.ListSnapshotsResponse, error) {
	return nil, unimplemented("ListSnapshots", "git keeps the history in the repository, so the driver takes no snapshots")
}

func (c *controller) GetSnapshot(
	context.Context, *csi.GetSnapshotRequest,
) (*csi.GetSnapshotResponse, error) {
	return nil, unimplemented("GetSnapshot", "git keeps the history in the repository, so the driver takes no snapshots")
}

func (c *controller) ControllerExpandVolume(
	context.Context, *csi.ControllerExpandVolumeRequest,
) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, unimplemented("ControllerExpandVolume", "a git volume has no size")
}

func (c *controller) ControllerGetVolume(
	context.Context, *csi.ControllerGetVolumeRequest,
) (*csi.ControllerGetVolumeResponse, error) {
	return nil, unimplemented("ControllerGetVolume", "the controller keeps no volume")
}
