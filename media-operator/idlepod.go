package main

// Each Player that drives a screen reconciles into one standing idle
// pod, owned by the Player through an owner reference, so deleting the
// Player tears the pod down. The pod runs mpv in its idle mode and draws
// the clock while no Play runs. It holds a shared draw device on the
// Player's screen, so the idle pod and a Play's own pod draw to one
// screen at once. A Play's mpv starts with the same app-id and draws over
// the idle clock.
//
// The clock does not return on its own when the Play ends. Weston's
// kiosk-shell reveals a lower surface only along a code path gated on a
// seat, and liken's compositor has none, so the idle surface stays
// hidden though the idle mpv still runs. So the pod carries a second
// container, the idle command sidecar, that recreates the idle mpv's
// surface on the operator's re-present, and kiosk reveals the fresh
// surface. The two containers share the ipc volume, where mpv serves the
// socket the sidecar drives.

import "errors"

// The two containers in the standing idle pod. idleContainer runs mpv in
// its idle mode and draws the clock; idleCommandContainer is the native
// sidecar that recreates mpv's surface on a re-present. Each name is the
// job the container does.
const (
	idleContainer        = "idle"
	idleCommandContainer = "idle-command"
)

// idleDrawRequest names the claim's request for the shared draw device,
// the display companion the display-operator publishes per connector. It
// delivers the compositor socket and the app-id, and sets no mode, so
// the idle clock draws to the screen without owning its resolution.
const idleDrawRequest = "draw"

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
// idle pod the Player owns, and this operator carries no delete verb for
// them.
func playerOwner(player *Player) OwnerReference {
	return OwnerReference{
		APIVersion: mediaAPIVersion,
		Kind:       "Player",
		Name:       player.Metadata.Name,
		UID:        player.Metadata.UID,
		Controller: true,
	}
}

// buildIdleClaim turns one Player into the standing claim for its idle
// pod: the shared draw device on the Player's own screen, and the render
// node the idle mpv draws through.
//
// The draw request reuses the Player's own display selector, so the idle
// client draws to the same screen a Play does. Its class is the cluster's
// display-draw class, which the operator reads from its environment,
// because the draw companion is cluster policy and not the Player's
// exclusive output class. The display-operator marks that device
// shareable, so the idle pod and a Play pod hold the screen at once. The
// render request mirrors the playback claim, because the idle mpv runs
// --vo=gpu and needs the node that renders.
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

// buildIdlePod writes the standing idle pod: the player image in its idle
// mode holding the draw and render requests and drawing the clock, beside
// the idle command sidecar that recreates the surface on a re-present.
// restartPolicy is Always because the pod is a service and not a job: a
// crash restarts it, and the pod ends only when the Player is deleted. It
// carries the household timezone when the cluster set one, so the idle
// clock reads the same wall-clock zone the playback display reads, and it
// carries the bus address and the Player's commands topic, because the
// sidecar reads the re-present off that topic.
func buildIdlePod(player *Player, claim *ResourceClaim, image, busAddress, topicBase, timeZone string) *Pod {
	container := Container{
		Name:         idleContainer,
		Image:        image,
		Command:      []string{"/media-operator", idleMode},
		VolumeMounts: []VolumeMount{ipcMount()},
	}
	// The clock reads TZ against the image's tz database. Set it only when
	// the household stated a zone, so an unset zone leaves the pod on UTC,
	// the way the playback pod does.
	if timeZone != "" {
		container.Env = append(container.Env, EnvVar{Name: timeZoneVariable, Value: timeZone})
	}
	// The idle container holds every request the claim asks for, the draw
	// device and the render node.
	for _, request := range claimRequests(claim) {
		container.Resources.Claims = append(container.Resources.Claims,
			ContainerClaim{Name: podClaimName, Request: request})
	}

	// The idle command sidecar owns no device claim. It subscribes to the
	// Player's commands topic and drives the idle mpv over the shared ipc
	// socket, so it mounts the ipc volume and reaches nothing else.
	sidecar := Container{
		Name:    idleCommandContainer,
		Image:   image,
		Command: []string{"/media-operator", idleCommandMode},
		Env: []EnvVar{
			{Name: busAddressVariable, Value: busAddress},
			{Name: playerCommandsTopicVariable, Value: playerCommandsTopic(topicBase, player.Metadata.Namespace, player.Metadata.Name)},
		},
		VolumeMounts:  []VolumeMount{ipcMount()},
		RestartPolicy: sidecarRestartPolicy,
	}

	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            idlePodName(player.Metadata.Name),
			Namespace:       player.Metadata.Namespace,
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
			Volumes:    []Volume{{Name: ipcVolumeName, EmptyDir: &EmptyDirVolumeSource{}}},
		},
	}
}

// reconcileIdle reconciles one Player into its standing idle claim and
// pod, both owned by the Player. It builds nothing when the Player drives
// no screen or when the cluster names no display-draw class, which is how
// a cluster turns the idle screen off. It creates each object once and
// never rebuilds it, the way the standing remote pod reconciles: a Player
// edited later changes the next reconcile's object, not this run's.
//
// A 409 on either create means another pass, or another copy of this
// operator, created the object first, which is success.
func (o *operator) reconcileIdle(player *Player, timeZone string) error {
	if player.Spec.Display == nil || o.idleDisplayClass == "" {
		return nil
	}
	claim := buildIdleClaim(player, o.idleDisplayClass)
	if err := ensureClaim(o.client, claim); err != nil {
		return err
	}

	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	_, err := GetPod(o.client, namespace, idlePodName(name))
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	_, err = CreatePod(o.client, buildIdlePod(player, claim, o.image, o.busAddress, o.topicBase, timeZone))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}
