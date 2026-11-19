package main

// Each Remote becomes one long-running pod that runs whether or not
// anything is playing. The pod's claim for the controller's device
// schedules it on the machine that has the radio. Its container runs the
// sidecar image in reader mode, which reads the event nodes of the
// claimed device and publishes each event to the Remote's events topic.

const remoteReaderContainer = "reader"

// remotePodName is the name of a Remote's pod. It is the Remote's name
// with a suffix, so a person can find one object from the other.
func remotePodName(remote string) string {
	return remote + "-remote"
}

// remoteClaimName is the name of the pod's claim: the pod's name plus
// the "-devices" suffix, which the playback claim also uses.
func remoteClaimName(remote string) string {
	return remotePodName(remote) + "-devices"
}

// remoteOwner makes the Remote the controller owner of its claim and
// pod, so the garbage collector deletes both when the Remote is deleted.
// The operator deletes either object itself only to replace one that no
// longer matches its template.
func remoteOwner(remote *Remote) OwnerReference {
	return OwnerReference{
		APIVersion: mediaAPIVersion,
		Kind:       "Remote",
		Name:       remote.Metadata.Name,
		UID:        remote.Metadata.UID,
		Controller: true,
	}
}

// buildRemoteClaim builds the claim for a Remote's controller. It has
// one request, for the device that the Remote's spec selects.
//
// The request tolerates the taint bluetooth.liken.sh/disconnected with
// no time limit, so a controller that goes to sleep keeps its allocation
// and the pod keeps running. It does not tolerate the NoSchedule taint
// bluetooth.liken.sh/no-input-node, so the pod stays Pending until the
// controller connects for the first time.
func buildRemoteClaim(remote *Remote) *ResourceClaim {
	claim := &ResourceClaim{
		APIVersion: claimAPIVersion,
		Kind:       "ResourceClaim",
		Metadata: ObjectMeta{
			Name:            remoteClaimName(remote.Metadata.Name),
			Namespace:       remote.Metadata.Namespace,
			OwnerReferences: []OwnerReference{remoteOwner(remote)},
		},
	}
	claim.add(
		remoteRequestName(remote.Metadata.Name),
		PlayerDevice{
			Class:    remote.Spec.Device.Class,
			Selector: remote.Spec.Device.Selector,
			// The parameters are copied as the Remote has them. The
			// input driver defines and validates them, and this
			// operator does not read them.
			Parameters: remote.Spec.Device.Parameters,
		},
		tolerateForever(remoteDisconnectedTaint),
	)
	return claim
}

// buildRemotePod builds the Remote's reader pod. restartPolicy is Always
// because the pod runs until the Remote is deleted. Unlike the playback
// pod, it has no IPC volume, because the reader uses no mpv socket.
func buildRemotePod(remote *Remote, claim *ResourceClaim, sidecarImage, busAddress, topicBase string) *Pod {
	container := Container{
		Name:    remoteReaderContainer,
		Image:   sidecarImage,
		Command: []string{podBinary, remoteMode},
		Env: []EnvVar{
			{Name: remoteNamespaceVariable, Value: remote.Metadata.Namespace},
			{Name: remoteNameVariable, Value: remote.Metadata.Name},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
		},
	}
	// The variable is set only when discovery is on, so the pod spec of
	// a Remote without discovery does not change and its pod is not
	// replaced.
	if remote.Spec.Discovery {
		container.Env = append(container.Env,
			EnvVar{Name: remoteDiscoveryVariable, Value: discoveryOn})
	}
	container.Resources.Claims = claimEntries(claim)

	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            remotePodName(remote.Metadata.Name),
			Namespace:       remote.Metadata.Namespace,
			Labels:          map[string]string{playbackLabelKey: remoteLabelValue},
			OwnerReferences: []OwnerReference{remoteOwner(remote)},
		},
		Spec: PodSpec{
			RestartPolicy:                "Always",
			AutomountServiceAccountToken: noServiceAccountToken(),
			ResourceClaims: []PodResourceClaim{{
				Name:              podClaimName,
				ResourceClaimName: claim.Metadata.Name,
			}},
			Containers:  []Container{container},
			Tolerations: playerNodeTolerations(),
		},
	}
}

// reconcileRemote creates the claim and the pod of one Remote. When the
// Remote's device spec or the sidecar image changes, the existing object
// no longer matches its template, so this pass deletes it and the next
// pass creates the replacement. The pass replaces the reader pod at once,
// even while something plays, because the pod has no state to recover
// and loses only the controller's input until the new pod starts.
func (o *operator) reconcileRemote(remote *Remote, known claimRead) error {
	claim := buildRemoteClaim(remote)
	pod := o.resources.apply(buildRemotePod(remote, claim, o.sidecarImage, o.busAddress, o.topicBase))
	return o.reconcilePersistent(newPersistentPair("remote "+remote.Metadata.Namespace+"/"+remote.Metadata.Name, remoteRef(remote), claim, pod), known)
}
