package main

// node.go holds the CSI Node service: the calls the kubelet makes to
// put a volume under a pod and take it away.

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

// node answers the Node service and holds what this node has published.
type node struct {
	csi.UnimplementedNodeServer
	nodeID string
	store  *store
	mounts mountSyscalls
	events *events
	arms   *arming
	// The gauges every volume reports itself on.
	readings *metrics
	logger   *slog.Logger
	base     context.Context
	// How long a tree rests before the driver reads what is pending, and
	// how often it reads that anyway.
	quiesce time.Duration
	sweep   time.Duration
	// mounted asks the kernel whether a path is still a mount. A restarted
	// driver asks it about every record.
	//
	// mountinfo is the file mounted reads. It is a field so a test can
	// name a file of its own.
	mounted   func(string) bool
	mountinfo string
	// inotify opens the file the watch reads. It is a seam, so a test can
	// drive a driver whose kernel refused one.
	inotify func(int) (int, error)

	mu        sync.Mutex
	volumes   map[string]*volume
	followers map[string]*follower
	// The writeable volumes this node has staged, and the loops that watch
	// and arm them.
	staged   map[string]*volume
	watchers map[string]*watcher
	armings  map[string]context.CancelFunc
}

// newNode builds the service. base is the driver's run, so every fetch
// loop ends when the pod stops.
func newNode(base context.Context, cfg *config, posting *events, readings *metrics, logger *slog.Logger) *node {
	answering := &node{
		nodeID:    cfg.nodeID,
		store:     newStore(cfg.store),
		mounts:    kernelMounts{},
		events:    posting,
		readings:  readings,
		logger:    logger,
		base:      base,
		quiesce:   defaultQuiesce,
		sweep:     defaultSweep,
		mountinfo: mountTable,
		inotify:   unix.InotifyInit1,
		volumes:   map[string]*volume{},
		followers: map[string]*follower{},
		staged:    map[string]*volume{},
		watchers:  map[string]*watcher{},
		armings:   map[string]context.CancelFunc{},
	}
	answering.arms = newArming(answering, posting.client, logger)
	answering.mounted = func(path string) bool { return mountedNow(answering.mountinfo, path) }
	return answering
}

// NodeGetInfo names the node and no topology. A checkout is made on
// whichever node publishes it, so no node is closer to a volume than
// another.
func (n *node) NodeGetInfo(
	context.Context, *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: n.nodeID}, nil
}

