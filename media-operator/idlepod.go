package main

// Each Player that drives a screen reconciles into one standing idle
// pod, owned by the Player through an owner reference, so deleting the
// Player tears the pod down. The pod runs mpv in its idle mode and draws
// the clock while no Play runs. It holds a shared draw device on the
// Player's screen, so the idle pod and a Play's own pod draw to one
// screen at once. A Play's mpv starts with the same app-id and draws
// over the idle clock, and the clock shows again when the Play ends.

import "errors"

// The one container in the standing idle pod runs mpv in its idle mode,
// so its name is the job it does.
const idleContainer = "idle"

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
// mode, holding the draw and render requests, drawing the clock.
// restartPolicy is Always because the pod is a service and not a job: a
// crash restarts it, and the pod ends only when the Player is deleted. It
// carries the household timezone when the cluster set one, so the idle
// clock reads the same wall-clock zone the playback display reads, and no
// bus environment, because nothing drives the idle client this milestone.
func buildIdlePod(player *Player, claim *ResourceClaim, image, timeZone string) *Pod {
	container := Container{
		Name:    idleContainer,
		Image:   image,
		Command: []string{"/media-operator", idleMode},
	}
	// The clock reads TZ against the image's tz database. Set it only when
	// the household stated a zone, so an unset zone leaves the pod on UTC,
	// the way the playback pod does.
	if timeZone != "" {
		container.Env = append(container.Env, EnvVar{Name: timeZoneVariable, Value: timeZone})
	}
	// The one container holds every request the claim asks for, the draw
	// device and the render node.
	for _, request := range claimRequests(claim) {
		container.Resources.Claims = append(container.Resources.Claims,
			ContainerClaim{Name: podClaimName, Request: request})
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
			RestartPolicy: "Always",
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
	_, err = CreatePod(o.client, buildIdlePod(player, claim, o.image, timeZone))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}
