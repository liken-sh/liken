package main

// stage.go holds the calls the kubelet makes for a volume it stages: the
// access mode that decides the kind, the stage that brings a writeable
// volume's work tree to the ref, and the publish that binds it under the
// pod. readonly.go holds what a read-only claim does with the same calls.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NodeStageVolume reads the access mode, then stages what it names: the
// tree of a read-only claim, or the work tree of a writeable volume, made
// on its first stage on this node, with the loop that reads the claim
// for its class.
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
	kind, err := stageKind(request.GetVolumeCapability())
	if err != nil {
		return nil, err
	}
	parsed, err := parseStageAttributes(kind, request.GetVolumeContext())
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
	if kind == readOnlyClaim {
		return n.stageReadOnly(ctx, request, parsed, holder)
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
		kind:        writeableVolume,
		context:     request.GetVolumeContext(),
	}
	if err := n.stageTree(ctx, arriving, repo); err != nil {
		return nil, err
	}
	// A stage reconciles the tree, finds a deleted ref, and reads
	// the work another tree left, so what the volume reports is taken
	// once here, after all of it.
	n.noteHealth(ctx, arriving)

	n.mu.Lock()
	n.staged[id] = arriving
	n.arm(arriving)
	n.mu.Unlock()
	return &csi.NodeStageVolumeResponse{}, nil
}

// stageKind reads the kind of volume the access mode asks for.
//
// The access mode decides the kind. ReadWriteOncePod stages a writeable
// volume. ReadOnlyMany, and the single-node read-only mode beside it,
// stage a read-only claim. Every other mode is refused, because the
// driver serves no other.
func stageKind(capability *csi.VolumeCapability) (volumeKind, error) {
	switch mode := capability.GetAccessMode().GetMode(); mode {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER:
		return writeableVolume, nil
	case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:
		return readOnlyClaim, nil
	default:
		// The message names the modes the driver serves, so a person
		// who reads it in the pod's events can fix the claim.
		return 0, status.Errorf(codes.InvalidArgument,
			"access_mode: %s is not SINGLE_NODE_SINGLE_WRITER, which is ReadWriteOncePod, "+
				"and not MULTI_NODE_READER_ONLY or SINGLE_NODE_READER_ONLY, which are ReadOnlyMany",
			mode)
	}
}

// stageTree fetches the ref into the shared bare repository and brings
// the work tree to it.
// A tree the node already holds is reconciled with what the fetch
// found, and a remote that holds the ref no longer leaves the tree as it
// is and stops every push until the ref exists again.
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
	side := n.fetchSide(ctx, env, repo, staging)
	remove()
	if fetchErr != nil {
		if staging.work.exists() && strings.Contains(fetchErr.Error(), missingRef) {
			return n.refDeleted(ctx, staging)
		}
		return status.Error(codes.Unavailable, fetchErr.Error())
	}
	commit, err := repo.resolve(ctx, staging.attributes.ref)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	n.noteAbandoned(staging)

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
	if err := n.reconcile(ctx, staging, head, commit, side); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// A reconcile that moved the tree leaves nothing wrong with it. One
	// that left the tree alone may have said why, and that stands.
	if moved := staging.work.refCommit(ctx, "HEAD"); moved != head {
		staging.reportCommit(moved)
	}
	return nil
}

// missingRef is how git states a ref the remote does not hold.
const missingRef = "couldn't find remote ref"

// fetchSide takes the side branch of a diverged volume, so the
// stage knows whether the remote still holds it. A remote that holds it
// no longer is a person who merged and deleted it, which is not a
// failure of the stage.
func (n *node) fetchSide(
	ctx context.Context, env []string, repo *repository, staging *volume,
) string {
	if !staging.work.exists() {
		return ""
	}
	branch := staging.work.divergedBranch(ctx)
	if branch == "" {
		return ""
	}
	if err := repo.fetch(ctx, env, branch, 0); err != nil {
		n.logger.InfoContext(ctx, "the remote holds no side branch",
			"volume", staging.id, "branch", branch, "reason", err)
		return ""
	}
	return branch
}

// refDeleted keeps the tree and reports the ref the remote no
// longer holds, because a push to a ref a person deleted would put it
// back.
func (n *node) refDeleted(ctx context.Context, staging *volume) error {
	head, err := staging.work.head(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	staging.reportRefDeleted(head)
	n.logger.WarnContext(ctx, "the remote holds no ref",
		"volume", staging.id, "ref", staging.attributes.ref)
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
	fetchErr := staging.work.fetchMetadata(ctx, env, staging.attributes.url, metadataRef)
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
	if found && staged.writeable() {
		n.push(ctx, staged)
		n.markUnstaged(ctx, staged)
		n.mu.Lock()
		n.disarm(staged)
		n.mu.Unlock()
	}
	if found && staged.kind == readOnlyClaim {
		n.unstageReadOnly(ctx, staged)
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
	if staged.kind == readOnlyClaim {
		if err := n.publishReadOnly(ctx, staged, request, parsed); err != nil {
			return n.refusedClaim(ctx, staged, err)
		}
		return nil
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
	staged.context = request.GetVolumeContext()
	n.record(ctx, staged)

	n.mu.Lock()
	n.volumes[id] = staged
	n.watch(staged)
	n.mu.Unlock()
	return nil
}
