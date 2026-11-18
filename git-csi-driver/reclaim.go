package main

// reclaim.go applies a writeable volume's reclaim policy to the work
// tree on each node when its PersistentVolume is deleted. Delete
// removes the tree and Retain keeps it. The remote repository is never
// changed.
//
// csi-provisioner deletes a released PersistentVolume with the Delete
// policy after DeleteVolume returns. Each node plugin watches
// PersistentVolumes and removes its own tree when the watch reports the
// delete. A node plugin that was down at the delete removes the tree
// when it starts, because no PersistentVolume has that handle any more.
// A deleted PersistentVolume cannot be read, so the node copies the
// policy into a file in the volume's directory at each stage and at
// each update the watch reports.
//
// A Retain tree stays until a person removes it or a new
// PersistentVolume with the same handle stages it. The next push of
// that volume sends any commit that the last unstage could not push.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// policyFileName is the file in a volume's directory that holds the
// reclaim policy of the volume's PersistentVolume.
const policyFileName = "reclaim-policy"

func (s *store) policyFile(id string) string {
	return filepath.Join(s.volumeDir(id), policyFileName)
}

// recordedPolicy returns the policy in the volume's policy file, or an
// empty policy when there is no file. reclaim removes a tree only for
// Delete, so a tree with no policy file is kept.
func (s *store) recordedPolicy(id string) corev1.PersistentVolumeReclaimPolicy {
	content, err := os.ReadFile(s.policyFile(id))
	if err != nil {
		return ""
	}
	return corev1.PersistentVolumeReclaimPolicy(trimLine(string(content)))
}

// isDirectoryName reports whether a volume handle is a single path
// element, so that it cannot point outside the store's volumes
// directory. The watch reads handles from every PersistentVolume of
// this driver, including ones a person wrote by hand.
func isDirectoryName(handle string) bool {
	return handle != "" && handle != "." && handle != ".." &&
		!strings.ContainsRune(handle, filepath.Separator)
}

// recordReclaimPolicy writes the reclaim policy of a PersistentVolume of this
// driver to the policy file, when this node has a work tree for its
// handle. It skips the write when the file already has that policy,
// because the watch also reports each status update.
func (n *node) recordReclaimPolicy(ctx context.Context, pv *corev1.PersistentVolume) {
	source := pv.Spec.CSI
	if source == nil || source.Driver != driverName || !isDirectoryName(source.VolumeHandle) {
		return
	}
	id := source.VolumeHandle
	if !n.store.tree(id).exists() {
		return
	}
	policy := pv.Spec.PersistentVolumeReclaimPolicy
	if n.store.recordedPolicy(id) == policy {
		return
	}
	if err := os.WriteFile(n.store.policyFile(id), []byte(string(policy)+"\n"), 0o600); err != nil {
		n.logger.WarnContext(ctx, "The reclaim policy file was not written",
			"volume", id, "policy", policy, "error", err)
	}
}

// recordReclaimPolicyOf runs recordReclaimPolicy on the watch's cached PersistentVolume
// for the handle.
func (n *node) recordReclaimPolicyOf(ctx context.Context, id string) {
	if pv, found := n.demands.cachedVolume(id); found {
		n.recordReclaimPolicy(ctx, pv)
	}
}

// reclaim removes the work tree of a volume when this node has not
// staged or published it, no PersistentVolume has its handle, and the
// recorded policy is Delete. Unpushed commits are lost with the tree.
// The log line reports whether there were any, and whether the volume
// had moved to a side branch, whose commits are on the remote but not
// on the followed ref.
func (n *node) reclaim(ctx context.Context, id string) {
	if !isDirectoryName(id) || n.usesVolume(id) || n.demands.hasVolume(id) {
		return
	}
	if n.store.recordedPolicy(id) != corev1.PersistentVolumeReclaimDelete {
		return
	}
	work := n.store.tree(id)
	unpushed := work.refCommit(ctx, "HEAD") != work.refCommit(ctx, pushedRef)
	diverged := work.divergedBranch(ctx)
	if err := os.RemoveAll(work.directory); err != nil {
		n.logger.WarnContext(ctx, "The work tree was not removed", "volume", id, "error", err)
		return
	}
	n.logger.InfoContext(ctx, "Removed the work tree", "volume", id,
		"reason", "its PersistentVolume was deleted with the Delete reclaim policy",
		"unpushed", unpushed, "diverged", diverged)
	// The tree may have been the last user of its bare repository, so
	// sweep now instead of at the next periodic sweep.
	n.sweepStore(ctx)
}

// reclaimAll runs reclaim on every volume directory in the store, to
// remove the trees whose PersistentVolumes were deleted while the node
// plugin was down.
func (n *node) reclaimAll(ctx context.Context) {
	entries, err := os.ReadDir(filepath.Join(n.store.root, "volumes"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		n.reclaim(ctx, entry.Name())
	}
}

// usesVolume reports whether this node has staged or published the volume.
func (n *node) usesVolume(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, staged := n.staged[id]
	_, published := n.volumes[id]
	return staged || published
}
