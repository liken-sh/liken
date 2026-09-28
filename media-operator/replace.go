package main

// A recreate replaces the playback pod under the same name, so it is two
// steps with a wait between them. The API server keeps a deleted pod,
// marked with a deletionTimestamp, until the kubelet has stopped every
// container in it, and a create under that name answers 409 until then.
// mpv stops at once, but the sidecars take their grace period, so the old
// pod stands for a second or more.
//
// The operator sends the delete and keeps the reason in
// operator.replacements. Each pass that reads the terminating pod leaves
// it alone. The pod watch reports the delete, and the pass that finds the
// name free creates the new pod and writes the one line. Without the wait,
// every pass in that window reads the old remote set off the terminating
// pod, sends another delete, and meets the 409.

import "errors"

// replace deletes the run's pod, and its claim when the claim itself
// diverged, and creates the new pod at the film's place when the name is
// already free. Otherwise the pass that finds the name free creates it,
// and replace returns no pod.
func (o *operator) replace(play *Play, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote, claimChanged bool, reason string) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if err := DeletePod(o.client, namespace, podName(name)); err != nil {
		return nil, err
	}
	if claimChanged {
		if err := DeleteResourceClaim(o.client, namespace, claimName(name)); err != nil {
			return nil, err
		}
	}
	o.replacements[runKey(namespace, name)] = reason
	_, err := GetPod(o.client, namespace, podName(name))
	if errors.Is(err, ErrNotFound) {
		return o.finishReplacement(play, claim, resolved, prefs, remotes, reason)
	}
	return nil, err
}

// finishReplacement creates the pod a recreate owes, once the old pod is
// gone. A claim that is still being deleted is not the claim the new pod
// must hold, so the pod waits for the claim too.
func (o *operator) finishReplacement(play *Play, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote, reason string) (*Pod, error) {
	key := runKey(play.Metadata.Namespace, play.Metadata.Name)
	held, err := GetResourceClaim(o.client, claim.Metadata.Namespace, claim.Metadata.Name)
	switch {
	case errors.Is(err, ErrNotFound):
		if _, err := CreateResourceClaim(o.client, claim); err != nil && !errors.Is(err, ErrConflict) {
			return nil, err
		}
	case err != nil:
		return nil, err
	case held.Metadata.deleting():
		// A claim that the old pod held finishes its delete only after the
		// pod is gone, so the pass that finds the pod gone can still find
		// the claim on its way out. The claim's removal wakes the pass
		// (changewake.go), and that pass creates the pod.
		return nil, nil
	}
	pod, err := o.createPodAtStash(play, claim, resolved, prefs, remotes)
	if err != nil {
		return nil, err
	}
	delete(o.replacements, key)
	logLine(o.log, "play %s: recreated playback pod %s at %s, because %s",
		key, pod.Metadata.Name, startName(o.stashedPosition(play)), reason)
	return pod, nil
}
