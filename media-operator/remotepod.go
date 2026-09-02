package main

// Each Remote reconciles into one standing pod, owned by the Remote
// through an owner reference, so deleting the Remote tears the pod
// down. The pod holds the controller's device claim, which pins it to
// the machine that owns the radio and runs it whether or not anything
// plays.
//
// The container is the sidecar image in its reader mode, which reads
// the claim's event nodes and publishes each event to the Remote's
// events topic.

// The one container in the standing pod. The pod runs a single reader,
// so its name is the job it does.
const remoteReaderContainer = "reader"

// remotePodName is the name of a Remote's standing pod, the Remote's
// name plus its job, so a person reading either object finds the other.
func remotePodName(remote string) string {
	return remote + "-remote"
}

// remoteClaimName is the standing pod's claim, the pod's name plus the
// suffix the playback claim uses, so a person reading either object
// finds the other.
func remoteClaimName(remote string) string {
	return remotePodName(remote) + "-devices"
}

// remoteOwner is the ownerReference that makes deleting the Remote the
// whole teardown: the garbage collector deletes the claim and the pod
// the Remote owns. The operator deletes either one itself only to
// replace an object that no longer matches the template.
func remoteOwner(remote *Remote) OwnerReference {
	return OwnerReference{
		APIVersion: mediaAPIVersion,
		Kind:       "Remote",
		Name:       remote.Metadata.Name,
		UID:        remote.Metadata.UID,
		Controller: true,
	}
}

// buildRemoteClaim turns one Remote into the standing claim for its
// controller: one request, named for the Remote behind the remote
// prefix, for the device the Remote's spec selects.
//
// The claim tolerates bluetooth.liken.sh/disconnected with no limit, so
// a controller that sleeps keeps its allocation and the pod keeps
// running. It does not tolerate bluetooth.liken.sh/no-input-node, which
// is NoSchedule, so the pod stays Pending until the controller first
// connects, then runs across every sleep after.
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
		PlayerDevice{Class: remote.Spec.Device.Class, Selector: remote.Spec.Device.Selector},
		tolerateForever(remoteDisconnectedTaint),
	)
	return claim
}

// buildRemotePod writes the standing pod: the sidecar image in its
// reader mode, holding the controller's claim and publishing to the
// bus.
//
// restartPolicy is Always because the pod is a service and not a job: a
// crash restarts it, and the pod ends only when the Remote is deleted.
// It carries no IPC volume, because the reader drives no mpv socket.
func buildRemotePod(remote *Remote, claim *ResourceClaim, sidecarImage, busAddress, topicBase string) *Pod {
	container := Container{
		Name:    remoteReaderContainer,
		Image:   sidecarImage,
		Command: []string{"/media-operator", remoteMode},
		Env: []EnvVar{
			{Name: remoteNamespaceVariable, Value: remote.Metadata.Namespace},
			{Name: remoteNameVariable, Value: remote.Metadata.Name},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
		},
	}
	// The discovery variable is set only where the Remote asks for the
	// mode, so an ordinary Remote's pod carries the spec it always
	// carried and does not roll for a field it does not use.
	if remote.Spec.Discovery {
		container.Env = append(container.Env,
			EnvVar{Name: remoteDiscoveryVariable, Value: discoveryOn})
	}
	// The one container holds the claim's one request, the controller
	// this Remote selects.
	for _, request := range claimRequests(claim) {
		container.Resources.Claims = append(container.Resources.Claims,
			ContainerClaim{Name: podClaimName, Request: request})
	}

	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            remotePodName(remote.Metadata.Name),
			Namespace:       remote.Metadata.Namespace,
			OwnerReferences: []OwnerReference{remoteOwner(remote)},
		},
		Spec: PodSpec{
			RestartPolicy: "Always",
			ResourceClaims: []PodResourceClaim{{
				Name:              podClaimName,
				ResourceClaimName: claim.Metadata.Name,
			}},
			Containers: []Container{container},
		},
	}
}

// reconcileRemote reconciles one Remote into its standing claim and
// pod, both owned by the Remote.
//
// The pair follows the template: an edit to the Remote's device
// selector, or a release that changes the sidecar image, deletes the
// stale object and the next pass creates the replacement.
//
// The reader pod drops controller input for the few seconds the
// replacement takes, and it holds no other state a rebuild would have to
// recover. A guard that held the roll while a person watched a film would
// trade those seconds for a pod of unpredictable age, so the pass rolls
// the pod whenever the template changes.
func (o *operator) reconcileRemote(remote *Remote, known claimRead) error {
	claim := buildRemoteClaim(remote)
	pod := buildRemotePod(remote, claim, o.sidecarImage, o.busAddress, o.topicBase)
	return o.reconcileStanding(standingPair(claim, pod), known)
}
