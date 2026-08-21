package main

// The playback pod is the whole of what a Play becomes at run time.
// The trust split shows in what the pod does not get: it decodes media
// from the network, so it carries no ServiceAccount and no API
// credentials. mpv is the pod's own process, and the kubelet sends it
// the grace-period signal and reads its exit code. One native sidecar
// is the pod's bus client, and everything the pod says goes over the
// bus, which the operator alone reads onto a Play's status.

import "encoding/json"

// The container names, and the claim's pod-local name the container's
// resources.claims entries repeat.
const (
	playerContainer = "player"
	bridgeContainer = "bridge"
	podClaimName    = "devices"
)

// An init container with restartPolicy Always is what Kubernetes calls
// a native sidecar: the kubelet starts it before the player, keeps it
// beside the player, and restarts it alone when it exits. An ordinary
// container would not do, because the pod's restart policy is Never and
// a bridge that exited would stay down; and the pod must still end when
// the player ends, which a second ordinary container would also
// prevent.
const sidecarRestartPolicy = "Always"

// playbackGracePeriod is five seconds because mpv exits on the SIGTERM
// the kubelet sends, measured near one second, and nothing here has
// state to flush.
const playbackGracePeriod = 5

func podName(play string) string {
	return play + "-playback"
}

// buildPod writes the pod a person wrote by hand before this operator
// existed. restartPolicy is Never because the pod's end is the play's
// end: a finished film is not a failure to restart. The player image's
// entrypoint shim execs mpv, so the arguments are nothing but the
// resolved playlist in spec order.
func buildPod(play *Play, claim *ResourceClaim, resolved resolution, image, busAddress, topicBase string, remotes []boundRemote) *Pod {
	grace := int64(playbackGracePeriod)
	// The IPC volume is unconditional, so mpv serves its socket at one
	// path whether or not this pod binds a remote. The mount list is
	// built fresh rather than appended to resolved.Mounts, so the
	// resolution's own slice is never written through.
	mounts := make([]VolumeMount, 0, len(resolved.Mounts)+1)
	mounts = append(mounts, resolved.Mounts...)
	mounts = append(mounts, ipcMount())

	container := Container{
		Name:  playerContainer,
		Image: image,
		// The image's entrypoint shim runs mpv, so the pod supplies
		// only what to play.
		Args:         resolved.Items,
		VolumeMounts: mounts,
	}
	// The start is added only when the spec declares one, so an
	// ordinary run's pod carries nothing extra. The shim reads it and
	// turns it into mpv's --start.
	if play.Spec.Start != "" {
		container.Env = append(container.Env,
			EnvVar{Name: playStartVariable, Value: play.Spec.Start})
	}
	// The player container holds every request the claim asks for,
	// because the playback claim holds the player's roles alone.
	for _, request := range claimRequests(claim) {
		container.Resources.Claims = append(container.Resources.Claims,
			ContainerClaim{Name: podClaimName, Request: request})
	}

	volumes := make([]Volume, 0, len(resolved.Volumes)+1)
	volumes = append(volumes, resolved.Volumes...)
	volumes = append(volumes, Volume{Name: ipcVolumeName, EmptyDir: &EmptyDirVolumeSource{}})

	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            podName(play.Metadata.Name),
			Namespace:       play.Metadata.Namespace,
			OwnerReferences: []OwnerReference{playOwner(play)},
		},
		Spec: PodSpec{
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: &grace,
			ResourceClaims: []PodResourceClaim{{
				Name:              podClaimName,
				ResourceClaimName: claim.Metadata.Name,
			}},
			InitContainers: []Container{bridgeSidecar(play, image, busAddress, topicBase, remotes)},
			Containers:     []Container{container},
			Volumes:        volumes,
		},
	}
}

// bridgeSidecar is the playback pod's one bus client: the player image
// in its bridge mode, holding no device claim, reaching mpv through the
// shared IPC socket. It subscribes to each bound remote's events topic,
// applies that remote's compiled table, and publishes mpv's report to
// the Play's status topic.
//
// PROSE: phase 2 rewrites the bridge process and may refine this
// sidecar's shape.
func bridgeSidecar(play *Play, image, busAddress, topicBase string, remotes []boundRemote) Container {
	entries := make([]remoteBindings, 0, len(remotes))
	for _, remote := range remotes {
		entries = append(entries, remoteBindings{EventsTopic: remote.EventsTopic, Bindings: remote.Bindings})
	}
	// The marshal error is dropped: a slice of structs whose fields are
	// strings and integers marshals unconditionally.
	encoded, _ := json.Marshal(entries)
	return Container{
		Name:    bridgeContainer,
		Image:   image,
		Command: []string{"/media-operator", bridgeMode},
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: play.Metadata.Namespace},
			{Name: playNameVariable, Value: play.Metadata.Name},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
			{Name: remotesVariable, Value: string(encoded)},
		},
		VolumeMounts:  []VolumeMount{ipcMount()},
		RestartPolicy: sidecarRestartPolicy,
	}
}

func ipcMount() VolumeMount {
	return VolumeMount{Name: ipcVolumeName, MountPath: ipcMountPath}
}
