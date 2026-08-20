package main

// The playback pod is the whole of what a Play becomes at run time.
// The trust split shows in what the pod does not get: it decodes
// media from the network, so it carries no ServiceAccount and no API
// credentials, and everything it has to say goes over plain HTTP to
// the operator, which holds the only pen on the status.

import "encoding/json"

// The container is named for its job, and the claim's pod-local name
// is the one the container's resources.claims entries repeat.
const (
	playerContainer = "player"
	podClaimName    = "devices"
)

// An init container with restartPolicy Always is what Kubernetes
// calls a native sidecar: the kubelet starts it before the player,
// keeps it beside the player, and restarts it alone when it exits.
// An ordinary container would not do, because the pod's restart
// policy is Never and a remote that exited would stay down; and the
// pod must still end when the player ends, which a second ordinary
// container would also prevent.
const sidecarRestartPolicy = "Always"

// playbackGracePeriod is five seconds because mpv exits on the
// SIGTERM the supervisor forwards, measured near one second, and
// nothing here has state to flush.
const playbackGracePeriod = 5

func podName(play string) string {
	return play + "-playback"
}

// buildPod writes the pod a person wrote by hand before this
// operator existed. restartPolicy is Never because the pod's end is
// the play's end: a finished film is not a failure to restart. The
// image's entrypoint already selects the supervise mode, so the
// arguments are nothing but the resolved playlist in spec order.
func buildPod(play *Play, claim *ResourceClaim, resolved resolution, image, token, operatorURL string, remotes []boundRemote) *Pod {
	grace := int64(playbackGracePeriod)
	// The IPC volume is unconditional, so mpv serves its socket at
	// one path whether or not this pod carries a remote. The mount
	// list is built fresh rather than appended to resolved.Mounts,
	// so the resolution's own slice is never written through.
	mounts := make([]VolumeMount, 0, len(resolved.Mounts)+1)
	mounts = append(mounts, resolved.Mounts...)
	mounts = append(mounts, ipcMount())

	container := Container{
		Name:  playerContainer,
		Image: image,
		// The image's entrypoint names the supervise mode, so the
		// pod supplies only what to play.
		Args: resolved.Items,
		// These four values are the whole of what the pod knows
		// about the control plane: who it is, what proves it, and
		// where to report.
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: play.Metadata.Namespace},
			{Name: playNameVariable, Value: play.Metadata.Name},
			{Name: playTokenVariable, Value: token},
			{Name: operatorURLVariable, Value: operatorURL},
		},
		VolumeMounts: mounts,
	}
	// The start is added only when the spec declares one, so an
	// ordinary run's pod carries nothing extra.
	if play.Spec.Start != "" {
		container.Env = append(container.Env,
			EnvVar{Name: playStartVariable, Value: play.Spec.Start})
	}
	// One entry per player role, so the container holds every device
	// the player needs and nothing else: a controller's input nodes
	// are held by that controller's sidecar alone.
	for _, request := range playerRequests(claim) {
		container.Resources.Claims = append(container.Resources.Claims,
			ContainerClaim{Name: podClaimName, Request: request})
	}

	volumes := make([]Volume, 0, len(resolved.Volumes)+1)
	volumes = append(volumes, resolved.Volumes...)
	volumes = append(volumes, Volume{Name: ipcVolumeName, EmptyDir: &EmptyDirVolumeSource{}})

	sidecars := make([]Container, 0, len(remotes))
	for _, remote := range remotes {
		sidecars = append(sidecars, remoteSidecar(remote, image))
	}

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
			InitContainers: sidecars,
			Containers:     []Container{container},
			Volumes:        volumes,
		},
	}
}

// remoteSidecar is one Remote's container: the player image in its
// remote mode, holding that controller's request and the compiled
// table. The table travels in an environment variable rather than a
// ConfigMap, so the pod still reads nothing from the API server and
// the map is exactly as immutable as the container set around it.
func remoteSidecar(remote boundRemote, image string) Container {
	// The marshal error is dropped: a slice of structs whose fields
	// are integers and strings marshals unconditionally.
	bindings, _ := json.Marshal(remote.Bindings)
	name := remoteRequestName(remote.Name)
	return Container{
		Name:          name,
		Image:         image,
		Command:       []string{"/media-operator", remoteMode},
		Env:           []EnvVar{{Name: keymapVariable, Value: string(bindings)}},
		Resources:     ResourceRequirements{Claims: []ContainerClaim{{Name: podClaimName, Request: name}}},
		VolumeMounts:  []VolumeMount{ipcMount()},
		RestartPolicy: sidecarRestartPolicy,
	}
}

func ipcMount() VolumeMount {
	return VolumeMount{Name: ipcVolumeName, MountPath: ipcMountPath}
}

// tokenFromPod recovers the minted token from the pod spec. An
// operator that restarted holds no tokens, and the pod it left
// running still reports, so the spec's env is where the token
// survives and the next pass adopts it from there.
func tokenFromPod(pod *Pod) string {
	for _, container := range pod.Spec.Containers {
		if container.Name != playerContainer {
			continue
		}
		for _, variable := range container.Env {
			if variable.Name == playTokenVariable {
				return variable.Value
			}
		}
	}
	return ""
}
