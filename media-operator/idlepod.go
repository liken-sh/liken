package main

// Each Player that drives a screen reconciles into a standing
// claim on the screen and, where this operator draws the idle screen
// itself, one standing idle client pod. Both are owned by the Player
// through an owner reference, so deleting the Player tears them down.
// The pod draws the clock while no Play runs. The claim holds a shared
// draw device on the Player's screen, so the idle pod and a Play's own
// pod draw to one screen at once. A Play's mpv starts with the same
// app-id and draws over the idle clock.
//
// Who draws is a choice. spec.idle.controller names the operator that
// draws, and under this operator's own name spec.idle.image names the
// client. A Player that names no image runs the image the operator
// reads from IDLE_IMAGE. Every client starts with its image's own
// entrypoint and reads the unit's state off the bus, so one contract
// serves the client this project ships and any other a household
// states.

import (
	"strconv"
	"strings"
)

// The one container of a standing idle pod. It runs the idle client and
// draws the clock, and the name is the job the container does.
const idleContainer = "idle"

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

// retiredIdleCommandPodSuffix names the pod that stood beside the idle
// pod in releases before 2026.09.02-002. It held the timers, the focus
// gate, and the press gate, which the client holds now, so nothing
// builds it. The reconcile deletes one that still stands, because a
// live one would step the volume beside the client. This constant and
// the delete that reads it can go once no cluster runs an older
// release.
const retiredIdleCommandPodSuffix = "-command"

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

// idleRemoteTopics is one of the unit's controllers as the idle
// client reads it: the topic its presses arrive on and the topic
// its focus mark stands on. The client gates every press on the mark
// naming this Player, and it builds the cycle topic from the focus
// topic.
type idleRemoteTopics struct {
	Events string
	Focus  string
}

// gatherIdleRemotes builds the events and focus topics of every
// controller the Player names, in spec.remotes order. That position is
// the index a focus moment carries, and it is the order the status
// topic lists the parts in, so the two agree. The playback pod's own
// gather sorts by name, and is a separate function for that reason.
// This one reads no API object: the key table is the standing pod's
// business.
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

// buildIdlePod writes the standing idle client pod: one container
// that holds the draw and render requests and draws the clock. The
// client's image is idle.Image, which resolveIdle settles, and the pod
// runs only where the resolved controller is this operator's own.
//
// The client holds the fade and off windows, the focus gate, the shade,
// the volume step, the cycle request, and the panel desire in its own
// process, through the media-screen crate. So the container carries the
// whole contract that crate reads: the two windows, the unit's name,
// the bus address, and every topic the client subscribes to or
// publishes on.
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
	player *Player, claim *ResourceClaim, busAddress, topicBase, timeZone string,
	idle resolvedIdle, remotes []idleRemoteTopics,
) *Pod {
	namespace, name := player.Metadata.Namespace, player.Metadata.Name

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
	// carries the address and the unit's retained state topic.
	container.Env = append(container.Env,
		EnvVar{Name: busAddressVariable, Value: busAddress},
		EnvVar{Name: playerStatusTopicVariable, Value: playerStatusTopic(topicBase, namespace, name)})
	if topic := idleVolumeTopic(player, topicBase); topic != "" {
		container.Env = append(container.Env,
			EnvVar{Name: playerVolumeTopicVariable, Value: topic})
	}
	// The Player's object name, the value every focus mark holds. It
	// is not the friendly name IDLE_PLAYER_NAME carries, because the
	// operator writes marks from metadata.name.
	container.Env = append(container.Env, EnvVar{Name: playerNameVariable, Value: name})
	// The commands topic carries the operator's re-present, which the
	// client answers with a fresh surface. The panel topic is where the
	// client states its desire for the panel, and the operator reads
	// that desire and overrides the screen's Display. Both arrive
	// whole, because the operator holds the topic base and the client
	// parses no topic.
	container.Env = append(container.Env,
		EnvVar{Name: playerCommandsTopicVariable, Value: playerCommandsTopic(topicBase, namespace, name)},
		EnvVar{Name: playerPanelTopicVariable, Value: playerPanelTopic(topicBase, namespace, name)})
	// The two remote lists stay index-aligned, so the client pairs
	// each events topic with the focus topic that carries its mark. A
	// remote's position in them is its spec.remotes order, and the
	// client sends that index with the focus moment. A Player with no
	// remotes sends neither variable, and the fade then runs on the
	// timer alone.
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
	// Both windows travel on every client, because the resolver settles
	// them for every Player and zero is a policy the client must read,
	// not infer from an absent variable. The off mode stays with the
	// operator, because the operator writes the override.
	container.Env = append(container.Env,
		EnvVar{
			Name:  idleFadeAfterSecondsVariable,
			Value: strconv.FormatInt(idle.FadeAfterSeconds, 10),
		},
		EnvVar{
			Name:  idleOffAfterSecondsVariable,
			Value: strconv.FormatInt(idle.OffAfterSeconds, 10),
		})

	// The idle container holds every request the claim carries,
	// because the pod's one job is to draw.
	for _, request := range claimRequests(claim) {
		container.Resources.Claims = append(container.Resources.Claims,
			ContainerClaim{Name: podClaimName, Request: request})
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
			RestartPolicy: "Always",
			ResourceClaims: []PodResourceClaim{{
				Name:              podClaimName,
				ResourceClaimName: claim.Metadata.Name,
			}},
			Containers: []Container{container},
		},
	}
}

