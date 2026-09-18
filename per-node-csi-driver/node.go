package main

// node.go implements the CSI Node service. The kubelet calls it to mount a
// volume in a pod and to remove that mount.

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
)

// node implements the Node service and records this node's published copies.
type node struct {
	csi.UnimplementedNodeServer
	nodeID   string
	store    *store
	mounts   mountSyscalls
	events   *events
	readings *metrics
	logger   *slog.Logger
	// mounted asks the kernel whether a path is still a mount. A restarted
	// driver asks it about every hold.
	//
	// mountinfo is the file mounted reads. It is a field so a test can
	// name a file of its own.
	mounted   func(string) bool
	mountinfo string
	// space reads the filesystem that NodeGetVolumeStats reports as
	// available and total. It is a field so a test can drive a driver
	// whose kernel refused the reading.
	space func(string) (int64, int64, error)

	mu    sync.Mutex
	holds map[string]hold
}

// newNode builds the Node service around the store the flags named,
// with the kernel's own mount calls and mount table.
func newNode(cfg *config, posting *events, readings *metrics, logger *slog.Logger) *node {
	answering := &node{
		nodeID:    cfg.nodeID,
		store:     newStore(cfg.store),
		mounts:    kernelMounts{},
		events:    posting,
		readings:  readings,
		logger:    logger,
		mountinfo: mountTable,
		space:     spaceOf,
		holds:     map[string]hold{},
	}
	answering.mounted = func(path string) bool { return mountedNow(answering.mountinfo, path) }
	return answering
}

// NodeGetInfo returns the node name and declares no topology. Every node
// keeps its own copy, so no node is closer to a volume than another.
func (n *node) NodeGetInfo(
	context.Context, *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: n.nodeID}, nil
}

// NodeGetCapabilities declares GET_VOLUME_STATS alone. A publish is a
// mkdir and a bind mount, so there is nothing to stage, and a copy has
// no size of its own to expand.
func (n *node) NodeGetCapabilities(
	context.Context, *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
				},
			},
		}},
	}, nil
}

// NodePublishVolume gives the pod this node's copy of the handle, and
// makes the directory on the first publish on this node. A second pod
// that asks for a handle another pod holds is refused. The pod that
// holds the handle can publish the same target repeatedly as the kubelet
// retries, and it can publish the copy at a new target.
func (n *node) NodePublishVolume(
	ctx context.Context, request *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	handle := request.GetVolumeId()
	target := request.GetTargetPath()
	pod := podOf(request.GetVolumeContext())

	if err := checkHandle(handle); err != nil {
		n.events.post(ctx, pod, corev1.EventTypeWarning, reasonRefused,
			status.Convert(err).Message())
		return nil, err
	}
	if target == "" {
		return nil, status.Error(codes.InvalidArgument, "target_path: the call names no path")
	}

	standing, held := n.holder(handle)
	if held && standing.PodUID != pod.uid {
		err := status.Errorf(codes.FailedPrecondition,
			"volume_id: %s is held on %s by pod %s/%s",
			handle, n.nodeID, standing.PodNamespace, standing.PodName)
		n.events.post(ctx, pod, corev1.EventTypeWarning, reasonHeld,
			status.Convert(err).Message())
		return nil, err
	}
	if held && standing.Target == target {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if err := n.mount(request, handle, target); err != nil {
		n.readings.mountFailed()
		n.events.post(ctx, pod, corev1.EventTypeWarning, reasonMountFailed,
			status.Convert(err).Message())
		return nil, err
	}
	n.keepHold(ctx, handle, hold{
		PodUID:       pod.uid,
		PodName:      pod.name,
		PodNamespace: pod.namespace,
		Target:       target,
	})
	n.logger.InfoContext(ctx, "published", "volume", handle, "target", target)
	return &csi.NodePublishVolumeResponse{}, nil
}

// mount makes the copy, makes the target, and binds the copy onto the
// target. Any mount at the target comes away first, because a driver
// that restarted left its own mount there, and a second bind would
// stack on top of it.
func (n *node) mount(
	request *csi.NodePublishVolumeRequest, handle, target string,
) error {
	if err := n.store.makeCopy(handle); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := unbind(n.mounts, target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := bind(n.mounts, n.store.copyPath(handle), target, request.GetReadonly()); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return nil
}

// NodeUnpublishVolume removes the mount and releases the hold. It
// removes nothing under copies/, because the copy outlives its pod.
func (n *node) NodeUnpublishVolume(
	ctx context.Context, request *csi.NodeUnpublishVolumeRequest,
) (*csi.NodeUnpublishVolumeResponse, error) {
	handle := request.GetVolumeId()
	target := request.GetTargetPath()
	switch {
	case handle == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	case target == "":
		return nil, status.Error(codes.InvalidArgument, "target_path: the call names no path")
	}

	if err := unbind(n.mounts, target); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// os.Remove, never os.RemoveAll. A detached mount leaves an empty
	// directory, so a target that still holds files is a mount the
	// kernel did not detach. A recursive delete there would walk into
	// the copy and destroy the node's data. A target that is already
	// gone is an unpublish the kubelet repeated.
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// The hold goes only when it names this target. When the kubelet
	// takes a refused pod down it unpublishes that pod's target too, and
	// that call must not give up the copy the holding pod still uses.
	if standing, held := n.holder(handle); held && standing.Target != target {
		n.logger.InfoContext(ctx, "unpublished a target the hold does not name",
			"volume", handle, "target", target, "held", standing.Target)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}
	n.dropHold(ctx, handle)
	n.readings.forget(handle)
	n.logger.InfoContext(ctx, "unpublished", "volume", handle)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats reports the copy's bytes as used, and the
// filesystem that holds the store as available and total. Every copy on
// the node shares that filesystem with every other volume there, so the
// available bytes are the node's and not the copy's.
func (n *node) NodeGetVolumeStats(
	_ context.Context, request *csi.NodeGetVolumeStatsRequest,
) (*csi.NodeGetVolumeStatsResponse, error) {
	handle := request.GetVolumeId()
	switch {
	case handle == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	case request.GetVolumePath() == "":
		return nil, status.Error(codes.InvalidArgument, "volume_path: the call names no path")
	}

	used, err := copySize(n.store.copyPath(handle))
	if err != nil {
		return nil, status.Errorf(codes.NotFound,
			"volume_id: %s has no copy on %s: %s", handle, n.nodeID, err)
	}
	available, total, err := n.space(n.store.root)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	n.readings.record(handle, used)
	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{{
			Unit:      csi.VolumeUsage_BYTES,
			Used:      used,
			Available: available,
			Total:     total,
		}},
	}, nil
}

// NodeExpandVolume is not served. A copy takes what the store's
// filesystem has, and the driver keeps no size limit to raise.
func (n *node) NodeExpandVolume(
	context.Context, *csi.NodeExpandVolumeRequest,
) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented,
		"NodeExpandVolume: a copy has no size of its own")
}
