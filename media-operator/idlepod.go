package main

// A Player with a display gets a ResourceClaim for the shared draw
// device on its screen and, when this operator draws the idle screen, a
// pod that runs the idle client. The client draws a clock while no Play
// runs. The draw device is shareable, so the idle pod and a Play's pod
// draw to the same screen at the same time, and the Play's mpv window
// covers the clock until it closes.
//
// spec.idle.controller selects the operator that draws the idle screen.
// When it is this operator, spec.idle.image selects the client image,
// and IDLE_IMAGE is the default. Every client starts with its image's
// entrypoint and reads the Player's state from the message bus, so a
// client from another project gets the same pod as the built-in one.

import (
	"strconv"
	"strings"
)

const idleContainer = "idle"

// idleDrawRequest requests the draw device that display-operator
// publishes beside each connector. It gives the pod the compositor
// socket and sets no display mode, so the idle clock does not change the
// screen's resolution.
const idleDrawRequest = "draw"

// idleWindowGraceSeconds is how long the idle client waits for a window
// before it exits. It is longer than a compositor restart and shorter
// than the time a person would wait at a black screen.
const idleWindowGraceSeconds = 15

// The container port has a name so that a PodMonitor selects it by name
// and does not repeat the number.
const (
	idleMetricsPort = 9200
	metricsPortName = "metrics"
)

func idlePodName(player string) string {
	return player + "-idle"
}

// retiredIdleCommandPodSuffix names a pod that releases before
// 2026.09.02-002 ran beside the idle pod to apply the idle timers and
// the remote presses. reconcileIdle deletes it, because it would change
// the volume at the same time as the idle client. Remove the suffix and
// the delete when no cluster runs an older release.
const retiredIdleCommandPodSuffix = "-command"

func idleClaimName(player string) string {
	return idlePodName(player) + "-devices"
}

// playerOwner makes the Player the controller of its idle claim and idle
// pod, so the garbage collector deletes both with the Player.
func playerOwner(player *Player) OwnerReference {
	return OwnerReference{
		APIVersion: mediaAPIVersion,
		Kind:       "Player",
		Name:       player.Metadata.Name,
		UID:        player.Metadata.UID,
		Controller: true,
	}
}

// idlePlayerName returns the name that the idle screen shows for the
// Player.
func idlePlayerName(player *Player) string {
	if player.Spec.DisplayName != "" {
		return player.Spec.DisplayName
	}
	return player.Metadata.Name
}

func deviceDisplayName(device PlayerDevice) string {
	if device.DisplayName != "" {
		return device.DisplayName
	}
	return device.Class
}

func remoteDisplayName(remote PlayerRemote) string {
	if remote.DisplayName != "" {
		return remote.DisplayName
	}
	return remote.Name
}

// idleComponents returns the device names that the idle screen lists.
func idleComponents(player *Player) []string {
	var components []string
	if player.Spec.Display != nil {
		components = append(components, deviceDisplayName(*player.Spec.Display))
	}
	for _, sink := range player.Spec.Sinks {
		components = append(components, deviceDisplayName(sink))
	}
	for _, remote := range player.Spec.Remotes {
		components = append(components, remoteDisplayName(remote))
	}
	return components
}

// idleRemoteTopics are the bus topics of one Remote. Events receives its
// button presses. Focus receives the name of the Player that the remote
// controls, and the idle client acts on a press only when that name is
// this Player's. The client also derives the cycle topic from Focus.
type idleRemoteTopics struct {
	Events string
	Focus  string
}

// gatherIdleRemotes returns the topics of each Remote in spec.remotes
// order. The client sends a remote's index in this list with its focus
// event, and the Player's status lists the remotes in the same order.
// gatherRemotes for the playback pod sorts by name and reads each
// Remote's keymap, so it cannot serve here.
func gatherIdleRemotes(player *Player, base string) []idleRemoteTopics {
	namespace := player.Metadata.Namespace
	remotes := make([]idleRemoteTopics, 0, len(player.Spec.Remotes))
	for _, entry := range player.Spec.Remotes {
		remotes = append(remotes, idleRemoteTopics{
			Events: remoteEventsTopic(base, namespace, entry.Name),
			Focus:  remoteFocusTopic(base, namespace, entry.Name),
		})
	}
	return remotes
}

func joinIdleRemotes(remotes []idleRemoteTopics, field func(idleRemoteTopics) string) string {
	values := make([]string, len(remotes))
	for index, remote := range remotes {
		values[index] = field(remote)
	}
	return strings.Join(values, "\n")
}

