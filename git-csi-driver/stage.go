package main

// stage.go implements NodeStageVolume, NodeUnstageVolume, and the
// publish of a staged writeable volume. readonly.go handles the same
// calls for a read-only claim.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NodeStageVolume stages a read-only claim or a writeable volume,
// depending on the access mode. For a writeable volume it brings the
// work tree to the ref, creating the tree on the first stage on this
// node, and starts the goroutine that watches the claim's class.
// A repeated call with the same staging path succeeds and does nothing.
func (n *node) NodeStageVolume(
	ctx context.Context, request *csi.NodeStageVolumeRequest,
) (*csi.NodeStageVolumeResponse, error) {
	id := request.GetVolumeId()
	staging := request.GetStagingTargetPath()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id is empty")
	case strings.ContainsRune(id, filepath.Separator):
		return nil, status.Error(codes.InvalidArgument, "volume_id: a volume id is one path element")
	case staging == "":
		return nil, status.Error(codes.InvalidArgument, "staging_target_path is empty")
	}
	kind, err := stageKind(request.GetVolumeCapability())
	if err != nil {
		return nil, err
	}
	parsed, err := parseStageAttributes(kind, request.GetVolumeContext())
	if err != nil {
		return nil, err
	}
	supplied, err := parseCredentials(request.GetSecrets())
	if err != nil {
		return nil, err
	}

	n.mu.Lock()
	existing, found := n.staged[id]
	n.mu.Unlock()
	if found {
		if existing.staging != staging {
			return nil, status.Errorf(codes.FailedPrecondition,
				"volume_id: %s is staged at %s", id, existing.staging)
		}
		return &csi.NodeStageVolumeResponse{}, nil
	}
	if kind == readOnlyClaim {
		return n.stageReadOnly(ctx, request, parsed, supplied)
	}

	directory := n.store.volumeDir(id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	repo := n.store.repository(parsed.url)
	staged := &volume{
		id:          id,
		attributes:  parsed,
		credentials: supplied,
		directory:   directory,
		tree:        filepath.Join(directory, "tree"),
		work:        n.store.workTree(repo, id),
		staging:     staging,
		kind:        writeableVolume,
		context:     request.GetVolumeContext(),
	}
	if err := n.stageTree(ctx, staged, repo); err != nil {
		return nil, err
	}
	// stageTree can record a deleted ref or a fault, so the health is
	// read after it returns.
	n.recordHealth(ctx, staged)
	n.recordReclaimPolicyOf(ctx, id)

	n.mu.Lock()
	n.staged[id] = staged
	n.arm(staged)
	n.mu.Unlock()
	n.demands.actOnSavedDemand(ctx, id)
	return &csi.NodeStageVolumeResponse{}, nil
}

func stageKind(capability *csi.VolumeCapability) (volumeKind, error) {
	switch mode := capability.GetAccessMode().GetMode(); mode {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER:
		return writeableVolume, nil
	case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:
		return readOnlyClaim, nil
	default:
		// The message gives the Kubernetes access modes too, because a
		// person reads it in the pod's events and fixes the claim.
		return 0, status.Errorf(codes.InvalidArgument,
			"access_mode: %s is not SINGLE_NODE_SINGLE_WRITER, which is ReadWriteOncePod, "+
				"not MULTI_NODE_READER_ONLY, which is ReadOnlyMany, and not SINGLE_NODE_READER_ONLY",
			mode)
	}
}

// stageTree fetches the ref into the shared bare repository and brings
// the work tree to it. A work tree that already exists on the node is
// reconciled with the fetched commit. When the remote has no ref and
// the work tree exists, the tree stays as it is and the driver pushes
// nothing until the ref exists again. With no work tree, a missing ref
// fails the stage.
func (n *node) stageTree(ctx context.Context, staging *volume, repo *repository) error {
	defer repo.lock()()

	if !repo.exists() {
		if err := repo.create(ctx); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}
	env, remove, err := staging.credential().use(staging.directory)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	start := time.Now()
	fetchErr := n.metrics.timeFetch(repo.name, func() error {
		return repo.fetch(ctx, env, staging.attributes.ref, 0)
	})
	side := n.fetchSide(ctx, env, repo, staging)
	remove()
	if fetchErr != nil {
		if staging.work.exists() && strings.Contains(fetchErr.Error(), missingRef) {
			return n.refDeleted(ctx, staging)
		}
		return status.Error(codes.Unavailable, fetchErr.Error())
	}
	staging.satisfyDemandsBefore(start)
	commit, err := repo.resolve(ctx, staging.attributes.ref)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	if !staging.work.exists() {
		if err := staging.work.create(ctx, staging.attributes.ref, commit); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		// pushedRef starts at the commit the remote has, so the first
		// commit the driver makes is the first unpushed one.
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
	// A work tree with no pushedRef has no commit from the driver, so
	// head is a commit the remote has.
	if staging.work.refCommit(ctx, pushedRef) == "" {
		if err := staging.work.markPushed(ctx, head); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}
	staging.reportCommit(head)
	if err := n.reconcile(ctx, staging, head, commit, side); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// Record the commit only when reconcile moved the tree, because
	// reportCommit clears the faults. A reconcile that left the tree
	// alone can have recorded a fault that must stay.
	if moved := staging.work.refCommit(ctx, "HEAD"); moved != head {
		staging.reportCommit(moved)
	}
	return nil
}

// missingRef is the text in git's fetch error when the remote has no
// such ref.
const missingRef = "couldn't find remote ref"

// fetchSide fetches the side branch of a diverged volume and returns its
// name, or an empty string when the volume has none or the fetch fails.
// A missing branch usually means a person merged and deleted it, so a
// failed fetch does not fail the stage.
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
	err := n.metrics.timeFetch(repo.name, func() error {
		return repo.fetch(ctx, env, branch, 0)
	})
	if err != nil {
		n.logger.InfoContext(ctx, "The side branch was not fetched from the remote",
			"volume", staging.id, "branch", branch, "error", err)
		return ""
	}
	return branch
}

