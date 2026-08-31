package main

// Each Player that drives a screen reconciles into one standing idle
// pod, owned by the Player through an owner reference, so deleting the
// Player tears the pod down. The pod draws the clock while no Play runs.
// It holds a shared draw device on the Player's screen, so the idle pod
// and a Play's own pod draw to one screen at once. A Play's mpv starts
// with the same app-id and draws over the idle clock.
//
// The client that draws is a choice. spec.idle.image names it, and a
// Player that names none runs the image the operator reads from
// IDLE_IMAGE. Every client starts with its image's own entrypoint and
// reads the unit's state off the bus, so one contract serves the client
// this project ships and any other a household states.
//
// The clock does not return on its own when the Play ends. Weston's
// kiosk-shell reveals a lower surface only along a code path gated on a
// seat, and liken's compositor has none, so the idle surface stays
// hidden though the idle client still runs. So the pod carries a second
// container, the idle command sidecar. It states the present moment on
// the screen topic, the client maps a fresh surface, and kiosk reveals
// that one.

import (
	"strconv"
	"strings"
)

// The two containers in the standing idle pod. idleContainer runs the
// idle client and draws the clock; idleCommandContainer is the native
// sidecar that holds the timers and states each moment the client draws.
// Each name is the job the container does.
const (
	idleContainer        = "idle"
	idleCommandContainer = "idle-command"
)

// idleDrawRequest names the claim's request for the shared draw device,
// the display companion the display-operator publishes per connector. It
// delivers the compositor socket and the app-id, and sets no mode, so
// the idle clock draws to the screen without owning its resolution.
const idleDrawRequest = "draw"

// How long the idle client waits for its window before it exits and
// lets the kubelet restart the container. It is longer than a
// compositor restart takes and shorter than a person waits at a black
// screen.
const idleWindowGraceSeconds = 15

// idlePodName is a Player's standing idle pod, the Player's name plus its
// job, so a person reading either object finds the other.
func idlePodName(player string) string {
	return player + "-idle"
}

// idleClaimName is the idle pod's claim, the pod's name plus the suffix
// the playback claim uses, so a person reading either object finds the
// other.
func idleClaimName(player string) string {
	return idlePodName(player) + "-devices"
}

// playerOwner is the ownerReference that makes deleting the Player the
// whole teardown: the garbage collector deletes the idle claim and the
// idle pod the Player owns. The operator deletes either one itself only
// to replace an object that no longer matches the template.
func playerOwner(player *Player) OwnerReference {
	return OwnerReference{
		APIVersion: mediaAPIVersion,
		Kind:       "Player",
		Name:       player.Metadata.Name,
		UID:        player.Metadata.UID,
		Controller: true,
	}
}

// idlePlayerName is the friendly name the idle screen shows for the whole
// unit. It reads spec.displayName when the household set one, and falls back
// to the Player's object name, so an unnamed Player still shows a name.
func idlePlayerName(player *Player) string {
	if player.Spec.DisplayName != "" {
		return player.Spec.DisplayName
	}
	return player.Metadata.Name
}

// deviceDisplayName is the friendly name the idle screen shows for one device
// selection. It reads displayName when set. Names are user-set, so the
// fallback stays plain: the DeviceClass name says what kind of device the
// selection is when the household named none.
func deviceDisplayName(device PlayerDevice) string {
	if device.DisplayName != "" {
		return device.DisplayName
	}
	return device.Class
}

// remoteDisplayName is the friendly name the idle screen shows for one
// controller. It reads displayName when set, and falls back to Name, the
// Remote this entry references.
func remoteDisplayName(remote PlayerRemote) string {
	if remote.DisplayName != "" {
		return remote.DisplayName
	}
	return remote.Name
}

// idleComponents lists the friendly names of the unit's parts in the order
// the idle screen shows them: the display first, then each sink in spec
// order, then each remote in spec order. Each name resolves to its
// displayName, or a plain fallback when the household named none. A Player
// with no display and no parts yields an empty list, and buildIdlePod then
// sends no parts variable.
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

