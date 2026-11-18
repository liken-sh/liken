package main

// The driver keeps each volume's credential in memory only, never on the
// node's disk, and never reads a Secret from the API server. The
// CSIDriver sets requiresRepublish, so the kubelet calls
// NodePublishVolume again for every mounted volume on each pod sync,
// with the current contents of the volume's nodePublishSecretRef Secret.
// Those calls give a restarted driver its credentials back, and give a
// running driver a rotated Secret.

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
)

// lostCredential is the health report of a resumed volume until the
// next publish call supplies its credential.
const lostCredential = "The driver restarted and has no credential for this volume " +
	"until the kubelet publishes it again"

// noPublishSecret explains why a resumed volume never gets its
// credential back. The csi block of a PersistentVolume is immutable, so
// the message tells the person to create the PersistentVolume again.
const noPublishSecret = "the PersistentVolume sets nodeStageSecretRef and no nodePublishSecretRef, " +
	"so the driver cannot get the credential of this volume again after it restarts. " +
	"Create the PersistentVolume again with nodePublishSecretRef equal to nodeStageSecretRef"

// same reports whether two credentials are equal, and treats two nils
// as equal.
func (c *credentials) same(other *credentials) bool {
	if c == nil || other == nil {
		return c == other
	}
	return *c == *other
}

// credential takes the volume's lock because a republish can replace
// the credential while the fetch loop or the watch reads it.
func (v *volume) credential() *credentials {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.credentials
}

// hasOrAwaitsCredential reports whether the volume has a credential or is
// waiting for one. The volume's record on disk stores the result, so a
// volume that is still waiting when the driver restarts again waits
// again.
func (v *volume) hasOrAwaitsCredential() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.credentials != nil || v.credentialLost
}

// waitsForCredential reports whether the volume was resumed after a
// restart and has not received its credential yet. The fetch loop and
// the watch skip such a volume, because each attempt would fail and log
// a warning.
func (v *volume) waitsForCredential() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.credentialLost
}

func (v *volume) markCredentialLost() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.credentialLost = true
}

// credentialReport returns the health report of a volume that waits for
// its credential, and false for every other volume. The report replaces
// the error of the last fetch or push, because that error is from before
// the restart, and no fetch or push runs until the credential arrives.
func (v *volume) credentialReport() (string, bool) {
	if !v.credentialLost {
		return "", false
	}
	if v.noPublishSecret {
		return lostCredential + ": " + noPublishSecret, true
	}
	return lostCredential, true
}

// storeCredential stores the credential, ends any wait for one, and
// reports whether the volume was waiting.
func (v *volume) storeCredential(fresh *credentials) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.credentials = fresh
	waited := v.credentialLost
	v.credentialLost = false
	return waited
}

// reportNoPublishSecret records that the PersistentVolume has no
// nodePublishSecretRef, and returns true only the first time, so the
// Event is posted once per volume.
func (v *volume) reportNoPublishSecret() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.noPublishSecret {
		return false
	}
	v.noPublishSecret = true
	return true
}

// checkSecret refuses the first publish of a staged volume when its
// Secret differs from the credential the volume already has from
// nodeStageSecretRef or an earlier publish. Without the check, the
// driver would use one of the two Secrets and nobody would know which.
func checkSecret(vol *volume, supplied *credentials) error {
	current := vol.credential()
	if current != nil && supplied != nil && !current.same(supplied) {
		return status.Error(codes.InvalidArgument,
			"nodePublishSecretRef: the Secret differs from the credential the volume holds, "+
				"which came from nodeStageSecretRef or from an earlier publish; "+
				"name one Secret in both nodeStageSecretRef and nodePublishSecretRef")
	}
	return nil
}

// storeSecret stores the credential of a publish call. The kubelet reads
// the Secret again for each call, so a rotated Secret replaces the stored
// credential at the next pod sync.
//
// A call with no Secret leaves the credential as it is. When the volume
// has a credential or waits for one, that means the PersistentVolume has
// nodeStageSecretRef and no nodePublishSecretRef, and storeSecret reports
// it once.
//
// It returns whether the call changed anything, and republished skips
// the call's log line when it did not.
func (n *node) storeSecret(ctx context.Context, vol *volume, supplied *credentials) bool {
	if supplied == nil {
		return vol.hasOrAwaitsCredential() && n.noPublishSecret(ctx, vol)
	}
	old := vol.credential()
	if old.same(supplied) {
		return false
	}
	switch {
	case vol.storeCredential(supplied):
		n.logger.InfoContext(ctx, "The volume has its credential again", "volume", vol.id)
		n.recordHealth(ctx, vol)
	case old != nil:
		n.logger.InfoContext(ctx, "The credential of the volume changed", "volume", vol.id)
	default:
		// The volume had no credential and was not waiting for one, so
		// its fetches already work without one and nothing needs to run
		// again.
		n.logger.InfoContext(ctx, "The volume received its credential", "volume", vol.id)
		return true
	}
	n.credentialArrived(vol)
	return true
}

// republished handles a repeated publish. The kubelet repeats the call
// for every volume on every pod sync, so a repeat that changes nothing
// is marked quiet and logs no call line.
func (n *node) republished(ctx context.Context, vol *volume, supplied *credentials) {
	if !n.storeSecret(ctx, vol, supplied) {
		quietCall(ctx)
	}
}

// noPublishSecret logs the warning and posts the
// GitVolumeNoPublishSecret Event the first time it is called for a
// volume, and returns true when it did.
func (n *node) noPublishSecret(ctx context.Context, vol *volume) bool {
	if !vol.reportNoPublishSecret() {
		return false
	}
	n.logger.WarnContext(ctx, "The PersistentVolume sets no nodePublishSecretRef", "volume", vol.id, "reason", noPublishSecret)
	n.postVolumeEvent(vol, corev1.EventTypeWarning, reasonNoPublishSecret, noPublishSecret)
	if vol.writeable() {
		n.events.postClaim(vol.claimNow(), corev1.EventTypeWarning, reasonNoPublishSecret, noPublishSecret)
	}
	n.recordHealth(ctx, vol)
	return true
}

// credentialArrived wakes the volume's watch to push and the volume's
// fetch loop to fetch, so the volume does not wait for its next timer.
// After a rotation, fetches with the revoked key fail, and a fetch for a
// demand retries on a backoff of up to maxDemandRetry. The new
// credential ends that wait.
func (n *node) credentialArrived(vol *volume) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if watch, found := n.watchers[vol.id]; found && watch.volume == vol {
		watch.credentialArrived()
	}
	if !vol.attributes.pull.follows() {
		return
	}
	if loop := n.loopOf(vol); loop != nil {
		loop.credentialArrived(vol)
	}
}

// credentialArrived adds the volume to wanted and wakes the loop, which
// runs a pass at once, without the --demand-min-interval delay of a
// demand. Because the volume is in wanted, a failed fetch retries on the
// demand backoff.
func (f *follower) credentialArrived(vol *volume) {
	f.mu.Lock()
	if f.volumes[vol.id] != vol {
		f.mu.Unlock()
		return
	}
	f.wanted[vol.id] = vol
	f.mu.Unlock()
	select {
	case f.republishWake <- struct{}{}:
	default:
	}
}

func (w *watcher) credentialArrived() {
	select {
	case w.republishWake <- struct{}{}:
	default:
	}
}

// pushArrived pushes the commits made while the credential was missing
// or revoked. It commits nothing, because only the quiesce period of the
// volume's class decides when a write is finished.
func (w *watcher) pushArrived(ctx context.Context) {
	w.node.push(ctx, w.volume)
	w.node.recordHealth(ctx, w.volume)
}
