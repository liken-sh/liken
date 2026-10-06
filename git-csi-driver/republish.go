package main

// republish.go takes a volume's credential from the kubelet's repeated
// NodePublishVolume calls.
//
// A Secret reaches the driver only inside a kubelet call, and the driver
// keeps it in memory and never on the node's disk. The CSIDriver sets
// requiresRepublish, so the kubelet calls NodePublishVolume again for
// every mounted volume on each pod sync, and reads the volume's
// nodePublishSecretRef Secret again for each call. A driver that
// restarted takes its credentials back from those calls, with no read of
// a Secret of its own.

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
)

// lostCredential is what a volume reports from the moment a restarted
// driver resumes it until a republish returns its credential.
const lostCredential = "the driver restarted and holds no credential for this volume " +
	"until the kubelet publishes it again"

// noPublishSecret is what a volume says when no publish can return its
// credential. A PersistentVolume's csi block is immutable, so the fix is
// a new PersistentVolume.
const noPublishSecret = "the PersistentVolume names nodeStageSecretRef and no nodePublishSecretRef, " +
	"so the driver cannot take this volume's credential back after it restarts; " +
	"create the PersistentVolume again with nodePublishSecretRef equal to nodeStageSecretRef"

// same reports whether two credentials hold the same keys. Two absent
// credentials are the same.
func (c *credentials) same(other *credentials) bool {
	if c == nil || other == nil {
		return c == other
	}
	return *c == *other
}

// credential is the Secret the volume fetches and pushes with. A
// republish replaces it while the fetch loop and the watch read it, so
// every read takes the volume's lock.
func (v *volume) credential() *credentials {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.credentials
}

// needsCredential reports whether the volume had a credential, which is
// what the record keeps. A resumed volume that waits for its credential
// still needs one, so a second restart also waits.
func (v *volume) needsCredential() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.credentials != nil || v.credentialLost
}

// waitsForCredential reports whether the volume is a resumed volume
// that had a credential and holds none yet. Such a volume fetches and
// pushes nothing, because every attempt fails without the credential and
// writes a warning. The republish that returns the credential starts the
// fetch or the push at once.
func (v *volume) waitsForCredential() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.credentialLost
}

// loseCredential marks a resumed volume that had a credential and holds
// none now.
func (v *volume) loseCredential() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.credentialLost = true
}

// credentialReport is what a volume that waits for its credential
// reports, and false for every other volume. It comes before a failed
// fetch or push in the report: the failure is from before the restart,
// and no fetch or push runs until the credential returns.
func (v *volume) credentialReport() (string, bool) {
	if !v.credentialLost {
		return "", false
	}
	if v.noPublishSecret {
		return lostCredential + ": " + noPublishSecret, true
	}
	return lostCredential, true
}

// takeCredential holds the credential a publish carried, and reports
// whether the volume waited for one after a restart. That credential
// also clears the report of the wait.
func (v *volume) takeCredential(fresh *credentials) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.credentials = fresh
	waited := v.credentialLost
	v.credentialLost = false
	return waited
}

// reportNoPublishSecret records that no publish carries the volume's
// credential, and reports whether this is the first time for this
// volume, which is when the Event goes out. A volume that waits for its
// credential then reports why none arrives.
func (v *volume) reportNoPublishSecret() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.noPublishSecret {
		return false
	}
	v.noPublishSecret = true
	return true
}

// checkSecret refuses a first publish of a staged volume whose Secret
// differs from the credential the volume holds. That credential came
// from the stage, or from a later republish after a rotation. Without
// the refusal the driver would choose one of the two Secrets in
// silence.
func checkSecret(held *volume, holder *credentials) error {
	current := held.credential()
	if current != nil && holder != nil && !current.same(holder) {
		return status.Error(codes.InvalidArgument,
			"nodePublishSecretRef: the Secret differs from the credential the volume holds, "+
				"which came from nodeStageSecretRef or from an earlier publish; "+
				"name one Secret in both nodeStageSecretRef and nodePublishSecretRef")
	}
	return nil
}

