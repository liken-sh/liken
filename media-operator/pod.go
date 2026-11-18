package main

// A Play runs as one playback pod. The pod decodes media fetched from
// the network, so it has no ServiceAccount token and cannot reach the
// Kubernetes API. The command sidecar reports to the operator over the
// message bus instead, and the operator copies those reports onto the
// Play's status.

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// The names of the playback pod's containers, and podClaimName, the
// name of the claim in the pod's spec.resourceClaims that each
// container's resources.claims entries refer to.
const (
	playerContainer  = "player"
	commandContainer = "command"
	displayContainer = "display"
	podClaimName     = "devices"
)

// Every pod this operator builds has this label, and its value is the
// kind of pod: playback, idle, or remote. The operator's pod watch
// selects on these values, so it receives only its own pods. A
// display-operator Layout can select on the playback and idle values to
// give each kind of pod its own region of the screen, so neither pod
// sets its own position.
const (
	playbackLabelKey   = "media.liken.sh/component"
	playbackLabelValue = "playback"
	idleLabelValue     = "idle"
	remoteLabelValue   = "remote"
)

// The operator adds this label to a playback pod when the run reports
// that it is ending, while mpv is still drawing the film. When a
// display-operator Layout requires the label to be absent from its film
// region, the pod leaves the region as soon as it gets the label, and
// the compositor fades out the film before mpv exits. display-operator
// has no code for this key. It only applies the Layout's selector.
const (
	endingLabelKey   = "media.liken.sh/ending"
	endingLabelValue = "true"
)

// playerNodeTaint is the key of the taint that a cluster owner can put
// on a machine that drives a screen, to keep scan jobs and other
// unrelated pods off a small machine.
const playerNodeTaint = "media.liken.sh/player"

// playerNodeTolerations lets this operator's pods run on a machine
// tainted with playerNodeTaint, because they use that machine's display
// or controllers. The toleration matches only this key and only the
// NoSchedule effect, so a NoExecute taint such as node unreachable
// still evicts the pod.
func playerNodeTolerations() []Toleration {
	return []Toleration{{
		Key:      playerNodeTaint,
		Operator: "Exists",
		Effect:   "NoSchedule",
	}}
}

// An init container with restartPolicy Always is a Kubernetes native
// sidecar. The kubelet starts it before the player and restarts it
// whenever it exits, even though the pod's restartPolicy is Never. The
// kubelet also stops it when the player exits, so the pod ends with the
// film. An ordinary second container would stay down after it exited,
// and it would keep the pod running after the film ended.
const sidecarRestartPolicy = "Always"

// playbackGracePeriod is the pod's terminationGracePeriodSeconds. The
// player shim execs mpv, which exits as soon as it receives SIGTERM, and
// the pod has no state to flush. The Kubernetes default of 30 seconds
// would only delay the pod's deletion.
const playbackGracePeriod = 5

// noServiceAccountToken is the automountServiceAccountToken of every
// pod this operator builds. No such pod calls the Kubernetes API, and
// each one decodes media from the network, so none mounts a token.
func noServiceAccountToken() *bool {
	off := false
	return &off
}

func podName(play string) string {
	return play + "-playback"
}

