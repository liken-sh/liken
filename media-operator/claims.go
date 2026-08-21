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

// The two taints the hardware operators set when equipment goes
// away, tolerated for thirty seconds: that long is a cable being
// moved, and longer is equipment that left, which ends the pod so
// the play fails rather than freezing.
//
// The controller's taint is the third, and it is tolerated with no
// limit at all, because a controller that sleeps is normal and a
// film must keep playing without it.
const (
	displayDisconnectedTaint = "display.liken.sh/disconnected"
	audioDisconnectedTaint   = "audio.liken.sh/disconnected"
	remoteDisconnectedTaint  = "bluetooth.liken.sh/disconnected"
	disconnectedTolerance    = 30
)

func remoteRequestName(remote string) string {
	return remoteRequestPrefix + remote
}

// tolerateBriefly survives a cable being moved: thirty seconds of
// absence keeps the pod, and longer evicts it, so the play fails
// rather than freezing on equipment that left.
func tolerateBriefly(taint string) []DeviceToleration {
	seconds := int64(disconnectedTolerance)
	return []DeviceToleration{{
		Key:               taint,
		Operator:          "Exists",
		Effect:            "NoExecute",
		TolerationSeconds: &seconds,
	}}
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
		claim.add(screenRequest, *player.Spec.Display, tolerateBriefly(displayDisconnectedTaint))
	}
	for index, sink := range player.Spec.Sinks {
		claim.add(audioRequestPrefix+strconv.Itoa(index), sink, tolerateBriefly(audioDisconnectedTaint))
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