// buildIdleClaim requests the draw device on the Player's screen and,
// when the Player sets spec.render, the same render node as the playback
// claim. The draw request copies the Player's display selector, so it
// selects the same screen as a Play. It uses displayClass, not the
// Player's display class, because a Play's claim holds that device
// exclusively and the draw device is shareable.
func buildIdleClaim(player *Player, displayClass string) *ResourceClaim {
	claim := &ResourceClaim{
		APIVersion: claimAPIVersion,
		Kind:       "ResourceClaim",
		Metadata: ObjectMeta{
			Name:            idleClaimName(player.Metadata.Name),
			Namespace:       player.Metadata.Namespace,
			OwnerReferences: []OwnerReference{playerOwner(player)},
		},
	}
	claim.add(idleDrawRequest, PlayerDevice{Class: displayClass, Selector: player.Spec.Display.Selector}, nil)
	if player.Spec.Render != nil {
		// The render request has no toleration, because a GPU does not
		// appear or disappear while the machine runs.
		claim.add(renderRequest, *player.Spec.Render, nil)
	}
	return claim
}

// buildIdlePod returns the pod that runs the idle client. The client
// applies the fade and off timers, the remote presses, the volume
// indicator, and the panel requests itself, through the media-screen
// crate, so the environment holds every duration, name, and bus topic
// that the crate reads. restartPolicy is Always because the pod runs
// for as long as the Player exists.
func buildIdlePod(
	player *Player, claim *ResourceClaim, busAddress, topicBase, timeZone string,
	idle resolvedIdle, remotes []idleRemoteTopics,
) *Pod {
	namespace, name := player.Metadata.Namespace, player.Metadata.Name

	// The container sets no command, so a Player that sets its own image
	// changes nothing else in the pod.
	container := Container{
		Name:  idleContainer,
		Image: idle.Image,
	}
	// Without a zone, the clock shows UTC, as the playback pod does.
	if timeZone != "" {
		container.Env = append(container.Env, EnvVar{Name: timeZoneVariable, Value: timeZone})
	}
	// A compositor restart closes the client's window, and the client
	// cannot reconnect. Without the watchdog it would keep running with
	// no window and the screen would show only the compositor's
	// background. With it, the client exits after the grace, and the
	// container's first process starts it again when the new compositor
	// accepts a connection. The container exits only when the client
	// fails in another way, or when no new compositor accepts a
	// connection within COMPOSITOR_RETURN_LIMIT in
	// idle/src/harness/respawn.rs.
	container.Env = append(container.Env,
		EnvVar{Name: idleWindowGraceVariable, Value: strconv.Itoa(idleWindowGraceSeconds)})
	// The client draws these names only until the first retained status
	// arrives from the bus, so a Player edit shows without a restart.
	container.Env = append(container.Env, EnvVar{Name: idlePlayerNameVariable, Value: idlePlayerName(player)})
	if components := idleComponents(player); len(components) > 0 {
		container.Env = append(container.Env,
			EnvVar{Name: idlePlayerComponentsVariable, Value: strings.Join(components, "\n")})
	}

	// The client reports the image tag as its version. An image named by
	// digest has no tag, so the client reports no version.
	container.Env = append(container.Env,
		EnvVar{Name: metricsAddressVariable, Value: "0.0.0.0:" + strconv.Itoa(idleMetricsPort)})
	if _, tag, tagged := splitReference(idle.Image); tagged {
		container.Env = append(container.Env, EnvVar{Name: mediaVersionVariable, Value: tag})
	}
	container.Ports = []ContainerPort{{Name: metricsPortName, ContainerPort: idleMetricsPort}}

	container.Env = append(container.Env,
		EnvVar{Name: busAddressVariable, Value: busAddress},
		EnvVar{Name: playerStatusTopicVariable, Value: playerStatusTopic(topicBase, namespace, name)})
	if topic := idleVolumeTopic(player, topicBase); topic != "" {
		container.Env = append(container.Env,
			EnvVar{Name: playerVolumeTopicVariable, Value: topic})
	}
	// Focus messages name the Player by metadata.name, which can differ
	// from the display name in IDLE_PLAYER_NAME.
	container.Env = append(container.Env, EnvVar{Name: playerNameVariable, Value: name})
	// On the commands topic, the playback pod requests the work that
	// follows a run, and a client from another operator responds. On the
	// panel topic, the client publishes the panel state it requests, and
	// the operator writes it to the screen's Display. The operator passes
	// full topic names because the client does not know the topic base.
	container.Env = append(container.Env,
		EnvVar{Name: playerCommandsTopicVariable, Value: playerCommandsTopic(topicBase, namespace, name)},
		EnvVar{Name: playerPanelTopicVariable, Value: playerPanelTopic(topicBase, namespace, name)})
	// The power topic receives this operator's wake and sleep requests
	// for the screen, whether or not the Player has a Receiver.
	container.Env = append(container.Env,
		EnvVar{Name: playerPowerTopicVariable, Value: playerPowerTopic(topicBase, namespace, name)})
	// The client pairs the two lists by line. A Player with no remotes
	// gets neither variable.
	if len(remotes) > 0 {
		container.Env = append(container.Env,
			EnvVar{
				Name:  remoteEventsTopicsVariable,
				Value: joinIdleRemotes(remotes, func(r idleRemoteTopics) string { return r.Events }),
			},
			EnvVar{
				Name:  remoteFocusTopicsVariable,
				Value: joinIdleRemotes(remotes, func(r idleRemoteTopics) string { return r.Focus }),
			})
	}
	// Both durations are always set, because zero is a setting. The off
	// mode is not passed, because the operator applies it itself.
	container.Env = append(container.Env,
		EnvVar{
			Name:  idleFadeAfterSecondsVariable,
			Value: strconv.FormatInt(idle.FadeAfterSeconds, 10),
		},
		EnvVar{
			Name:  idleOffAfterSecondsVariable,
			Value: strconv.FormatInt(idle.OffAfterSeconds, 10),
		})

	container.Resources.Claims = claimEntries(claim)

	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:      idlePodName(name),
			Namespace: namespace,
			// A display-operator Layout selects the idle pod for a region
			// by this label.
			Labels:          map[string]string{playbackLabelKey: idleLabelValue},
			OwnerReferences: []OwnerReference{playerOwner(player)},
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

// idleVolumeTopic returns the Player's volume topic, or "" when the
// Player has no sinks. On "" the idle client draws no volume indicator
// and ignores volume presses. The Player's status publishes the same
// value, so a client from another operator reads the same signal.
func idleVolumeTopic(player *Player, topicBase string) string {
	if len(player.Spec.Sinks) == 0 {
		return ""
	}
	return playerVolumeTopic(topicBase, player.Metadata.Namespace, player.Metadata.Name)
}

// idleClaimFor returns the idle claim, or nil when the Player has no
// display or the operator has no display-draw class. Leaving the class
// unset turns the idle screen off for the cluster. reconcileIdle and the
// status both call it, so they agree on whether the idle screen exists.
func (o *operator) idleClaimFor(player *Player) *ResourceClaim {
	if player.Spec.Display == nil || o.idleDisplayClass == "" {
		return nil
	}
	return buildIdleClaim(player, o.idleDisplayClass)
}

// reconcileIdle creates, replaces, or deletes the idle claim and pod.
// With the controller media.liken.sh/idle-screen, this operator creates
// both. With media.liken.sh/none, neither exists. With any other
// controller, this operator creates only the claim, and that controller
// creates the pod from the settings in status.idle.
//
// When a Player edit or a new image changes a template, the pass deletes
// the old object and the next pass creates the new one. Replacing the
// idle pod blanks the screen once, which is acceptable for a change that
// a person made.
func (o *operator) reconcileIdle(player *Player, timeZone string, defaultIdle *IdlePolicy) error {
	claim := o.idleClaimFor(player)
	if claim == nil {
		return nil
	}
	idle := resolveIdle(player.Spec.Idle, defaultIdle, o.idleImage)
	namespace, name := player.Metadata.Namespace, player.Metadata.Name

	screen := persistentPair{
		subject:   "player " + namespace + "/" + name,
		absent:    "the idle screen went to controller " + idle.Controller,
		namespace: namespace,
		claimName: claim.Metadata.Name,
		podName:   idlePodName(name),
	}
	if idle.Controller != idleControllerNone {
		screen.claim = claim
		if idle.Controller == idleControllerOwn {
			remotes := gatherIdleRemotes(player, o.topicBase)
			screen.pod = o.resources.apply(buildIdlePod(player, claim, o.busAddress, o.topicBase, timeZone, idle, remotes))
		}
	}
	if err := o.reconcilePersistent(screen, claimRead{}); err != nil {
		return err
	}
	// The retired command pod has no component label, so the pods watch
	// does not see it, and reconcilePersistent reads it from the API server
	// once per Player.
	return o.reconcilePersistent(persistentPair{
		subject:   "player " + namespace + "/" + name,
		absent:    "the idle screen runs in one pod now, and an older release made this one",
		namespace: namespace,
		podName:   idlePodName(name) + retiredIdleCommandPodSuffix,
	}, claimRead{})
}