// buildPod returns the playback pod for a Play. Its restartPolicy is
// Never, because the end of the pod is the end of the run, and a film
// that finished is not a failure to restart. The player image's
// entrypoint shim starts mpv, so the container's arguments are only the
// resolved playlist in spec order.
func buildPod(
	play *Play, claim *ResourceClaim, resolved resolution,
	image, sidecarImage, displayImage, busAddress, topicBase string,
	remotes []boundRemote, prefs resolvedPreferences, passthrough []string, playerVerbose string,
) *Pod {
	grace := int64(playbackGracePeriod)
	// Every pod mounts the IPC volume, with or without a bound remote,
	// because the command sidecar always drives mpv through its socket.
	// The copy keeps the append from writing into resolved.Mounts.
	mounts := make([]VolumeMount, 0, len(resolved.Mounts)+1)
	mounts = append(mounts, resolved.Mounts...)
	mounts = append(mounts, ipcMount())

	blocks := presentationBlocks(play.Spec.Items, resolved)
	next := nextBlock(play.Spec.Next, resolved.Next)

	container := Container{
		Name:         playerContainer,
		Image:        image,
		Args:         resolved.Items,
		VolumeMounts: mounts,
		// The shim reads each block's item type, and expands a music
		// album into one timeline before it starts mpv.
		Env: []EnvVar{{Name: presentationsVariable, Value: blocks}},
	}
	if next != "" {
		container.Env = append(container.Env, EnvVar{Name: nextVariable, Value: next})
	}
	// The shim turns the start into mpv's --start.
	if play.Spec.Start != "" {
		container.Env = append(container.Env,
			EnvVar{Name: playStartVariable, Value: play.Spec.Start})
	}
	// With no preference and no passthrough codec, mpv keeps its
	// defaults and decodes every track. A track in a passthrough codec
	// goes to the receiver still encoded (passthrough.go).
	//
	// No volume option is set, so mpv plays at its default of 100%. The
	// receiver or the sinks set the room's level (volumeengine.go).
	options := mpvPreferenceOptions(prefs)
	if len(passthrough) > 0 {
		options = append(options, spdifOption(passthrough))
	}
	if len(options) > 0 {
		container.Env = append(container.Env,
			EnvVar{Name: playerOptionsVariable, Value: strings.Join(options, "\n")})
	}
	// This variable turns on mpv's full output. The operator reads it
	// from its own environment at startup, so a change to the operator's
	// Deployment reaches only the pods created after the operator
	// restarts.
	if playerVerbose != "" {
		container.Env = append(container.Env,
			EnvVar{Name: playerVerboseVariable, Value: playerVerbose})
	}
	// The playback claim requests only the player's devices, so the
	// player container uses all of its requests.
	container.Resources.Claims = claimEntries(claim)

	volumes := make([]Volume, 0, len(resolved.Volumes)+1)
	volumes = append(volumes, resolved.Volumes...)
	volumes = append(volumes, Volume{Name: ipcVolumeName, EmptyDir: &EmptyDirVolumeSource{}})

	initContainers := []Container{
		commandSidecar(play, claim, blocks, next, sidecarImage, busAddress, topicBase, remotes, prefs),
	}
	// An audio-only Player's pod has no compositor, so a display
	// container there would fail to open its window and restart for the
	// whole run.
	if claimHasScreen(claim) {
		initContainers = append(initContainers,
			displaySidecar(play, claim, mounts, displayImage, prefs))
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
			AutomountServiceAccountToken:  noServiceAccountToken(),
			TerminationGracePeriodSeconds: &grace,
			ResourceClaims: []PodResourceClaim{{
				Name:              podClaimName,
				ResourceClaimName: claim.Metadata.Name,
			}},
			InitContainers: initContainers,
			Containers:     []Container{container},
			Volumes:        volumes,
			Tolerations:    playerNodeTolerations(),
		},
	}
}

// mpvPreferenceOptions returns the mpv flags for the resolved
// preferences. It sets a flag only for a preference that has a value,
// and it adds --subs-match-os-language=no only when it sets another
// flag.
//
// In the on and auto modes, the command sidecar picks the subtitle track
// after mpv loads each file (subtitlechoice.go). The flags decide the
// track that mpv picks first, which shows until the sidecar replaces it
// and stays when the sidecar fails. In on mode,
// --subs-fallback-forced=no stops mpv from ranking a forced track in the
// audio's language above a full track in that language. mpv still ranks
// a forced track first when it has the default flag, and only the
// sidecar corrects that. In auto mode, --subs-with-matching-audio=forced
// lets mpv pick a forced track when the audio is in a subtitle language,
// which is the track auto mode is for.
func mpvPreferenceOptions(prefs resolvedPreferences) []string {
	var options []string
	if len(prefs.AudioLanguages) > 0 {
		options = append(options, "--alang="+strings.Join(prefs.AudioLanguages, ","))
	}
	if len(prefs.SubtitleLanguages) > 0 {
		options = append(options, "--slang="+strings.Join(prefs.SubtitleLanguages, ","))
	}
	switch prefs.Subtitles {
	case subtitlesOn:
		options = append(options, "--sub-visibility=yes", "--subs-with-matching-audio=yes", "--subs-fallback-forced=no")
	case subtitlesOff:
		options = append(options, "--sid=no")
	case subtitlesAuto:
		options = append(options, "--subs-with-matching-audio=forced")
	}
	// mpv keeps a sid that the sidecar set for the next file when that
	// file has the same track layout. If the sidecar then failed on the
	// later file, it would show the earlier file's choice in place of
	// mpv's own pick. --reset-on-next-file=sid returns sid to its
	// command-line value at each file.
	if subtitlePreferencesValue(prefs) != "" {
		options = append(options, "--reset-on-next-file=sid")
	}
	if len(options) == 0 {
		return nil
	}
	return append(options, "--subs-match-os-language=no")
}

