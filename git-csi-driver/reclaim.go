package main

// reclaim.go keeps or removes a writeable volume's work tree when its
// PersistentVolume is deleted, by the PersistentVolume's reclaim
// policy. Kubernetes gives the policy the same meaning for every
// volume: Retain keeps the data after the PersistentVolume is deleted,
// and Delete removes it. The data of a git volume is its work trees,
// one on each node that staged it, and never the remote repository.
//
// csi-provisioner, the upstream sidecar beside the controller, deletes
// a released PersistentVolume of the Delete policy after DeleteVolume
// answers. Each node plugin watches PersistentVolumes already, and
// removes its own work tree when the watch reports the delete. A node
// plugin that was down during the delete finds the tree at its next
// start: no PersistentVolume carries the handle any more. The
// PersistentVolume is gone by then, so the node records the policy in
// the volume's directory at each stage and at each change the watch
// reports, and reads the policy back from that file.
//
// A Retain tree stays until a person removes it or a new
// PersistentVolume with the same handle stages it again, and its next
// stage pushes any commit the last unstage could not.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// policyFileName is the file in a volume's directory that records the
// reclaim policy of the PersistentVolume that carries the volume.
const policyFileName = "reclaim-policy"

// policyFile is where the volume's directory records its reclaim
// policy.
func (s *store) policyFile(id string) string {
	return filepath.Join(s.volumeDir(id), policyFileName)
}

// recordedPolicy is the policy the volume's directory records, and
// empty where it records none. A tree that no stage recorded a policy
// for answers empty, and empty keeps the tree.
func (s *store) recordedPolicy(id string) corev1.PersistentVolumeReclaimPolicy {
	content, err := os.ReadFile(s.policyFile(id))
	if err != nil {
		return ""
	}
	return corev1.PersistentVolumeReclaimPolicy(trimLine(string(content)))
}

// oneElement reports whether a volume handle names one directory in
// the store. The kubelet stages no other handle, and a handle the watch
// reads comes from any PersistentVolume of the driver, which a person
// writes.
func oneElement(handle string) bool {
	return handle != "" && handle != "." && handle != ".." &&
		!strings.ContainsRune(handle, filepath.Separator)
}

// notePolicy records the reclaim policy of a PersistentVolume of the
// driver, when this node holds a work tree for its handle. A policy the
// file already records is not written again, because the watch reports
// every status write on the PersistentVolume too.
func (n *node) notePolicy(ctx context.Context, held *corev1.PersistentVolume) {
	source := held.Spec.CSI
	if source == nil || source.Driver != driverName || !oneElement(source.VolumeHandle) {
		return
	}
	id := source.VolumeHandle
	if !n.store.tree(id).exists() {
		return
	}
	policy := held.Spec.PersistentVolumeReclaimPolicy
	if n.store.recordedPolicy(id) == policy {
		return
	}
	if err := os.WriteFile(n.store.policyFile(id), []byte(string(policy)+"\n"), 0o600); err != nil {
		n.logger.WarnContext(ctx, "the reclaim policy was not recorded",
			"volume", id, "policy", policy, "error", err)
	}
}

// notePolicyOf records the policy of the PersistentVolume that carries
// the handle, as the watch holds it.
func (n *node) notePolicyOf(ctx context.Context, id string) {
	if held, found := n.demands.heldVolume(id); found {
		n.notePolicy(ctx, held)
	}
}

// reclaim removes the work tree of a volume when this node holds no
// stage of it, no PersistentVolume carries its handle, and the last
// policy the node recorded for it is Delete. A commit in the tree that
// no push sent goes with it, as Delete means for every volume, and the
// log says so. The log names the side branch of a diverged tree too,
// because its commits are on the remote but not on the followed ref.
func (n *node) reclaim(ctx context.Context, id string) {
	if !oneElement(id) || n.holds(id) || n.demands.carried(id) {
		return
	}
	if n.store.recordedPolicy(id) != corev1.PersistentVolumeReclaimDelete {
		return
	}
	work := n.store.tree(id)
	unpushed := work.refCommit(ctx, "HEAD") != work.refCommit(ctx, pushedRef)
	diverged := work.divergedBranch(ctx)
	if err := os.RemoveAll(work.directory); err != nil {
		n.logger.WarnContext(ctx, "the work tree stayed", "volume", id, "error", err)
		return
	}
	n.logger.InfoContext(ctx, "removed the work tree", "volume", id,
		"reason", "its PersistentVolume was deleted with the Delete reclaim policy",
		"unpushed", unpushed, "diverged", diverged)
	// The tree was the last to name its repository, or one of several,
	// so the walk removes or collects the repository now and not up to
	// an hour later.
	n.sweepStore(ctx)
}

// reclaimAll reclaims every volume directory in the store, which is
// how a node plugin that was down during a delete removes the tree it
// missed.
func (n *node) reclaimAll(ctx context.Context) {
	entries, err := os.ReadDir(filepath.Join(n.store.root, "volumes"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		n.reclaim(ctx, entry.Name())
	}
}

// holds reports the volumes this node has staged or published, which
// stay whatever their PersistentVolume does.
func (n *node) holds(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, staged := n.staged[id]
	_, published := n.volumes[id]
	return staged || published
}