// idleRemoteTopics is one of the unit's controllers as the idle pod reads
// it: the topic its presses arrive on, the topic its compiled keymap
// stands on, and the topic its focus mark stands on. Keymap is empty for a
// controller with no keymap. The sidecar gates every press on the mark
// naming this Player, and it builds the cycle topic from the focus topic.
type idleRemoteTopics struct {
	Events string
	Keymap string
	Focus  string
}

// gatherIdleRemotes reads the events and keymap topics of every
// controller the Player names, in spec order. The keymap name is the
// Player entry's override, or the Remote's own. A Remote the API does
// not hold leaves its keymap blank rather than failing the idle pod,
// so that controller's presses still wake the screen and none of them
// is back.
func gatherIdleRemotes(c *Client, player *Player, base string) []idleRemoteTopics {
	namespace := player.Metadata.Namespace
	remotes := make([]idleRemoteTopics, 0, len(player.Spec.Remotes))
	for _, entry := range player.Spec.Remotes {
		topics := idleRemoteTopics{
			Events: remoteEventsTopic(base, namespace, entry.Name),
			Focus:  remoteFocusTopic(base, namespace, entry.Name),
		}
		name := entry.Keymap
		if name == "" {
			remote, err := GetRemote(c, namespace, entry.Name)
			if err == nil {
				name = remote.Spec.Keymap
			}
		}
		if name != "" {
			topics.Keymap = keymapTopic(base, name)
		}
		remotes = append(remotes, topics)
	}
	return remotes
}

// joinIdleRemotes joins one field of every remote with newlines, the
// same form the parts list travels in.
func joinIdleRemotes(remotes []idleRemoteTopics, field func(idleRemoteTopics) string) string {
	values := make([]string, len(remotes))
	for index, remote := range remotes {
		values[index] = field(remote)
	}
	return strings.Join(values, "\n")
}

// buildIdleClaim turns one Player into the standing claim for its idle
// pod: the shared draw device on the Player's own screen, and the render
// node the idle client draws through.
//
// The draw request reuses the Player's own display selector, so the idle
// client draws to the same screen a Play does. Its class is the cluster's
// display-draw class, which the operator reads from its environment,
// because the draw companion is cluster policy and not the Player's
// exclusive output class. The display-operator marks that device
// shareable, so the idle pod and a Play pod hold the screen at once. The
// render request mirrors the playback claim, because the idle client
// draws through the GPU and needs the node that renders.
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
		// The render node takes no toleration, because a GPU does not come
		// and go while the machine runs.
		claim.add(renderRequest, *player.Spec.Render, nil)
	}
	return claim
}