// commandSidecar returns the native sidecar that runs the sidecar image
// in its command mode. It subscribes to the Play's commands topic, drives
// mpv through the IPC socket, and publishes the Play's status on the
// bus. It mounts only the IPC volume and uses no device claim, because
// it opens no media itself.
func commandSidecar(
	play *Play, claim *ResourceClaim, blocks, next string,
	sidecarImage, busAddress, topicBase string, remotes []boundRemote,
	prefs resolvedPreferences,
) Container {
	env := []EnvVar{
		{Name: playNamespaceVariable, Value: play.Metadata.Namespace},
		{Name: playNameVariable, Value: play.Metadata.Name},
		// The sidecar stamps this UID on every report. wire.go explains
		// how the operator uses it.
		{Name: podUIDVariable, ValueFrom: &EnvVarSource{
			FieldRef: &ObjectFieldSelector{FieldPath: "metadata.uid"},
		}},
		{Name: busAddressVariable, Value: busAddress},
		{Name: topicBaseVariable, Value: topicBase},
		{Name: presentationsVariable, Value: blocks},
	}
	if next != "" {
		env = append(env, EnvVar{Name: nextVariable, Value: next})
	}
	// The sidecar picks a subtitle track only in the on and auto modes.
	// In off mode --sid=no is enough, and with no mode mpv picks the
	// track itself.
	if choice := subtitlePreferencesValue(prefs); choice != "" {
		env = append(env, EnvVar{Name: subtitlePreferencesVariable, Value: choice})
	}
	// The sidecar acts on a controller's button press only while that
	// controller's focus topic names this Player, so it needs the
	// Player's name. The events and focus topic lists have one line per
	// controller, in the same order.
	env = append(env, EnvVar{Name: playerNameVariable, Value: playerName(play)})
	if len(remotes) > 0 {
		focuses := make([]string, len(remotes))
		for index, remote := range remotes {
			focuses[index] = remote.FocusTopic
		}
		env = append(env,
			EnvVar{
				Name:  remoteEventsTopicsVariable,
				Value: strings.Join(remoteEventsTopics(remotes), "\n"),
			},
			EnvVar{Name: remoteFocusTopicsVariable, Value: strings.Join(focuses, "\n")})
	}
	// A claim has a sink request only for a Player that lists sinks.
	// Without the volume topic the sidecar subscribes to no volume and
	// none is drawn.
	if claimHasSink(claim) {
		env = append(env, EnvVar{
			Name:  playerVolumeTopicVariable,
			Value: playerVolumeTopic(topicBase, play.Metadata.Namespace, playerName(play)),
		})
	}
	env = append(env,
		EnvVar{Name: metricsAddressVariable, Value: "0.0.0.0:" + strconv.Itoa(commandMetricsPort)})
	// The sidecar reports its image's tag as its version, so the metrics
	// show the release that this pod runs.
	if _, tag, tagged := splitReference(sidecarImage); tagged {
		env = append(env, EnvVar{Name: mediaVersionVariable, Value: tag})
	}
	return Container{
		Name:          commandContainer,
		Image:         sidecarImage,
		Command:       []string{podBinary, commandMode},
		Env:           env,
		Ports:         []ContainerPort{{Name: metricsPortName, ContainerPort: commandMetricsPort}},
		VolumeMounts:  []VolumeMount{ipcMount()},
		RestartPolicy: sidecarRestartPolicy,
	}
}

