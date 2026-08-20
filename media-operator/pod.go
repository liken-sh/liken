package main

// The playback pod is the whole of what a Play becomes at run time.
// The trust split shows in what the pod does not get: it decodes
// media from the network, so it carries no ServiceAccount and no API
// credentials, and everything it has to say goes over plain HTTP to
// the operator, which holds the only pen on the status.

// The container is named for its job, and the claim's pod-local name
// is the one the container's resources.claims entries repeat.
const (
	playerContainer = "player"
	podClaimName    = "devices"
)

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
func buildPod(play *Play, claim *ResourceClaim, resolved resolution, image, token, operatorURL string) *Pod {
	grace := int64(playbackGracePeriod)
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
		VolumeMounts: resolved.Mounts,
	}
	// One entry per request, so the container holds every device
	// the player needs, and a later plan can keep a role out of a
	// container that must not hold it by naming fewer.
	for _, request := range claimRequests(claim) {
		container.Resources.Claims = append(container.Resources.Claims,
			ContainerClaim{Name: podClaimName, Request: request})
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
			Containers: []Container{container},
			Volumes:    resolved.Volumes,
		},
	}
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
