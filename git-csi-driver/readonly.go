package main

// readonly.go holds the calls the kubelet makes for a read-only claim:
// the stage that places the ref in the volume's own tree, and the
// publishes that bind that one tree under every pod on the node.

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

// claimDeadline bounds the one call a stage or a resume makes to find
// the claim a volume handle is bound to.
//
// The driver mounts whether or not the API server answers, so the lookup
// waits this long and no longer.
const claimDeadline = 15 * time.Second

// stageReadOnly places the ref in the volume's tree and adds the tree
// to the repository's fetch loop.
//
// A read-only claim makes no work tree, arms nothing, and watches
// nothing, because nothing writes it.
func (n *node) stageReadOnly(
	ctx context.Context,
	request *csi.NodeStageVolumeRequest,
	parsed *attributes,
	holder *credentials,
) (*csi.NodeStageVolumeResponse, error) {
	id := request.GetVolumeId()
	directory := n.store.volumeDir(id)
	arriving := &volume{
		id:          id,
		attributes:  parsed,
		credentials: holder,
		directory:   directory,
		tree:        filepath.Join(directory, "tree"),
		staging:     request.GetStagingTargetPath(),
		kind:        readOnlyClaim,
		context:     request.GetVolumeContext(),
		targets:     map[string]podReference{},
	}
	// The claim is found before the fetch, so a stage the remote refuses
	// reports on the claim. The claim is the only object a stage call
	// names.
	n.findClaim(ctx, arriving)

	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, n.refusedClaim(ctx, arriving, status.Error(codes.Internal, err.Error()))
	}
	if err := n.stage(ctx, arriving); err != nil {
		_ = os.RemoveAll(directory)
		return nil, n.refusedClaim(ctx, arriving, err)
	}
	n.record(ctx, arriving)

	n.mu.Lock()
	n.staged[id] = arriving
	n.follow(arriving)
	n.mu.Unlock()
	return &csi.NodeStageVolumeResponse{}, nil
}

// publishReadOnly binds the staged tree read-only at one more target.
//
// Many pods on one node publish one staged tree, so the volume keeps
// every target it bound. A publish at a target it already holds answers
// success.
func (n *node) publishReadOnly(
	ctx context.Context,
	staged *volume,
	request *csi.NodePublishVolumeRequest,
	parsed *attributes,
) error {
	target := request.GetTargetPath()
	if staged.boundAt(target) {
		return nil
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// A driver that restarted left its mounts behind, so the target
	// comes away before the new bind goes on.
	if err := unbind(n.mounts, target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := bindReadOnly(n.mounts, staged.tree, target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	staged.bind(target, parsed.pod)
	n.record(ctx, staged)

	n.mu.Lock()
	n.volumes[staged.id] = staged
	n.mu.Unlock()
	return nil
}

// unstageReadOnly stops the fetch loop and removes the tree.
//
// The kubelet sends the unstage after the last unpublish on the node. A
// read-only tree holds nothing a person has not pushed, so the volume's
// directory goes with it.
func (n *node) unstageReadOnly(ctx context.Context, staged *volume) {
	n.mu.Lock()
	n.unfollow(staged)
	delete(n.volumes, staged.id)
	n.mu.Unlock()
	n.readings.forget(staged)
	if err := os.RemoveAll(staged.directory); err != nil {
		n.logger.WarnContext(ctx, "the volume's directory stayed",
			"volume", staged.id, "error", err)
	}
}

// findClaim records the claim the volume handle is bound to, which
// labels the gauge and takes the volume's Events.
//
// A driver outside a cluster, or one the API server does not answer,
// still stages the volume and reports in its log alone.
func (n *node) findClaim(ctx context.Context, held *volume) {
	if n.arms.client == nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, claimDeadline)
	defer cancel()
	claim, err := n.arms.claimOf(bounded, held.id)
	if err != nil {
		n.logger.WarnContext(ctx, "the claim was not found",
			"volume", held.id, "error", err)
		return
	}
	held.setClaim(claim)
}

// refusedClaim posts the refusal on the claim and answers the error the
// call fails with.
//
// A stage call names no pod, so the claim is where a person reads why
// the pods that mount it stay in ContainerCreating.
func (n *node) refusedClaim(ctx context.Context, held *volume, err error) error {
	n.events.postClaim(ctx, held.claimNow(), corev1.EventTypeWarning,
		reasonRefused, status.Convert(err).Message())
	return err
}
