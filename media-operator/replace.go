package main

// A recreate gives the new playback pod the old pod's name, so it needs a
// delete, a wait, and a create. The API server keeps a deleted pod until
// the kubelet has stopped all of its containers, and a create under that
// name fails with 409 until then. The sidecars use their termination grace
// period, so this can take a second or more.
//
// replace sends the delete and records the reason in o.replacements.
// Passes that find the pod terminating leave it alone, and the first pass
// that finds it gone creates the new pod. Without the wait, every pass
// during the grace period would send another delete and get a 409.

import (
	"errors"
	"fmt"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// replace deletes the run's pod, and also its claim when claimChanged is
// set. If the pod name is already free, it creates the new pod at the
// film's saved position and returns it. Otherwise it returns no pod, and
// a later pass creates the pod.
//
// It marks the old pod's UID as gone in o.reports before it sends the
// delete, because the old pod's sidecar reports the end of playback when
// it receives SIGTERM, and that report must not end the Play.
func (o *operator) replace(play *Play, player *Player, running *Pod, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote, claimChanged bool, reason recreate) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	o.reports.markReplaced(namespace, name, running.Metadata.UID)
	if err := DeletePod(o.client, namespace, podName(name)); err != nil {
		o.reports.unmarkReplaced(namespace, name, running.Metadata.UID)
		return nil, err
	}
	if claimChanged {
		if err := DeleteResourceClaim(o.client, namespace, claimName(name)); err != nil {
			return nil, err
		}
	}
	o.replacements[runKey(namespace, name)] = reason
	_, err := GetPod(o.client, namespace, podName(name))
	if errors.Is(err, apiclient.ErrNotFound) {
		return o.finishReplacement(play, player, claim, resolved, prefs, remotes, reason)
	}
	return nil, err
}

// finishReplacement creates the new pod after the old pod is gone, and
// creates the claim first when it is missing. It returns no pod while the
// old claim is still being deleted, because a pod must not use a claim
// that is being deleted.
func (o *operator) finishReplacement(play *Play, player *Player, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote, reason recreate) (*Pod, error) {
	key := runKey(play.Metadata.Namespace, play.Metadata.Name)
	held, err := GetResourceClaim(o.client, claim.Metadata.Namespace, claim.Metadata.Name)
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		if _, err := CreateResourceClaim(o.client, claim); err != nil && !errors.Is(err, apiclient.ErrConflict) {
			return nil, err
		}
	case err != nil:
		return nil, err
	case held.Metadata.deleting():
		// The claim is deleted only after the old pod is gone, so this pass
		// can find the pod gone and the claim still terminating. The claim's
		// deletion starts another pass (changewake.go), which creates the pod.
		return nil, nil
	}
	pod, err := o.createPodAtSavedPosition(play, player, claim, resolved, prefs, remotes)
	if err != nil {
		return nil, err
	}
	delete(o.replacements, key)
	o.notePodRecreated(play, fmt.Sprintf("recreated playback pod %s at %s, because %s",
		pod.Metadata.Name, startName(o.savedPosition(play)), reason.reason), reason.fault)
	return pod, nil
}

// recreate is the reason for creating a run's pod again. fault is true
// when the pod failed or disappeared, and notePodRecreated then posts a
// Warning Event. It is false when a spec edit changed the pod, and the
// Event is Normal.
type recreate struct {
	reason string
	fault  bool
}
