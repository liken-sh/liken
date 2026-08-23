package main

// The playback pod is the whole of what a Play becomes at run time.
// The trust split shows in what the pod does not get: it decodes media
// from the network, so it carries no ServiceAccount and no API
// credentials. mpv is the pod's own process, and the kubelet sends it
// the grace-period signal and reads its exit code. One native sidecar
// is the pod's bus client, and everything the pod says goes over the
// bus, which the operator alone reads onto a Play's status.

import (
	"encoding/json"
	"slices"
	"strings"
)

// The container names, and the claim's pod-local name the container's
// resources.claims entries repeat.
const (
	playerContainer  = "player"
	commandContainer = "command"
	podClaimName     = "devices"
)

// Every playback pod carries this label, so the pod watch selects the
// operator's own pods and nothing else on the node.
const (
	playbackLabelKey   = "media.liken.sh/component"
	playbackLabelValue = "playback"
)

// translatorContainer names one controller's translator sidecar. The
// name carries the Remote's name, so the container set changes when a
// controller is added or removed, and the operator reads the set back to
// tell whether a Player reshaped a running pod.
func translatorContainer(remote string) string {
	return "translate-" + remote
}

// An init container with restartPolicy Always is what Kubernetes calls a
// native sidecar: the kubelet starts it before the player, keeps it
// beside the player, and restarts it alone when it exits. An ordinary
// container would not do, because the pod's restart policy is Never and a
// sidecar that exited would stay down; and the pod must still end when
// the player ends, which a second ordinary container would also prevent.
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
	mounts := make([]VolumeMount, 0, len(resolved.Mounts)+2)
	mounts = append(mounts, resolved.Mounts...)
	mounts = append(mounts, artMount(), ipcMount())

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

	volumes := make([]Volume, 0, len(resolved.Volumes)+2)
	volumes = append(volumes, resolved.Volumes...)
	volumes = append(volumes, artVolume(), Volume{Name: ipcVolumeName, EmptyDir: &EmptyDirVolumeSource{}})

	// The pod's sidecars are one command sidecar that owns the mpv socket
	// and one translator per bound remote. The list is built in that
	// order, and the operator reads the translator set back off it to tell
	// whether a Player reshaped this pod.
	initContainers := make([]Container, 0, len(remotes)+1)
	initContainers = append(initContainers, commandSidecar(play, resolved.Logos, resolved.Trickplays, image, busAddress, topicBase))
	for _, remote := range remotes {
		initContainers = append(initContainers, translatorSidecar(play, image, busAddress, topicBase, remote))
	}

	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            podName(play.Metadata.Name),
			Namespace:       play.Metadata.Namespace,
			Labels:          map[string]string{playbackLabelKey: playbackLabelValue},
			OwnerReferences: []OwnerReference{playOwner(play)},
		},
		Spec: PodSpec{
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: &grace,
			ResourceClaims: []PodResourceClaim{{
				Name:              podClaimName,
				ResourceClaimName: claim.Metadata.Name,
			}},
			InitContainers: initContainers,
			Containers:     []Container{container},
			Volumes:        volumes,
		},
	}
}

// commandSidecar is the playback pod's owner of the mpv IPC socket: the
// player image in its command mode, holding no device claim. It
// subscribes to the Play's commands topic, drives mpv through the shared
// socket, and publishes the Play's status. It mounts the IPC volume,
// because it is the one container besides mpv that reaches the socket.
func commandSidecar(play *Play, logos, trickplays []string, image, busAddress, topicBase string) Container {
	interval := play.Spec.TrickplayInterval
	if interval == "" {
		interval = defaultTrickplayInterval
	}
	return Container{
		Name:    commandContainer,
		Image:   image,
		Command: []string{"/media-operator", commandMode},
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: play.Metadata.Namespace},
			{Name: playNameVariable, Value: play.Metadata.Name},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
			{Name: presentationsVariable, Value: presentationBlocks(play.Spec.Items, logos, trickplays)},
			{Name: trickplayIntervalVariable, Value: interval},
		},
		// The command sidecar reads mpv's socket on the IPC volume and writes
		// decoded art on the art volume, so it mounts both. mpv reads the art
		// back through the same art volume.
		VolumeMounts:  []VolumeMount{ipcMount(), artMount()},
		RestartPolicy: sidecarRestartPolicy,
	}
}

