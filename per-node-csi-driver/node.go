package main

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type node struct {
	csi.UnimplementedNodeServer
	nodeID  string
	store   *store
	mounts  mountSyscalls
	events  *events
	metrics *metrics
	logger  *slog.Logger
	// mounted reports whether a path is a mount point in the mountinfo
	// file. The file is a field so a test can supply its own mount table.
	mounted   func(string) bool
	mountinfo string
	// space is a field so a test can make it fail.
	space func(string) (int64, int64, error)

	// records maps each handle to its mount records, keyed by target
	// path. The kubelet puts the pod's UID in the target path, so no two
	// pods share a target.
	mu      sync.Mutex
	records map[string]map[string]mountRecord
}

func newNode(cfg *config, events *events, metrics *metrics, logger *slog.Logger) *node {
	n := &node{
		nodeID:    cfg.nodeID,
		store:     newStore(cfg.store),
		mounts:    kernelMounts{},
		events:    events,
		metrics:   metrics,
		logger:    logger,
		mountinfo: mountTable,
		space:     spaceOf,
		records:   map[string]map[string]mountRecord{},
	}
	n.mounted = func(path string) bool { return mountedNow(n.mountinfo, path) }
	return n
}

// NodeGetInfo returns no topology, so the scheduler can put a pod that
// uses the volume on any node. Each node makes its own copy.
func (n *node) NodeGetInfo(
	context.Context, *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: n.nodeID}, nil
}

// NodeGetCapabilities lists no STAGE_UNSTAGE_VOLUME, because a publish
// is only a mkdir and a bind mount, and no EXPAND_VOLUME, because a copy
// has no size limit.
//
// SINGLE_NODE_MULTI_WRITER makes the kubelet send the CSI access mode
// that matches the claim: SINGLE_NODE_SINGLE_WRITER for
// ReadWriteOncePod and SINGLE_NODE_MULTI_WRITER for ReadWriteOnce.
// Without it, the kubelet sends SINGLE_NODE_WRITER for both.
// NodePublishVolume ignores the access mode, so every mode gets the same
// bind mount.
func (n *node) NodeGetCapabilities(
	context.Context, *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			rpcCapability(csi.NodeServiceCapability_RPC_GET_VOLUME_STATS),
			rpcCapability(csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER),
		},
	}, nil
}

func rpcCapability(kind csi.NodeServiceCapability_RPC_Type) *csi.NodeServiceCapability {
	return &csi.NodeServiceCapability{
		Type: &csi.NodeServiceCapability_Rpc{
			Rpc: &csi.NodeServiceCapability_RPC{Type: kind},
		},
	}
}

// NodePublishVolume bind-mounts this node's copy of the handle at the
// target path, and creates the copy when it does not exist. Every pod on
// the node that publishes the handle gets the same copy. The driver
// ignores the access mode and does not refuse a second pod, because the
// scheduler and the kubelet enforce the mode: ReadWriteOncePod allows
// one pod in the cluster, and ReadWriteOnce and ReadWriteMany allow any
// number of pods on a node. Pods that must not share a copy need required pod
// anti-affinity to put them on different nodes. A retried publish for a
// target that already has a mount record succeeds and mounts nothing.
func (n *node) NodePublishVolume(
	ctx context.Context, request *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	handle := request.GetVolumeId()
	target := request.GetTargetPath()
	pod := podOf(request.GetVolumeContext())

	if err := checkHandle(handle); err != nil {
		n.events.warn(pod, reasonRefused,
			"The volume handle cannot be a directory name in the store: "+status.Convert(err).Message())
		return nil, err
	}
	if target == "" {
		return nil, status.Error(codes.InvalidArgument, "target_path is empty")
	}

	if n.published(handle, target) {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if err := n.mount(request, handle, target); err != nil {
		n.metrics.mountFailed()
		n.events.warn(pod, reasonMountFailed,
			"The copy was not mounted into the pod: "+status.Convert(err).Message())
		return nil, err
	}
	n.saveRecord(ctx, handle, mountRecord{
		PodUID:       pod.uid,
		PodName:      pod.name,
		PodNamespace: pod.namespace,
		Target:       target,
	})
	n.logger.InfoContext(ctx, "Published the volume", "volume", handle, "target", target)
	return &csi.NodePublishVolumeResponse{}, nil
}

// mount unmounts the target before it binds the copy, because a mount
// from an earlier publish can be at the target with no record for it,
// such as after a restart that followed a failed write of the record
// file. A second bind would stack on top of the first.
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

// NodeUnpublishVolume unmounts the target and deletes its mount record. It
// keeps the copy, so a later pod on this node gets the same files. Only
// the sweep deletes a copy.
func (n *node) NodeUnpublishVolume(
	ctx context.Context, request *csi.NodeUnpublishVolumeRequest,
) (*csi.NodeUnpublishVolumeResponse, error) {
	handle := request.GetVolumeId()
	target := request.GetTargetPath()
	switch {
	case handle == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id is empty")
	case target == "":
		return nil, status.Error(codes.InvalidArgument, "target_path is empty")
	}

	if err := unbind(n.mounts, target); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// os.Remove, not os.RemoveAll. After the unmount the target is an
	// empty directory, so a target that still has files is still a
	// mount, and a recursive delete would delete the copy's files. A
	// target that is already gone means the kubelet repeated the call.
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Delete the copy's size series only after its last record, because
	// the kubelet keeps calling NodeGetVolumeStats for each pod that
	// still mounts the copy.
	if n.deleteRecord(ctx, handle, target) {
		n.metrics.forget(handle)
	}
	n.logger.InfoContext(ctx, "Unpublished the volume", "volume", handle, "target", target)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats returns the size of the copy's regular files as
// used bytes. Available and total are those of the store's filesystem,
// which every copy on the node shares, so they describe the node and not
// the copy.
func (n *node) NodeGetVolumeStats(
	_ context.Context, request *csi.NodeGetVolumeStatsRequest,
) (*csi.NodeGetVolumeStatsResponse, error) {
	handle := request.GetVolumeId()
	switch {
	case handle == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id is empty")
	case request.GetVolumePath() == "":
		return nil, status.Error(codes.InvalidArgument, "volume_path is empty")
	}

	used, err := copySize(n.store.copyPath(handle))
	if err != nil {
		return nil, status.Errorf(codes.NotFound,
			"volume_id: node %s has no copy of %s: %s", n.nodeID, handle, err)
	}
	available, total, err := n.space(n.store.root)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	n.metrics.record(handle, used)
	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{{
			Unit:      csi.VolumeUsage_BYTES,
			Used:      used,
			Available: available,
			Total:     total,
		}},
	}, nil
}

// NodeExpandVolume returns Unimplemented, because the driver sets no
// size limit on a copy.
func (n *node) NodeExpandVolume(
	context.Context, *csi.NodeExpandVolumeRequest,
) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented,
		"NodeExpandVolume: the driver sets no size limit on a copy")
}
