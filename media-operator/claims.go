package main

// This file writes the manifest the design says nobody should have
// to write again: one claim that holds the whole player, request by
// request, with the tolerations and parameter blocks a person wrote
// by hand in the days of the demo.

import (
	"encoding/json"
	"strconv"
)

// The request names are the roles, and they are the names the
// container's resources.claims refer to. One claim with named
// requests beats one claim per device because the set allocates as a
// unit: the scheduler finds one machine that satisfies every
// request, or the pod parks Pending with one story to read.
const (
	screenRequest      = "screen"
	renderRequest      = "render"
	audioRequestPrefix = "audio"
)

// The two taints the hardware operators set when equipment goes
// away, tolerated for thirty seconds: that long is a cable being
// moved, and longer is equipment that left, which ends the pod so
// the play fails rather than freezing.
const (
	displayDisconnectedTaint = "display.liken.sh/disconnected"
	audioDisconnectedTaint   = "audio.liken.sh/disconnected"
	disconnectedTolerance    = 30
)

// The render node takes no toleration, because a GPU does not come
// and go while the machine runs.
const noTaint = ""

// claimName is the Play's name plus its job, so a person reading
// either object finds the other.
func claimName(play string) string {
	return play + "-devices"
}

// playOwner is the ownerReference that makes deleting the Play the
// whole teardown: the garbage collector deletes what the Play owns,
// and this operator carries no delete verb at all.
func playOwner(play *Play) OwnerReference {
	return OwnerReference{
		APIVersion: mediaAPIVersion,
		Kind:       "Play",
		Name:       play.Metadata.Name,
		UID:        play.Metadata.UID,
		Controller: true,
	}
}

// buildClaim turns one Player into one claim, requests in role
// order: screen, sinks, render. A Player without a display or
// without a render node yields a claim without that request, and the
// player program plays without one, which is what an audio-only unit
// is.
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
		claim.add(screenRequest, *player.Spec.Display, displayDisconnectedTaint)
	}
	for index, sink := range player.Spec.Sinks {
		claim.add(audioRequestPrefix+strconv.Itoa(index), sink, audioDisconnectedTaint)
	}
	if player.Spec.Render != nil {
		claim.add(renderRequest, *player.Spec.Render, noTaint)
	}
	return claim
}

// add turns one device into a request and, when the Player states
// parameters for it, one opaque config block aimed at that request
// alone, so a codec never lands on a screen.
func (c *ResourceClaim) add(name string, device PlayerDevice, taint string) {
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
	if taint != noTaint {
		seconds := int64(disconnectedTolerance)
		request.Exactly.Tolerations = []DeviceToleration{{
			Key:               taint,
			Operator:          "Exists",
			Effect:            "NoExecute",
			TolerationSeconds: &seconds,
		}}
	}
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

// claimRequests lists the request names in claim order, which is
// what the container's resources.claims list repeats.
func claimRequests(claim *ResourceClaim) []string {
	names := make([]string, 0, len(claim.Spec.Devices.Requests))
	for _, request := range claim.Spec.Devices.Requests {
		names = append(names, request.Name)
	}
	return names
}
