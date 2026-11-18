package main

// readonly.go implements the CSI node calls for a read-only claim. The
// stage checks out the ref once per node, and each publish bind-mounts
// that checkout into one pod.

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
)

// claimDeadline limits how long findClaim waits for the API server.
// The volume is mounted even when the lookup fails, so a slow API
// server must not delay the mount.
const claimDeadline = 15 * time.Second

// stageReadOnly checks out the ref into the volume's tree and, unless
// pull is never, adds the volume to its repository's fetch loop.
// Nothing writes to a read-only volume, so it has no git directory of
// its own, is not armed, and has no watch for pod changes.
func (n *node) stageReadOnly(
	ctx context.Context,
	request *csi.NodeStageVolumeRequest,
	parsed *attributes,
	supplied *credentials,
) (*csi.NodeStageVolumeResponse, error) {
	id := request.GetVolumeId()
	directory := n.store.volumeDir(id)
	arriving := &volume{
		id:          id,
		attributes:  parsed,
		credentials: supplied,
		directory:   directory,
		tree:        filepath.Join(directory, "tree"),
		staging:     request.GetStagingTargetPath(),
		kind:        readOnlyClaim,
		context:     request.GetVolumeContext(),
		targets:     map[string]podReference{},
	}
	// Look up the claim before the fetch, so that a failed fetch can post
	// its Event on the claim.
	n.findClaim(ctx, arriving)

	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, n.postRefusalOnClaim(ctx, arriving, status.Error(codes.Internal, err.Error()))
	}
	if err := n.stage(ctx, arriving); err != nil {
		_ = os.RemoveAll(directory)
		return nil, n.postRefusalOnClaim(ctx, arriving, err)
	}
	n.record(ctx, arriving)

	n.mu.Lock()
	n.staged[id] = arriving
	n.joinFetchLoop(arriving)
	n.mu.Unlock()
	n.demands.actOnSavedDemand(ctx, id)
	return &csi.NodeStageVolumeResponse{}, nil
}

// publishReadOnly bind-mounts the staged tree read-only at one more
// target. Many pods on a node share the staged tree, so the volume
// records every target. A publish at a target that is already bound
// only updates the credential from the request.
func (n *node) publishReadOnly(
	ctx context.Context,
	staged *volume,
	request *csi.NodePublishVolumeRequest,
	parsed *attributes,
	supplied *credentials,
) error {
	// The kubelet mounts the target into the container read-write unless
	// the pod sets readOnly: true, even over this driver's read-only
	// bind. Without it the pod could write into the tree that the fetch
	// loop updates.
	if !request.GetReadonly() {
		return status.Error(codes.InvalidArgument,
			"readOnly: a claim on this driver has to be mounted read-only; "+
				"set readOnly: true on the pod's persistentVolumeClaim volume")
	}
	target := request.GetTargetPath()
	if staged.boundAt(target) {
		n.republished(ctx, staged, supplied)
		return nil
	}
	if err := checkSecret(staged, supplied); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// A restarted driver finds its old mounts still in place.
	if err := unbind(n.mounts, target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := bindReadOnly(n.mounts, staged.tree, target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	staged.bind(target, parsed.pod)
	n.storeSecret(ctx, staged, supplied)
	n.record(ctx, staged)

	n.mu.Lock()
	n.volumes[staged.id] = staged
	n.mu.Unlock()
	return nil
}

// unstageReadOnly removes the volume from its fetch loop and deletes
// the volume's directory, which has no local changes to lose.
func (n *node) unstageReadOnly(ctx context.Context, staged *volume) {
	n.mu.Lock()
	n.leaveFetchLoop(staged)
	delete(n.volumes, staged.id)
	n.mu.Unlock()
	n.metrics.forget(staged)
	if err := os.RemoveAll(staged.directory); err != nil {
		n.logger.WarnContext(ctx, "The volume directory was not removed",
			"volume", staged.id, "error", err)
	}
}

// findClaim looks up the PersistentVolumeClaim bound to the volume
// handle and stores it on the volume, which labels the volume's metrics
// and posts the volume's Events on it. Without an API client, or when
// the lookup fails, the volume has no claim and its faults go only to
// the log.
func (n *node) findClaim(ctx context.Context, vol *volume) {
	if n.arming.client == nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, claimDeadline)
	defer cancel()
	claim, err := n.arming.claimOf(bounded, vol.id)
	if err != nil {
		n.logger.WarnContext(ctx, "The PersistentVolumeClaim of the volume was not found",
			"volume", vol.id, "error", err)
		return
	}
	vol.setClaim(claim)
}

// postRefusalOnClaim posts err as a warning Event on the claim and returns it.
// A stage call names no pod, so the claim's Events are where a person
// finds out why its pods stay in ContainerCreating.
func (n *node) postRefusalOnClaim(ctx context.Context, vol *volume, err error) error {
	n.events.postClaim(vol.claimNow(), corev1.EventTypeWarning,
		reasonRefused, status.Convert(err).Message())
	return err
}
