package main

// This file writes the manifest the design says nobody should have
// to write again: one claim that holds the whole player, request by
// request, with the parameter blocks a person wrote by hand in the
// days of the demo.

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// The request names are the roles, and they are the names the
// container's resources.claims refer to. One claim with named
// requests beats one claim per device because the set allocates as a
// unit: the scheduler finds one machine that satisfies every request,
// or the pod parks Pending with one story to read.
//
// A remote's request is named for the Remote it claims, behind a
// prefix no player role starts with. The standing remote pod's claim
// uses the name, because that pod holds the controller now.
const (
	screenRequest       = "screen"
	renderRequest       = "render"
	audioRequestPrefix  = "audio"
	remoteRequestPrefix = "remote-"
)

// remoteDisconnectedTaint is the taint the hardware operator sets when a
// controller disconnects. The standing remote reader pod tolerates it
// forever, because a controller sleeps whenever a person puts it down and
// the reader must wait for it. The playback claim tolerates no disconnect
// taint at all, so a display or a speaker that leaves evicts the playback
// pod, and the operator recreates it at the film's place.
const (
	remoteDisconnectedTaint = "bluetooth.liken.sh/disconnected"
)

func remoteRequestName(remote string) string {
	return remoteRequestPrefix + remote
}

// tolerateForever has no tolerationSeconds, so the toleration never
// expires, and no effect, so it matches every effect the taint
// carries. A controller sleeps whenever a person puts it down. The
// missing effect is what lets the claim allocate while it sleeps,
// so a film starts without it, and the missing seconds are what
// keep a sleeping controller from evicting the film later.
func tolerateForever(taint string) []DeviceToleration {
	return []DeviceToleration{{
		Key:      taint,
		Operator: "Exists",
	}}
}

// claimName is the Play's name plus its job, so a person reading
// either object finds the other.
func claimName(play string) string {
	return play + "-devices"
}

// playOwner is the ownerReference that makes deleting the Play the
// whole teardown: the garbage collector deletes the claim and the pod
// the Play owns. The operator deletes a pod or a claim itself only to
// recreate a running Play's pod after its Player reshaped it.
func playOwner(play *Play) OwnerReference {
	return OwnerReference{
		APIVersion: mediaAPIVersion,
		Kind:       "Play",
		Name:       play.Metadata.Name,
		UID:        play.Metadata.UID,
		Controller: true,
	}
}

// buildClaim turns one Player into one claim, requests in role order:
// screen, sinks, render. A Player without a display or without a render
// node yields a claim without that request, and the player program
// plays without one, which is what an audio-only unit is.
//
// The playback claim holds the player's own devices and no controller.
// A Remote reconciles into its own standing pod, which holds the
// controller's claim, so a controller on one machine and a display on
// another still pair.
func buildClaim(play *Play, player *Player) *ResourceClaim {
	claim := &ResourceClaim{
		APIVersion: claimAPIVersion,
		Kind:       "ResourceClaim",
		Metadata: ObjectMeta{
			Name:            claimName(play.Metadata.Name),
			Namespace:       play.Metadata.Namespace,
			OwnerReferences: []OwnerReference{playOwner(play)},
		},
	}
	if player.Spec.Display != nil {
		claim.add(screenRequest, *player.Spec.Display, nil)
	}
	for index, sink := range player.Spec.Sinks {
		claim.add(audioRequestPrefix+strconv.Itoa(index), sink, nil)
	}
	if player.Spec.Render != nil {
		// The render node takes no toleration, because a GPU does not
		// come and go while the machine runs.
		claim.add(renderRequest, *player.Spec.Render, nil)
	}
	return claim
}

// add turns one device into a request and, when the Player states
// parameters for it, one opaque config block aimed at that request
// alone, so a codec never lands on a screen.
func (c *ResourceClaim) add(name string, device PlayerDevice, tolerations []DeviceToleration) {
	request := DeviceRequest{
		Name: name,
		Exactly: &ExactDeviceRequest{
			DeviceClassName: device.Class,
			// ExactCount with a count of one, because a role is one
			// piece of equipment.
			AllocationMode: "ExactCount",
			Count:          1,
		},
	}
	// An empty selector omits the list rather than sending an empty
	// expression, because a class that already names one kind of
	// device needs no CEL, and the API server refuses an expression
	// with nothing in it.
	if device.Selector != "" {
		request.Exactly.Selectors = []DeviceSelector{{CEL: &CELDeviceSelector{Expression: device.Selector}}}
	}
	request.Exactly.Tolerations = tolerations
	c.Spec.Devices.Requests = append(c.Spec.Devices.Requests, request)

	if device.Parameters == nil {
		return
	}
	// The parameters travel as the Player wrote them. This operator
	// never reads a driver's vocabulary; the driver validates its
	// own block at the allocation.
	values := device.Parameters.Values
	if len(values) == 0 {
		values = json.RawMessage("{}")
	}
	c.Spec.Devices.Config = append(c.Spec.Devices.Config, DeviceClaimConfiguration{
		Requests: []string{name},
		Opaque: &OpaqueDeviceConfiguration{
			Driver:     device.Parameters.Driver,
			Parameters: values,
		},
	})
}

// claimRequests lists the request names in claim order, which is what
// the container's resources.claims list repeats. The playback claim
// holds the player's roles alone, so every request is the player
// container's.
func claimRequests(claim *ResourceClaim) []string {
	names := make([]string, 0, len(claim.Spec.Devices.Requests))
	for _, request := range claim.Spec.Devices.Requests {
		names = append(names, request.Name)
	}
	return names
}

// claimHasSink reports whether this claim requests a speaker. The
// claim carries one request per sink the Player states, and none for
// a Player that states no sink, so it is the speaker gate the pod
// builder reads without holding the Player.
func claimHasSink(claim *ResourceClaim) bool {
	for _, request := range claim.Spec.Devices.Requests {
		if strings.HasPrefix(request.Name, audioRequestPrefix) {
			return true
		}
	}
	return false
}

// ensureClaim creates the playback claim when none exists and keeps an
// existing one. A 409 on the create is success, because another pass,
// or another copy of this operator, created the same claim first. The
// graceful recreate deletes a claim a Player reshaped before it calls
// this, so ensureClaim never updates a claim in place.
func ensureClaim(c *Client, claim *ResourceClaim) error {
	_, err := GetResourceClaim(c, claim.Metadata.Namespace, claim.Metadata.Name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if _, err := CreateResourceClaim(c, claim); err != nil && !errors.Is(err, ErrConflict) {
		return err
	}
	return nil
}

// claimDiverged reports whether the claim the current Player produces
// differs from the one in the cluster. An absent claim counts as
// diverged, so the recreate creates it.
func (o *operator) claimDiverged(desired *ResourceClaim) (bool, error) {
	current, err := GetResourceClaim(o.client, desired.Metadata.Namespace, desired.Metadata.Name)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	same, err := sameClaimSpec(current.Spec, desired.Spec)
	if err != nil {
		return false, err
	}
	return !same, nil
}

// sameClaimSpec compares two claim specs by their marshaled form. It
// reads only the fields this operator's own types model, because the
// client drops every field it does not know when it reads the stored
// claim, so a field the API server adds does not read as a change.
func sameClaimSpec(current, desired ResourceClaimSpec) (bool, error) {
	was, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	wants, err := json.Marshal(desired)
	if err != nil {
		return false, err
	}
	return string(was) == string(wants), nil
}
