package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
)

// node implements the CSI Node service, which the kubelet calls to mount
// a volume into a pod and to remove that mount. It keeps the volumes
// that are published or staged on this node.
type node struct {
	csi.UnimplementedNodeServer
	nodeID string
	store  *store
	mounts mountSyscalls
	events *events
	arming *arming
	// demands watches PersistentVolumes for demandAnnotation, which asks
	// for a pull. A pull that the annotation starts waits until demandMin
	// after the last pass of the repository's fetch loop.
	demands   *demandWatch
	demandMin time.Duration
	metrics   *metrics
	logger    *slog.Logger
	base      context.Context
	// quiesce is how long a work tree must go without a write before the
	// driver scans it for changes to commit. sweep is how often the driver
	// scans each work tree when no inotify event arrives. A test replaces
	// clock to control both.
	quiesce time.Duration
	sweep   time.Duration
	clock   clock
	// sweepEvery is how often sweepStore deletes the bare repositories
	// and refs that no volume uses.
	sweepEvery time.Duration
	// mounted reports whether a path is still a mount point. After a
	// restart, the driver calls it for each target in its record files.
	// It reads the mount table at mountinfo, which a test points at its
	// own file.
	mounted   func(string) bool
	mountinfo string
	// inotify is a field so a test can fail it, as a kernel that refuses
	// an inotify instance does.
	inotify func(int) (int, error)

	mu        sync.Mutex
	volumes   map[string]*volume
	followers map[string]*follower
	// staged holds the volumes staged on this node. watchers holds the
	// inotify loop of each writeable volume, and armingCancels holds the cancel
	// function of the goroutine that follows each writeable volume's claim
	// and its VolumeAttributesClass.
	staged        map[string]*volume
	watchers      map[string]*watcher
	armingCancels map[string]context.CancelFunc
}

// newNode builds the service. base is the driver's context, so every
// fetch loop ends when the driver's pod stops.
func newNode(base context.Context, cfg *config, posting *events, registry *metrics, logger *slog.Logger) *node {
	n := &node{
		nodeID:        cfg.nodeID,
		store:         newStore(cfg.store),
		mounts:        kernelMounts{},
		events:        posting,
		metrics:       registry,
		logger:        logger,
		base:          base,
		quiesce:       defaultQuiesce,
		sweep:         defaultSweep,
		clock:         wallClock{},
		sweepEvery:    defaultSweepEvery,
		demandMin:     cfg.demandMin,
		mountinfo:     mountTable,
		inotify:       unix.InotifyInit1,
		volumes:       map[string]*volume{},
		followers:     map[string]*follower{},
		staged:        map[string]*volume{},
		watchers:      map[string]*watcher{},
		armingCancels: map[string]context.CancelFunc{},
	}
	n.arming = newArming(n, posting.client, logger)
	n.demands = newDemandWatch(n, posting.client, logger)
	n.mounted = func(path string) bool { return mountedNow(n.mountinfo, path) }
	return n
}

// NodeGetInfo returns the node ID and no topology. Each node makes its
// own checkout, so a volume does not depend on where it is scheduled.
func (n *node) NodeGetInfo(
	context.Context, *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: n.nodeID}, nil
}

// NodeGetCapabilities lists the optional calls the kubelet may make.
// STAGE_UNSTAGE_VOLUME makes the kubelet stage a persistent volume once
// per node before it publishes the volume to each pod. GET_VOLUME_STATS
// makes the kubelet call NodeGetVolumeStats for the volume's size.
//
// The kubelet sends SINGLE_NODE_SINGLE_WRITER for a ReadWriteOncePod
// volume only to a driver that lists SINGLE_NODE_MULTI_WRITER. To any
// other driver it sends SINGLE_NODE_WRITER, which stageKind refuses.
func (n *node) NodeGetCapabilities(
	context.Context, *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	declared := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}
	capabilities := make([]*csi.NodeServiceCapability, 0, len(declared))
	for _, rpc := range declared {
		capabilities = append(capabilities, &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{Type: rpc},
			},
		})
	}
	return &csi.NodeGetCapabilitiesResponse{Capabilities: capabilities}, nil
}