// NodeGetCapabilities declares what the kubelet may ask of this node.
// STAGE_UNSTAGE_VOLUME makes the kubelet stage a persistent volume
// once per node before it publishes it per pod. GET_VOLUME_STATS makes
// it poll NodeGetVolumeStats, and VOLUME_CONDITION makes it read the
// condition in that answer.
//
// SINGLE_NODE_MULTI_WRITER is declared because the kubelet sends
// the access modes ReadWriteOncePod uses only to a driver that declares
// it, and sends the legacy SINGLE_NODE_WRITER to every other driver.
func (n *node) NodeGetCapabilities(
	context.Context, *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	declared := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_VOLUME_CONDITION,
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

// NodePublishVolume binds a tree under the pod: a read-only volume's
// checkout of the ref, made here, or a writeable volume's work tree,
// made at stage. A repeated call for a published volume answers
// success, because the kubelet retries.
func (n *node) NodePublishVolume(
	ctx context.Context, request *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	id := request.GetVolumeId()
	target := request.GetTargetPath()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	case strings.ContainsRune(id, filepath.Separator):
		return nil, status.Error(codes.InvalidArgument, "volume_id: a volume id is one path element")
	case target == "":
		return nil, status.Error(codes.InvalidArgument, "target_path: the call names no path")
	}

	parsed, err := parseAttributes(request)
	if err != nil {
		n.refused(ctx, podOf(request.GetVolumeContext()), err)
		return nil, err
	}
	holder, err := parseCredentials(request.GetSecrets())
	if err != nil {
		n.refused(ctx, parsed.pod, err)
		return nil, err
	}
	if !parsed.ephemeral {
		if err := n.publishStaged(ctx, request, parsed, holder); err != nil {
			n.refused(ctx, parsed.pod, err)
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
		return &csi.NodePublishVolumeResponse{}, nil
	}

	directory := n.store.volumeDir(id)
	mounting := &volume{
		id:          id,
		attributes:  parsed,
		credentials: holder,
		directory:   directory,
		tree:        filepath.Join(directory, "tree"),
		target:      target,
		pod:         parsed.pod,
	}
	if err := n.publish(ctx, mounting); err != nil {
		n.refused(ctx, parsed.pod, err)
		_ = os.RemoveAll(directory)
		return nil, err
	}
	n.record(ctx, mounting, request.GetVolumeContext())

	n.mu.Lock()
	n.volumes[id] = mounting
	n.follow(mounting)
	n.mu.Unlock()
	return &csi.NodePublishVolumeResponse{}, nil
}

// publish makes the volume's directory, stages the ref into it, and
// binds the tree onto the target path.
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
	// A driver that restarted left its mounts behind, so the target
	// comes away before the new bind goes on.
	if err := unbind(n.mounts, mounting.target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := bindReadOnly(n.mounts, mounting.tree, mounting.target); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return nil
}

// stage fetches the ref into the shared bare repository and places it
// in the volume's own tree. offline decides what a failed fetch means:
// refuse fails the publish, and allowStale publishes what the store
// holds and reports the failure. A repository the store never fetched
// is refused under both, because there is nothing to publish.
func (n *node) stage(ctx context.Context, mounting *volume) error {
	repo := n.store.repository(mounting.attributes.url)
	defer repo.lock()()

	first := !repo.exists()
	if first {
		if err := repo.create(ctx); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}
	// depth applies to the first fetch of a repository. A later volume
	// with a depth of its own reuses what is there.
	depth := 0
	if first {
		depth = mounting.attributes.depth
	}

	env, remove, err := mounting.credentials.use(mounting.directory)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	fetchErr := repo.fetch(ctx, env, mounting.attributes.ref, depth)
	remove()

	commit, resolveErr := repo.resolve(ctx, mounting.attributes.ref)
	switch {
	case fetchErr != nil && mounting.attributes.offline == offlineRefuse:
		return status.Error(codes.Unavailable, fetchErr.Error())
	case fetchErr != nil && resolveErr != nil:
		return status.Errorf(codes.Unavailable, "%s, and the node holds no copy of %s",
			fetchErr, mounting.attributes.ref)
	case resolveErr != nil:
		return status.Error(codes.Internal, resolveErr.Error())
	}

	if err := repo.place(ctx, commit, mounting.directory, mounting.tree); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	mounting.reportCommit(commit)
	if fetchErr != nil {
		mounting.reportTrouble(fetchErr.Error())
		n.events.post(ctx, mounting.attributes.pod, corev1.EventTypeWarning, reasonStale,
			fmt.Sprintf("%s is published from the node's copy at %s: %s",
				mounting.attributes.ref, short(commit), fetchErr))
	}
	return nil
}

// NodeUnpublishVolume takes the mount away, and a read-only volume's
// directory with it. A writeable volume's work tree stays for the next
// pod on this node, and the bare repository stays for the next volume
// of the same URL.
func (n *node) NodeUnpublishVolume(
	ctx context.Context, request *csi.NodeUnpublishVolumeRequest,
) (*csi.NodeUnpublishVolumeResponse, error) {
	id := request.GetVolumeId()
	target := request.GetTargetPath()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	case target == "":
		return nil, status.Error(codes.InvalidArgument, "target_path: the call names no path")
	}

	n.mu.Lock()
	published, found := n.volumes[id]
	if found {
		delete(n.volumes, id)
		n.unfollow(published)
		n.unwatch(published)
	}
	n.mu.Unlock()

	if err := unbind(n.mounts, target); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := os.RemoveAll(target); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// A writeable volume gives up only its record of the mount. It stays
	// on the gauges while it is staged, because it is still this node's
	// volume.
	if found && published.writeable {
		n.forget(published)
		// The pod is gone and nothing writes the tree now, so the
		// last of its work is committed and pushed without waiting for a
		// timer.
		if rules := published.policyNow(); rules != nil {
			n.commit(ctx, published, rules)
		}
		n.push(ctx, published)
	}
	if found && !published.writeable {
		if err := os.RemoveAll(published.directory); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	n.logger.InfoContext(ctx, "unpublished", "volume", id)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats reports the tree's size as used. A git volume has
// no free space to report, so available is zero.
//
// The condition is what the volume reports, in its order: trouble, then
// unarmed work, then the commit.
func (n *node) NodeGetVolumeStats(
	_ context.Context, request *csi.NodeGetVolumeStatsRequest,
) (*csi.NodeGetVolumeStatsResponse, error) {
	id := request.GetVolumeId()
	switch {
	case id == "":
		return nil, status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	case request.GetVolumePath() == "":
		return nil, status.Error(codes.InvalidArgument, "volume_path: the call names no path")
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
	abnormal, message := published.report()
	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{{
			Unit:      csi.VolumeUsage_BYTES,
			Used:      size,
			Available: 0,
			Total:     size,
		}},
		VolumeCondition: &csi.VolumeCondition{Abnormal: abnormal, Message: message},
	}, nil
}

func (n *node) NodeExpandVolume(
	context.Context, *csi.NodeExpandVolumeRequest,
) (*csi.NodeExpandVolumeResponse, error) {
	return nil, unimplemented("NodeExpandVolume", "never; git volumes have no size")
}

// refused posts the refusal on the pod, so a person who describes the
// pod sees why it stays in ContainerCreating.
func (n *node) refused(ctx context.Context, pod podReference, err error) {
	n.events.post(ctx, pod, corev1.EventTypeWarning, reasonRefused, status.Convert(err).Message())
}

// podOf reads the pod straight from the volume context, so a refused
// parse still knows where its Event goes.
func podOf(context map[string]string) podReference {
	return podReference{
		name:      context[podNameKey],
		namespace: context[podNamespaceKey],
		uid:       context[podUIDKey],
	}
}

// short is the seven characters git itself prints for a commit.
func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// unimplemented answers a call the driver does not serve yet. The
// message names the plan that adds it, so a reader of the log learns
// when the call will work, not only that it does not.
func unimplemented(rpc, when string) error {
	return status.Errorf(codes.Unimplemented, "%s: %s", rpc, when)
}
