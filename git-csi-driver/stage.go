package main

// stage.go holds the calls the kubelet makes for a writeable volume:
// the stage that brings the work tree to the ref, and the publish that
// binds it under the pod.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NodeStageVolume fetches the ref, makes the work tree on the volume's
// first stage on this node, and starts the loop that reads the claim.
func (n *node) NodeStageVolume(
	ctx context.Context, request *csi.NodeStageVolumeRequest,
) (*csi.NodeStageVolumeResponse, error) {
	id := request.GetVolumeId()
	staging := request.GetStagingTargetPath()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	case strings.ContainsRune(id, filepath.Separator):
		return nil, status.Error(codes.InvalidArgument, "volume_id: a volume id is one path element")
	case staging == "":
		return nil, status.Error(codes.InvalidArgument, "staging_target_path: the call names no path")
	}
	if err := checkAccessMode(request.GetVolumeCapability()); err != nil {
		return nil, err
	}
	parsed, err := parseStageAttributes(request.GetVolumeContext())
	if err != nil {
		return nil, err
	}
	holder, err := parseCredentials(request.GetSecrets())
	if err != nil {
		return nil, err
	}

	n.mu.Lock()
	staged, found := n.staged[id]
	n.mu.Unlock()
	if found {
		if staged.staging != staging {
			return nil, status.Errorf(codes.FailedPrecondition,
				"volume_id: %s is staged at %s", id, staged.staging)
		}
		return &csi.NodeStageVolumeResponse{}, nil
	}

	directory := n.store.volumeDir(id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	repo := n.store.repository(parsed.url)
	arriving := &volume{
		id:          id,
		attributes:  parsed,
		credentials: holder,
		directory:   directory,
		tree:        filepath.Join(directory, "tree"),
		work:        n.store.workTree(repo, id),
		staging:     staging,
		writeable:   true,
	}
	if err := n.stageTree(ctx, arriving, repo); err != nil {
		return nil, err
	}

	n.mu.Lock()
	n.staged[id] = arriving
	n.arm(arriving)
	n.mu.Unlock()
	return &csi.NodeStageVolumeResponse{}, nil
}

// checkAccessMode refuses every mode but the one ReadWriteOncePod asks
// for. The driver pushes what one writer wrote, and ReadWriteOnce
// allows two pods on one node.
func checkAccessMode(capability *csi.VolumeCapability) error {
	mode := capability.GetAccessMode().GetMode()
	if mode != csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER {
		return status.Errorf(codes.InvalidArgument,
			"access_mode: %s is not SINGLE_NODE_SINGLE_WRITER, which is ReadWriteOncePod", mode)
	}
	return nil
}

// stageTree fetches the ref into the shared bare repository and brings
// the work tree to it. A tree that already exists is left as the last
// pod left it. A ref that moved under it is reported in the condition;
// plan 06 reconciles it.
func (n *node) stageTree(ctx context.Context, staging *volume, repo *repository) error {
	defer repo.lock()()

	if !repo.exists() {
		if err := repo.create(ctx); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}
	env, remove, err := staging.credentials.use(staging.directory)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	fetchErr := repo.fetch(ctx, env, staging.attributes.ref, 0)
	remove()
	if fetchErr != nil {
		return status.Error(codes.Unavailable, fetchErr.Error())
	}
	commit, err := repo.resolve(ctx, staging.attributes.ref)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	if !staging.work.exists() {
		if err := staging.work.create(ctx, staging.attributes.ref, commit); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		// The mark starts at the commit the remote holds, so the
		// first commit the driver makes is the first thing unpushed.
		if err := staging.work.markPushed(ctx, commit); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		n.restore(ctx, staging)
		staging.reportCommit(commit)
		return nil
	}
	head, err := staging.work.head(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// A work tree from a driver that made no commits carries no
	// mark, and everything it holds is what the remote holds.
	if staging.work.refCommit(ctx, pushedRef) == "" {
		if err := staging.work.markPushed(ctx, head); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}
	staging.reportCommit(head)
	if head != commit {
		staging.reportTrouble(fmt.Sprintf("upstream moved: %s is at %s and the tree is at %s",
			staging.attributes.ref, short(commit), short(head)))
	}
	return nil
}

// restore replays what a checkout cannot carry. The metadata ref
// is fetched in a credential window of its own, and a restore that
// fails is reported and does not fail the stage.
func (n *node) restore(ctx context.Context, staging *volume) {
	env, remove, err := staging.credentials.use(staging.directory)
	if err != nil {
		n.logger.WarnContext(ctx, "the metadata was not fetched",
			"volume", staging.id, "error", err)
		return
	}
	fetchErr := staging.work.fetchMetadata(ctx, env, staging.attributes.url)
	remove()
	if fetchErr != nil {
		n.logger.InfoContext(ctx, "the remote holds no metadata",
			"volume", staging.id, "reason", fetchErr)
		return
	}
	if err := staging.work.replayMetadata(ctx, n.logger, os.Geteuid() == 0); err != nil {
		n.logger.WarnContext(ctx, "the metadata was not replayed",
			"volume", staging.id, "error", err)
	}
}

// NodeUnstageVolume stops the loops and keeps the work tree, because
// the next stage on this node starts from what the pod wrote.
func (n *node) NodeUnstageVolume(
	ctx context.Context, request *csi.NodeUnstageVolumeRequest,
) (*csi.NodeUnstageVolumeResponse, error) {
	id := request.GetVolumeId()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	case request.GetStagingTargetPath() == "":
		return nil, status.Error(codes.InvalidArgument, "staging_target_path: the call names no path")
	}

	n.mu.Lock()
	staged, found := n.staged[id]
	if found {
		delete(n.staged, id)
	}
	n.mu.Unlock()
	// Durability is the last push, so the volume leaves this node
	// only after what it holds has reached the remote.
	if found {
		n.push(ctx, staged)
		n.mu.Lock()
		n.disarm(staged)
		n.mu.Unlock()
	}
	n.logger.InfoContext(ctx, "unstaged", "volume", id)
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// publishStaged binds the work tree read-write under the pod. A volume
// the kubelet never staged is refused, because the tree it would bind
// does not exist.
func (n *node) publishStaged(
	ctx context.Context,
	request *csi.NodePublishVolumeRequest,
	parsed *attributes,
	holder *credentials,
) error {
	id, target := request.GetVolumeId(), request.GetTargetPath()
	n.mu.Lock()
	staged, found := n.staged[id]
	published, standing := n.volumes[id]
	n.mu.Unlock()
	if !found {
		return status.Errorf(codes.FailedPrecondition,
			"volume_id: %s is not staged on this node", id)
	}
	if standing {
		if published.target != target {
			return status.Errorf(codes.FailedPrecondition,
				"volume_id: %s is published at %s", id, published.target)
		}
		return nil
	}

	staged.setPod(parsed.pod)
	// A Secret named on the publish reaches the driver here and nowhere
	// else, so it replaces what the stage held.
	if holder != nil {
		staged.credentials = holder
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := unbind(n.mounts, target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := bindReadWrite(n.mounts, staged.tree, target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	staged.target = target
	n.record(ctx, staged, request.GetVolumeContext())

	n.mu.Lock()
	n.volumes[id] = staged
	n.watch(staged)
	n.mu.Unlock()
	return nil
}