// NodePublishVolume bind mounts a tree at the pod's target path. For an
// inline volume, it checks out the ref here. For a staged volume, it
// mounts the tree that NodeStageVolume made, read-only for a read-only
// claim and read-write for a writeable volume.
//
// The CSIDriver sets requiresRepublish, so the kubelet repeats this call
// for every mounted volume on each pod sync. A repeated call for a
// published target mounts and fetches nothing. It stores the call's
// credential and returns success, and it logs only when the call fails,
// changes the credential, or has no publish Secret.
func (n *node) NodePublishVolume(
	ctx context.Context, request *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	id := request.GetVolumeId()
	target := request.GetTargetPath()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id is empty")
	case strings.ContainsRune(id, filepath.Separator):
		return nil, status.Error(codes.InvalidArgument, "volume_id: a volume id is one path element")
	case target == "":
		return nil, status.Error(codes.InvalidArgument, "target_path is empty")
	}

	parsed, err := parseAttributes(request)
	if err != nil {
		n.refused(podOf(request.GetVolumeContext()), err)
		return nil, err
	}
	supplied, err := parseCredentials(request.GetSecrets())
	if err != nil {
		n.refused(parsed.pod, err)
		return nil, err
	}
	if !parsed.ephemeral {
		if err := n.publishStaged(ctx, request, parsed, supplied); err != nil {
			n.refused(parsed.pod, err)
			return nil, err
		}
		return &csi.NodePublishVolumeResponse{}, nil
	}

	n.mu.Lock()
	published, found := n.volumes[id]
	n.mu.Unlock()
	if found {
		if published.target != target {
			return nil, status.Errorf(codes.FailedPrecondition,
				"volume_id: %s is published at %s", id, published.target)
		}
		n.republished(ctx, published, supplied)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	directory := n.store.volumeDir(id)
	mounting := &volume{
		id:          id,
		attributes:  parsed,
		credentials: supplied,
		directory:   directory,
		tree:        filepath.Join(directory, "tree"),
		target:      target,
		kind:        inlineVolume,
		context:     request.GetVolumeContext(),
		pod:         parsed.pod,
	}
	if err := n.publish(ctx, mounting); err != nil {
		n.refused(parsed.pod, err)
		_ = os.RemoveAll(directory)
		return nil, err
	}
	n.record(ctx, mounting)

	n.mu.Lock()
	n.volumes[id] = mounting
	n.joinFetchLoop(mounting)
	n.mu.Unlock()
	return &csi.NodePublishVolumeResponse{}, nil
}