// refDeleted leaves the tree at its current commit and records that the
// remote has no ref, which stops pushes.
func (n *node) refDeleted(ctx context.Context, staging *volume) error {
	head, err := staging.work.head(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	staging.reportRefDeleted(head)
	n.logger.WarnContext(ctx, "The remote has no ref for the volume",
		"volume", staging.id, "ref", staging.attributes.ref)
	return nil
}

// restore fetches metadataRef from the remote and applies the file
// modes, owners, and empty directories that git does not store. It
// changes owners only when the driver runs as root. A failed restore is
// logged and does not fail the stage.
func (n *node) restore(ctx context.Context, staging *volume) {
	env, remove, err := staging.credential().use(staging.directory)
	if err != nil {
		n.logger.WarnContext(ctx, "The metadata of the volume was not restored because its credential was not written",
			"volume", staging.id, "error", err)
		return
	}
	fetchErr := staging.work.fetchMetadata(ctx, env, staging.attributes.url, metadataRef)
	remove()
	if fetchErr != nil {
		n.logger.InfoContext(ctx, "The remote has no metadata ref for the volume",
			"volume", staging.id, "error", fetchErr)
		return
	}
	if err := staging.work.replayMetadata(ctx, n.logger, os.Geteuid() == 0); err != nil {
		n.logger.WarnContext(ctx, "The metadata of the volume was not replayed",
			"volume", staging.id, "error", err)
	}
}

// NodeUnstageVolume pushes a writeable volume, stops its loops, and
// keeps the work tree, because the next stage on this node starts from
// what the pod wrote. reclaim deletes the tree when the PersistentVolume
// is gone.
func (n *node) NodeUnstageVolume(
	ctx context.Context, request *csi.NodeUnstageVolumeRequest,
) (*csi.NodeUnstageVolumeResponse, error) {
	id := request.GetVolumeId()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id is empty")
	case request.GetStagingTargetPath() == "":
		return nil, status.Error(codes.InvalidArgument, "staging_target_path is empty")
	}

	n.mu.Lock()
	staged, found := n.staged[id]
	if found {
		delete(n.staged, id)
	}
	n.mu.Unlock()
	if found && staged.writeable() {
		n.push(ctx, staged)
		n.mu.Lock()
		n.disarm(staged)
		n.mu.Unlock()
	}
	if found && staged.kind == readOnlyClaim {
		n.unstageReadOnly(ctx, staged)
	}
	// reclaim skips a staged volume, so a PersistentVolume with the
	// Delete reclaim policy that was deleted during the stage is
	// reclaimed here.
	n.reclaim(ctx, id)
	n.logger.InfoContext(ctx, "Unstaged the volume", "volume", id)
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// publishStaged bind-mounts a writeable volume's work tree read-write at
// the target path, and hands a read-only claim to publishReadOnly. It
// refuses a volume that is not staged on this node.
func (n *node) publishStaged(
	ctx context.Context,
	request *csi.NodePublishVolumeRequest,
	parsed *attributes,
	supplied *credentials,
) error {
	id, target := request.GetVolumeId(), request.GetTargetPath()
	n.mu.Lock()
	staged, found := n.staged[id]
	published, alreadyPublished := n.volumes[id]
	n.mu.Unlock()
	if !found {
		return status.Errorf(codes.FailedPrecondition,
			"volume_id: %s is not staged on this node", id)
	}
	if staged.kind == readOnlyClaim {
		if err := n.publishReadOnly(ctx, staged, request, parsed, supplied); err != nil {
			return n.postRefusalOnClaim(ctx, staged, err)
		}
		return nil
	}
	if alreadyPublished {
		if published.target != target {
			return status.Errorf(codes.FailedPrecondition,
				"volume_id: %s is published at %s", id, published.target)
		}
		n.republished(ctx, published, supplied)
		return nil
	}

	if err := checkSecret(staged, supplied); err != nil {
		return err
	}
	staged.setPod(parsed.pod)
	n.storeSecret(ctx, staged, supplied)
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