// idleVolumeTopic is the unit's level topic, or empty for a unit with
// no speakers. The volume topic is the speaker gate as well as the
// address, the way wire.go states it. A Player with no sinks has no
// level to mean anything, so the idle client reads no topic here: it
// draws no level and answers no volume press. The same empty string
// goes on the status, so a delegate's client reads the same gate.
func idleVolumeTopic(player *Player, topicBase string) string {
	if len(player.Spec.Sinks) == 0 {
		return ""
	}
	return playerVolumeTopic(topicBase, player.Metadata.Namespace, player.Metadata.Name)
}

// idleClaimFor is the standing claim on one Player's screen, or nil when
// the Player drives no screen or the cluster names no display-draw class,
// which is how a cluster turns the idle screen off. Every part of the
// pass that reads the idle claim reads it here, so the reconcile and the
// status answer the same question once.
func (o *operator) idleClaimFor(player *Player) *ResourceClaim {
	if player.Spec.Display == nil || o.idleDisplayClass == "" {
		return nil
	}
	return buildIdleClaim(player, o.idleDisplayClass)
}

// reconcileIdle reconciles one Player into the standing objects
// its resolved controller calls for, all of them owned by the Player. It
// builds nothing when the Player drives no screen or when the cluster
// names no display-draw class.
//
// The controller decides which objects stand.
// media.liken.sh/idle-screen is this operator's own, and it stands the
// claim and the idle client pod. Under media.liken.sh/none nothing
// stands. Every other name is a delegate: the claim stands, and the
// delegate's own operator builds the pod that draws, wired from
// status.idle.
//
// The objects follow the template, the way the standing remote pod does:
// an edit to the Player, or a release that changes either image, deletes
// the stale object and the next pass creates the replacement. Recreating
// the idle pod blinks the idle screen once. A release and a spec edit
// are both deliberate acts, so the pass rolls the pod with no guard.
func (o *operator) reconcileIdle(player *Player, timeZone string, defaultIdle *IdlePolicy) error {
	claim := o.idleClaimFor(player)
	if claim == nil {
		return nil
	}
	idle := resolveIdle(player.Spec.Idle, defaultIdle, o.idleImage)
	namespace, name := player.Metadata.Namespace, player.Metadata.Name

	screen := standing{namespace: namespace, claimName: claim.Metadata.Name, podName: idlePodName(name)}
	if idle.Controller != idleControllerNone {
		screen.claim = claim
		if idle.Controller == idleControllerOwn {
			remotes := gatherIdleRemotes(player, o.topicBase)
			screen.pod = buildIdlePod(player, claim, o.busAddress, o.topicBase, timeZone, idle, remotes)
		}
	}
	if err := o.reconcileStanding(screen); err != nil {
		return err
	}
	// The pod an older release stood. This pass wants none under that
	// name, so a live one is deleted and an absent one costs the one
	// GET the standing rule makes. See retiredIdleCommandPodSuffix.
	return o.reconcileStanding(standing{
		namespace: namespace,
		podName:   idlePodName(name) + retiredIdleCommandPodSuffix,
	})
}