// publish creates the volume's directory, fetches the ref into it, and
// bind mounts the tree at the target path.
func (n *node) publish(ctx context.Context, mounting *volume) error {
	if err := os.MkdirAll(mounting.directory, 0o700); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := n.stage(ctx, mounting); err != nil {
		return err
	}
	if err := os.MkdirAll(mounting.target, 0o755); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// A mount from before a driver restart can still be at the target,
	// so unmount it before the new bind mount.
	if err := unbind(n.mounts, mounting.target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := bindReadOnly(n.mounts, mounting.tree, mounting.target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return nil
}

// stage fetches the ref into the bare repository that every volume of
// the URL shares, then checks out the commit into the volume's own tree.
// When the fetch fails, the offline attribute decides the result.
// offlineRefuse fails the publish. offlineAllowStale publishes the commit
// that the store already has, and reports the failed fetch. Both fail
// when the store has no commit for the ref.
func (n *node) stage(ctx context.Context, mounting *volume) error {
	repo := n.store.repository(mounting.attributes.url)
	defer repo.lock()()

	first := !repo.exists()
	if first {
		if err := repo.create(ctx); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}
	// The depth applies only to the fetch that creates the repository. A
	// later volume of the same URL reuses the history that is already
	// there, whatever depth it asks for.
	depth := 0
	if first {
		depth = mounting.attributes.depth
	}

	env, remove, err := mounting.credential().use(mounting.directory)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	start := time.Now()
	fetchErr := n.metrics.timeFetch(repo.name, func() error {
		return repo.fetch(ctx, env, mounting.attributes.ref, depth)
	})
	remove()
	// A successful fetch serves every pull annotation stamped more than
	// demandSkew before the fetch started. A failed fetch serves none, so
	// the next annotation starts another fetch.
	if fetchErr == nil {
		mounting.satisfyDemandsBefore(start)
	}

	commit, resolveErr := repo.resolve(ctx, mounting.attributes.ref)
	switch {
	case fetchErr != nil && mounting.attributes.offline == offlineRefuse:
		return status.Error(codes.Unavailable, fetchErr.Error())
	case fetchErr != nil && resolveErr != nil:
		return status.Errorf(codes.Unavailable, "%s, and the node has no copy of %s",
			fetchErr, mounting.attributes.ref)
	case resolveErr != nil:
		return status.Error(codes.Internal, resolveErr.Error())
	}

	if err := repo.place(ctx, commit, mounting.directory, mounting.tree); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	mounting.reportCommit(commit)
	if fetchErr != nil {
		mounting.reportFetchFailed(fetchErr.Error())
		n.postVolumeEvent(mounting, corev1.EventTypeWarning, reasonStale,
			fmt.Sprintf("Published %s from the node's copy at %s, because the fetch failed: %s",
				mounting.attributes.ref, short(commit), fetchErr))
	}
	// Set the health gauge now, so a volume published after a failed
	// fetch shows as abnormal before the next fetch.
	n.recordHealth(ctx, mounting)
	return nil
}

// NodeUnpublishVolume removes one mount, and deletes an inline volume's
// directory. A read-only claim keeps its tree, its fetch loop, and its
// other targets for the other pods on this node until NodeUnstageVolume.
// A writeable volume keeps its work tree for the next pod on this node.
// The bare repository stays for the next volume of the same URL.
func (n *node) NodeUnpublishVolume(
	ctx context.Context, request *csi.NodeUnpublishVolumeRequest,
) (*csi.NodeUnpublishVolumeResponse, error) {
	id := request.GetVolumeId()
	target := request.GetTargetPath()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id is empty")
	case target == "":
		return nil, status.Error(codes.InvalidArgument, "target_path is empty")
	}

	n.mu.Lock()
	published, found := n.volumes[id]
	if found && published.kind == readOnlyClaim {
		if published.unbind(target) == 0 {
			delete(n.volumes, id)
		}
	}
	if found && published.kind != readOnlyClaim {
		delete(n.volumes, id)
		n.leaveFetchLoop(published)
		n.unwatch(published)
	}
	n.mu.Unlock()

	if err := unbind(n.mounts, target); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := os.RemoveAll(target); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if found && published.kind == readOnlyClaim {
		n.record(ctx, published)
	}
	if found && published.writeable() {
		// Delete the record file, so a restarted driver does not resume
		// this mount. The volume's metrics stay until NodeUnstageVolume.
		n.forget(published)
		// The pod is gone and nothing writes the tree, so commit and push
		// the remaining changes now instead of waiting for the quiesce.
		if rules := published.policyNow(); rules != nil {
			n.commit(ctx, published, rules)
		}
		n.push(ctx, published)
		n.recordHealth(ctx, published)
	}
	if found && published.kind == inlineVolume {
		if err := os.RemoveAll(published.directory); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		// An inline volume has no unstage, so delete its metrics here.
		n.metrics.forget(published)
	}
	n.logger.InfoContext(ctx, "Unpublished the volume", "volume", id)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats reports the size of the tree as bytes used. A git
// volume has no free space, so available is zero.
//
// The response has no volume condition, because CSI spec 1.13 removed
// the field. The volume's health is in the git_csi_volume_abnormal
// metric, in Events, and in the driver's log.
func (n *node) NodeGetVolumeStats(
	_ context.Context, request *csi.NodeGetVolumeStatsRequest,
) (*csi.NodeGetVolumeStatsResponse, error) {
	id := request.GetVolumeId()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id is empty")
	case request.GetVolumePath() == "":
		return nil, status.Error(codes.InvalidArgument, "volume_path is empty")
	}

	n.mu.Lock()
	published, found := n.volumes[id]
	n.mu.Unlock()
	if !found {
		return nil, status.Errorf(codes.NotFound, "volume_id: %s is not published on this node", id)
	}

	size, err := treeSize(published.tree)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{{
			Unit:      csi.VolumeUsage_BYTES,
			Used:      size,
			Available: 0,
			Total:     size,
		}},
	}, nil
}

func (n *node) NodeExpandVolume(
	context.Context, *csi.NodeExpandVolumeRequest,
) (*csi.NodeExpandVolumeResponse, error) {
	return nil, unimplemented("NodeExpandVolume", "a git volume has no size to expand")
}

// stagedVolume returns the volume staged on this node with the CSI
// volume handle, or nil. Every node watches the pull annotation of
// every PersistentVolume, and only the node that staged the volume
// fetches for it.
func (n *node) stagedVolume(handle string) *volume {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.staged[handle]
}

// volumesByRepo counts the volumes published on this node for each
// repository. The git_csi_volumes metric calls it at each scrape, so the
// metric keeps no count of its own that could differ from n.volumes.
func (n *node) volumesByRepo() map[string]float64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	counts := map[string]float64{}
	for _, vol := range n.volumes {
		counts[n.store.repository(vol.attributes.url).name]++
	}
	return counts
}

// refused posts a Warning Event on the pod, so kubectl describe shows
// why the pod stays in ContainerCreating.
func (n *node) refused(pod podReference, err error) {
	n.events.post(pod, corev1.EventTypeWarning, reasonRefused, status.Convert(err).Message())
}

// podOf reads the pod from the volume context without parsing the other
// attributes, so a request with invalid attributes still has a pod to
// post its Event on.
func podOf(context map[string]string) podReference {
	return podReference{
		name:      context[podNameKey],
		namespace: context[podNamespaceKey],
		uid:       context[podUIDKey],
	}
}

// short abbreviates a commit hash to seven characters.
func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// unimplemented returns the Unimplemented error for a call the driver
// does not serve, with a message that says why.
func unimplemented(rpc, why string) error {
	return status.Errorf(codes.Unimplemented, "%s: %s", rpc, why)
}