// takeSecret holds the credential of a publish call. A repeated publish
// carries the Secret the kubelet read just now, which is the one to use
// after a rotation, so it replaces what the volume holds.
//
// A publish with no Secret leaves the credential in place. When the
// volume holds a credential from the stage, or waits for one after a
// restart, the PersistentVolume names no publish Secret, and the volume
// says so.
//
// It reports whether the call changed anything, which is what decides
// whether a repeated publish is worth a line in the log.
func (n *node) takeSecret(ctx context.Context, held *volume, holder *credentials) bool {
	if holder == nil {
		return held.needsCredential() && n.noPublishSecret(ctx, held)
	}
	old := held.credential()
	if old.same(holder) {
		return false
	}
	switch {
	case held.takeCredential(holder):
		n.logger.InfoContext(ctx, "the credential returned", "volume", held.id)
		n.noteHealth(ctx, held)
	case old != nil:
		n.logger.InfoContext(ctx, "the credential changed", "volume", held.id)
	default:
		// A first publish that carries the Secret the stage did not.
		// The stage fetched without a credential and worked, so no
		// fetch or push waits for this one.
		n.logger.InfoContext(ctx, "the credential arrived", "volume", held.id)
		return true
	}
	n.credentialArrived(held)
	return true
}

// republished takes the credential of a repeated publish. A repeat that
// changes nothing is marked quiet, so the call's log line is left out:
// the kubelet repeats the call for every volume on each pod sync, and a
// line for each would bury the calls that did something.
func (n *node) republished(ctx context.Context, held *volume, holder *credentials) {
	if !n.takeSecret(ctx, held, holder) {
		quietCall(ctx)
	}
}

// noPublishSecret posts the Event and writes the log line once for the
// volume, and reports whether it did.
func (n *node) noPublishSecret(ctx context.Context, held *volume) bool {
	if !held.reportNoPublishSecret() {
		return false
	}
	n.logger.WarnContext(ctx, "no publish Secret", "volume", held.id, "reason", noPublishSecret)
	n.tell(held, corev1.EventTypeWarning, reasonNoPublishSecret, noPublishSecret)
	if held.writeable() {
		n.events.postClaim(held.claimNow(), corev1.EventTypeWarning, reasonNoPublishSecret, noPublishSecret)
	}
	n.noteHealth(ctx, held)
	return true
}

// credentialArrived starts the work the missing or old credential held
// back, at once and not at the next timer. A read-only volume fetches
// its ref, and a writeable volume pushes what it committed.
//
// A restart is one case: every fetch and push before the republish
// failed without a credential. A rotation is the other: when the old
// key is revoked, a fetch or a push with it fails, and a fetch that a
// demand asked for waits a backoff of up to five minutes before it
// tries again. The new key ends that wait. A rotation is rare, so the
// cost is one extra fetch or push for each volume at each rotation.
func (n *node) credentialArrived(held *volume) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if seeing, found := n.watchers[held.id]; found && seeing.volume == held {
		seeing.credentialArrived()
	}
	if !held.attributes.pull.follows() {
		return
	}
	if loop := n.loopOf(held); loop != nil {
		loop.credentialArrived(held)
	}
}

// credentialArrived wants a pass for the volume and wakes the loop
// past --demand-min-interval. The pass counts no demand, because nobody
// demanded it. A pass that works ends the backoff, and a pass that
// fails fetches again after the backoff a failed demand takes.
func (f *follower) credentialArrived(held *volume) {
	f.mu.Lock()
	if f.volumes[held.id] != held {
		f.mu.Unlock()
		return
	}
	f.wanted[held.id] = held
	f.mu.Unlock()
	select {
	case f.arrived <- struct{}{}:
	default:
	}
}

// credentialArrived wakes the watch to push.
func (w *watcher) credentialArrived() {
	select {
	case w.arrived <- struct{}{}:
	default:
	}
}

// pushArrived pushes what the tree committed while the credential was
// missing or old. It commits nothing, because the class's quiesce
// decides when a write is finished.
func (w *watcher) pushArrived(ctx context.Context) {
	w.node.push(ctx, w.volume)
	w.node.noteHealth(ctx, w.volume)
}