// presentationBlocks bakes every item's block into one JSON array in
// spec order, so playlist position i indexes item i's block. An item
// with no presentation becomes an empty object, so every position has a
// definite value the sidecar forwards as it is.
//
// Each block carries the resolved logo for its item, so the bridge reads an
// nfs logo by an in-pod path and mpv fetches an https logo by its URL.
func presentationBlocks(items []PlayItem, logos, trickplays []string) string {
	blocks := make([]json.RawMessage, len(items))
	for index, item := range items {
		if item.Presentation == nil {
			blocks[index] = json.RawMessage(emptyPresentation)
			continue
		}
		block := *item.Presentation
		if index < len(logos) {
			block.Logo = logos[index]
		}
		if index < len(trickplays) {
			block.Trickplay = trickplays[index]
		}
		encoded, err := json.Marshal(block)
		if err != nil {
			blocks[index] = json.RawMessage(emptyPresentation)
			continue
		}
		blocks[index] = encoded
	}
	array, err := json.Marshal(blocks)
	if err != nil {
		return "[]"
	}
	return string(array)
}

// translatorSidecar is one controller's translator: the player image in
// its translate mode, holding no device claim and no IPC mount. Its
// environment carries the play identity plus the three topics it works
// between: the events topic it reads, the keymap topic it reads the table
// from, and the focus topic it gates on. It builds the cycle topic it
// publishes to from the same identity.
func translatorSidecar(play *Play, image, busAddress, topicBase string, remote boundRemote) Container {
	return Container{
		Name:    translatorContainer(remote.Name),
		Image:   image,
		Command: []string{"/media-operator", translateMode},
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: play.Metadata.Namespace},
			{Name: playNameVariable, Value: play.Metadata.Name},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
			{Name: remoteNameVariable, Value: remote.Name},
			{Name: remoteEventsVariable, Value: remote.EventsTopic},
			{Name: keymapTopicVariable, Value: remote.KeymapTopic},
			{Name: focusTopicVariable, Value: remote.FocusTopic},
		},
		RestartPolicy: sidecarRestartPolicy,
	}
}

func ipcMount() VolumeMount {
	return VolumeMount{Name: ipcVolumeName, MountPath: ipcMountPath}
}

func artMount() VolumeMount {
	return VolumeMount{Name: artVolumeName, MountPath: artMountPath}
}

// artVolume is disk-backed, not memory, so a decoded logo never counts against
// the pod's memory. Its sizeLimit caps it, because the bridge writes into it
// and a runaway decode must not fill the node disk.
func artVolume() Volume {
	return Volume{Name: artVolumeName, EmptyDir: &EmptyDirVolumeSource{SizeLimit: artSizeLimit}}
}

// sameRemoteSet reports whether two pods carry the same translator
// sidecars, by the events topics they subscribe to. It reads no keymap,
// so a Keymap edit is bus state and not a shape change and recreates no
// pod. Only what the Player controls, the claim and the set of
// controllers, reshapes a running film.
func sameRemoteSet(current, desired *Pod) bool {
	return slices.Equal(podRemoteTopics(current), podRemoteTopics(desired))
}

// podRemoteTopics reads the events topics the translator sidecars
// subscribe to, in order, from each translator container's environment.
// Adding or removing a controller changes this set, and the recreate
// follows.
func podRemoteTopics(pod *Pod) []string {
	var topics []string
	for _, container := range pod.Spec.InitContainers {
		if !strings.HasPrefix(container.Name, translatorContainer("")) {
			continue
		}
		for _, variable := range container.Env {
			if variable.Name == remoteEventsVariable {
				topics = append(topics, variable.Value)
			}
		}
	}
	return topics
}