// buildIdlePod writes the standing idle pod: the idle client holding
// the draw and render requests and drawing the clock, beside the idle
// command sidecar that states each moment the client draws.
//
// sidecarImage carries this operator's binary alone, which the sidecar
// runs in its idle-command mode. The client's own image is idle.Image,
// which resolveIdle settles.
//
// restartPolicy is Always because the pod is a service and not a job: a
// crash restarts it, and the pod ends only when the Player is deleted.
//
// The pod carries the household timezone when the cluster set one, so
// the idle clock reads the same wall-clock zone the playback display
// reads. It carries the Player's friendly name and its parts, which the
// idle screen draws so a person reads what the unit is while no film
// runs. Those two variables are the first paint alone: the client seeds
// the identity block from them before the broker answers, and the first
// retained status replaces them, so an edit to the Player shows with no
// pod restart.
func buildIdlePod(
	player *Player, claim *ResourceClaim, sidecarImage, busAddress, topicBase, timeZone string,
	idle resolvedIdle, remotes []idleRemoteTopics,
) *Pod {
	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	// The client and the sidecar read the same three topics of the same
	// unit, so each one is built once here and set on both containers.
	statusTopic := playerStatusTopic(topicBase, namespace, name)
	screenTopic := playerScreenTopic(topicBase, namespace, name)
	// The volume topic is the speaker gate as well as the address, the
	// way wire.go states it. A Player with no sinks has no level to mean
	// anything, so neither container reads a topic here, the client draws
	// no level, and the sidecar answers no volume press.
	volumeTopic := ""
	if len(player.Spec.Sinks) > 0 {
		volumeTopic = playerVolumeTopic(topicBase, namespace, name)
	}

	// Every idle client is an image with its own entrypoint, so the
	// container names no command. A Player that states one runs that
	// image in place of the release's own, and a client that draws a
	// screen needs the same things whoever wrote it, so it keeps every
	// claim and every variable below.
	container := Container{
		Name:  idleContainer,
		Image: idle.Image,
	}
	// The clock reads TZ against the image's tz database. Set it only when
	// the household stated a zone, so an unset zone leaves the pod on UTC,
	// the way the playback pod does.
	if timeZone != "" {
		container.Env = append(container.Env, EnvVar{Name: timeZoneVariable, Value: timeZone})
	}
	// The idle client holds a window for its whole life, so it arms the
	// client's own watchdog. A compositor that restarts takes the window
	// with it, and a client that kept running windowless would leave the
	// compositor's background on the screen until a person deleted the
	// pod. The client exits instead, and the kubelet restarts the
	// container with backoff until the compositor answers again.
	container.Env = append(container.Env,
		EnvVar{Name: idleWindowGraceVariable, Value: strconv.Itoa(idleWindowGraceSeconds)})
	// The idle screen names the unit and lists its parts, so a person reads
	// what the unit is and what it plays through while no film runs. The name
	// always resolves, so the pod always carries it. The parts join with
	// newlines and travel in one variable, which the client splits. A
	// Player with no listed parts sends no parts variable, so the idle
	// screen draws the name alone.
	container.Env = append(container.Env, EnvVar{Name: idlePlayerNameVariable, Value: idlePlayerName(player)})
	if components := idleComponents(player); len(components) > 0 {
		container.Env = append(container.Env,
			EnvVar{Name: idlePlayerComponentsVariable, Value: strings.Join(components, "\n")})
	}

	// Every idle client reads the bus for itself, so the container
	// carries the address and the three topics the screen draws from:
	// the unit's retained state, its level, and the moments the sidecar
	// decides.
	container.Env = append(container.Env,
		EnvVar{Name: busAddressVariable, Value: busAddress},
		EnvVar{Name: playerStatusTopicVariable, Value: statusTopic},
		EnvVar{Name: playerScreenTopicVariable, Value: screenTopic})
	if volumeTopic != "" {
		container.Env = append(container.Env,
			EnvVar{Name: playerVolumeTopicVariable, Value: volumeTopic})
	}

	// The idle container holds every request the claim carries,
	// because the pod's one job is to draw.
	for _, request := range claimRequests(claim) {
		container.Resources.Claims = append(container.Resources.Claims,
			ContainerClaim{Name: podClaimName, Request: request})
	}

	// The idle command sidecar subscribes to the Player's commands and
	// status topics and states what it decides on the screen topic. It
	// holds no device and no volume, because it reads the bus and writes
	// the bus. Every topic is pre-built here, because the operator holds
	// the topic base and the sidecar parses no topic of its own.
	sidecar := Container{
		Name:    idleCommandContainer,
		Image:   sidecarImage,
		Command: []string{"/media-operator", idleCommandMode},
		Env: []EnvVar{
			{Name: busAddressVariable, Value: busAddress},
			// The Player's object name, the value every focus mark holds. It
			// is not the friendly name IDLE_PLAYER_NAME carries, because the
			// operator writes marks from metadata.name.
			{Name: playerNameVariable, Value: name},
			{Name: playerCommandsTopicVariable, Value: playerCommandsTopic(topicBase, namespace, name)},
			{Name: playerStatusTopicVariable, Value: statusTopic},
			{Name: playerScreenTopicVariable, Value: screenTopic},
		},
		RestartPolicy: sidecarRestartPolicy,
	}
	// The fade window travels on every pod, because the resolver
	// settles it for every Player and zero is a policy the sidecar
	// must read, not infer from an absent variable.
	sidecar.Env = append(sidecar.Env, EnvVar{
		Name:  idleFadeAfterSecondsVariable,
		Value: strconv.FormatInt(idle.FadeAfterSeconds, 10),
	})
	// The off window is set on every pod for the same reason the fade
	// window is, and the panel topic arrives whole. The off mode
	// stays with the operator, because the operator writes the
	// override.
	sidecar.Env = append(sidecar.Env,
		EnvVar{
			Name:  idleOffAfterSecondsVariable,
			Value: strconv.FormatInt(idle.OffAfterSeconds, 10),
		},
		EnvVar{
			Name:  idlePanelTopicVariable,
			Value: playerPanelTopic(topicBase, namespace, name),
		})
	if volumeTopic != "" {
		sidecar.Env = append(sidecar.Env,
			EnvVar{Name: playerVolumeTopicVariable, Value: volumeTopic})
	}
	// The three remote lists stay index-aligned, so the sidecar pairs each
	// events topic with the keymap that names its presses and the focus
	// topic that carries its mark. A remote's position in them is its
	// spec.remotes order, and the sidecar sends that index with the focus
	// pulse. A Player with no remotes sends none of the variables, and the
	// fade then runs on the timer alone.
	if len(remotes) > 0 {
		sidecar.Env = append(sidecar.Env,
			EnvVar{
				Name:  idleRemoteEventsTopicsVariable,
				Value: joinIdleRemotes(remotes, func(r idleRemoteTopics) string { return r.Events }),
			},
			EnvVar{
				Name:  idleRemoteKeymapTopicsVariable,
				Value: joinIdleRemotes(remotes, func(r idleRemoteTopics) string { return r.Keymap }),
			},
			EnvVar{
				Name:  idleRemoteFocusTopicsVariable,
				Value: joinIdleRemotes(remotes, func(r idleRemoteTopics) string { return r.Focus }),
			})
	}

	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            idlePodName(name),
			Namespace:       namespace,
			OwnerReferences: []OwnerReference{playerOwner(player)},
		},
		Spec: PodSpec{
			RestartPolicy:  "Always",
			InitContainers: []Container{sidecar},
			ResourceClaims: []PodResourceClaim{{
				Name:              podClaimName,
				ResourceClaimName: claim.Metadata.Name,
			}},
			Containers: []Container{container},
		},
	}
}

// reconcileIdle reconciles one Player into its standing idle claim and
// pod, both owned by the Player. It builds nothing when the Player drives
// no screen or when the cluster names no display-draw class, which is how
// a cluster turns the idle screen off.
//
// The pair follows the template, the way the standing remote pod does: an
// edit to the Player, or a release that changes either image, deletes
// the stale object and the next pass creates the replacement. Recreating
// the idle pod blinks the idle screen once. A release and a spec edit
// are both deliberate acts, so the pass rolls the pod with no guard.
func (o *operator) reconcileIdle(player *Player, timeZone string, defaultIdle *IdlePolicy) error {
	if player.Spec.Display == nil || o.idleDisplayClass == "" {
		return nil
	}
	claim := buildIdleClaim(player, o.idleDisplayClass)
	idle := resolveIdle(player.Spec.Idle, defaultIdle, o.idleImage)
	remotes := gatherIdleRemotes(o.client, player, o.topicBase)
	pod := buildIdlePod(player, claim, o.sidecarImage, o.busAddress, o.topicBase, timeZone, idle, remotes)
	return o.reconcileStanding(claim, pod)
}