// displaySidecar returns the native sidecar that draws the on-screen
// display on its own surface above mpv's. It uses every request of the
// player container, because it draws on the same screen with the same
// render device. It mounts the player's media mounts, read-only as the
// resolution created them, because it decodes its own images and a
// logo, a cover, or a trickplay sheet can be in the film's folder.
func displaySidecar(
	play *Play, claim *ResourceClaim, mounts []VolumeMount,
	displayImage string, prefs resolvedPreferences,
) Container {
	interval := play.Spec.TrickplayInterval
	if interval == "" {
		interval = defaultTrickplayInterval
	}
	container := Container{
		Name:          displayContainer,
		Image:         displayImage,
		Env:           []EnvVar{{Name: trickplayIntervalVariable, Value: interval}},
		VolumeMounts:  mounts,
		RestartPolicy: sidecarRestartPolicy,
	}
	// The display clock resolves TZ with the image's tz database, and
	// shows UTC when TZ is unset.
	if prefs.TimeZone != "" {
		container.Env = append(container.Env,
			EnvVar{Name: timeZoneVariable, Value: prefs.TimeZone})
	}
	container.Resources.Claims = claimEntries(claim)
	return container
}

// subtitlePreferencesValue returns the JSON that the sidecar's subtitle
// rule reads, or an empty string in a mode where the sidecar picks no
// track.
func subtitlePreferencesValue(prefs resolvedPreferences) string {
	if prefs.Subtitles != subtitlesOn && prefs.Subtitles != subtitlesAuto {
		return ""
	}
	// The value has only strings and string slices, which always
	// marshal.
	encoded, _ := json.Marshal(subtitlePreferences{
		Subtitles:         prefs.Subtitles,
		SDH:               prefs.SDH,
		AudioLanguages:    prefs.AudioLanguages,
		SubtitleLanguages: prefs.SubtitleLanguages,
	})
	return string(encoded)
}

// presentationBlocks returns a JSON array of every item's presentation
// block in spec order, so playlist position i has its block at index i.
// An item with no presentation, or whose block fails to marshal, gets
// an empty object. Each block has the item's resolved paths in place of
// the spec's, so the display reads an nfs or claim file from a path
// inside the pod and fetches an https file by its URL.
func presentationBlocks(items []PlayItem, resolved resolution) string {
	blocks := make([]json.RawMessage, len(items))
	for index, item := range items {
		if item.Presentation == nil {
			blocks[index] = json.RawMessage(emptyPresentation)
			continue
		}
		block := *item.Presentation
		if index < len(resolved.Logos) {
			block.Logo = resolved.Logos[index]
		}
		if index < len(resolved.Trickplays) {
			block.Trickplay = resolved.Trickplays[index]
		}
		if index < len(resolved.Arts) {
			block.Art = resolved.Arts[index]
		}
		if index < len(resolved.Appearances) {
			block.Appearances = resolved.Appearances[index]
		}
		if index < len(resolved.Contributors) {
			block.Contributors = resolved.Contributors[index]
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

// nextBlock returns the Play's next block as JSON with the art's
// resolved path, or an empty string when the Play has no next block or
// it does not marshal.
func nextBlock(next *PlayNext, art string) string {
	if next == nil {
		return ""
	}
	block := *next
	block.Art = art
	encoded, err := json.Marshal(block)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func ipcMount() VolumeMount {
	return VolumeMount{Name: ipcVolumeName, MountPath: ipcMountPath}
}

// sameRemoteSet reports whether a running pod's command sidecar
// subscribes to the events topics of the Play's bound remotes. It does
// not compare keymaps, because the sidecar receives a Keymap edit over
// the bus and needs no new pod.
func sameRemoteSet(current *Pod, remotes []boundRemote) bool {
	return slices.Equal(podRemoteTopics(current), remoteEventsTopics(remotes))
}

// remoteEventsTopics returns the remotes' events topics in order.
// buildPod and sameRemoteSet both use it, so the topics they compare
// cannot differ in order or form.
func remoteEventsTopics(remotes []boundRemote) []string {
	if len(remotes) == 0 {
		return nil
	}
	topics := make([]string, len(remotes))
	for index, remote := range remotes {
		topics[index] = remote.EventsTopic
	}
	return topics
}

func podRemoteTopics(pod *Pod) []string {
	for _, container := range pod.Spec.InitContainers {
		if container.Name != commandContainer {
			continue
		}
		for _, variable := range container.Env {
			if variable.Name == remoteEventsTopicsVariable {
				return splitTopicLines(variable.Value)
			}
		}
	}
	return nil
}
