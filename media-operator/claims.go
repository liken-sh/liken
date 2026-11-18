package main

// The ResourceClaim of a Play has one request for each device of its
// Player, with the Player's device parameters as opaque config.

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The container's resources.claims entries refer to these request
// names. The devices share one claim so that the scheduler allocates
// them together, on one machine that has every device, or the pod stays
// Pending.
//
// A Remote's pod has a claim of its own, whose request is remote-
// followed by the Remote's name.
const (
	screenRequest       = "screen"
	renderRequest       = "render"
	audioRequestPrefix  = "audio"
	remoteRequestPrefix = "remote-"
)

// remoteDisconnectedTaint is the device taint that bluetooth-operator
// sets on a disconnected controller. A Remote's pod tolerates it with
// no time limit, because a controller disconnects whenever a person puts
// it down. The playback claim tolerates no disconnected taint, so when a
// display or a speaker leaves, the playback pod is evicted and the
// operator creates it again at the position where the Play stopped.
const (
	remoteDisconnectedTaint = "bluetooth.liken.sh/disconnected"
)

func remoteRequestName(remote string) string {
	return remoteRequestPrefix + remote
}

// tolerateForever tolerates the NoExecute taint of that key with no
// tolerationSeconds, so the taint never evicts the pod.
func tolerateForever(taint string) []DeviceToleration {
	return []DeviceToleration{{
		Key:      taint,
		Operator: "Exists",
		Effect:   "NoExecute",
	}}
}

// claimName returns the name of the Play's claim, so a person who reads
// either object can find the other.
func claimName(play string) string {
	return play + "-devices"
}

// playOwner makes the Play the owner of its claim and pod, so deleting
// the Play deletes them through garbage collection.
func playOwner(play *Play) OwnerReference {
	return OwnerReference{
		APIVersion: mediaAPIVersion,
		Kind:       "Play",
		Name:       play.Metadata.Name,
		UID:        play.Metadata.UID,
		Controller: true,
	}
}

// buildClaim adds requests in the order screen, sinks, render. A Player
// with no display or no render node gets no request for it, and the
// player program runs without that device.
//
// The claim has no request for a controller. Each Remote has its own
// pod and claim, so a controller on one machine can work with a display
// on another.
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
		claim.add(renderRequest, *player.Spec.Render, nil)
	}
	return claim
}

// add appends a request for the device. The device's parameters go in
// a config block that names only this request, so a setting for one
// device never reaches another.
func (c *ResourceClaim) add(name string, device PlayerDevice, tolerations []DeviceToleration) {
	request := DeviceRequest{
		Name: name,
		Exactly: &ExactDeviceRequest{
			DeviceClassName: device.Class,
			AllocationMode:  "ExactCount",
			Count:           1,
		},
	}
	// The API server rejects an empty CEL expression, so an empty
	// selector leaves out the selectors list.
	if device.Selector != "" {
		request.Exactly.Selectors = []DeviceSelector{{CEL: &CELDeviceSelector{Expression: device.Selector}}}
	}
	request.Exactly.Tolerations = tolerations
	c.Spec.Devices.Requests = append(c.Spec.Devices.Requests, request)

	if device.Parameters == nil {
		return
	}
	// The operator copies the parameters without reading them. The
	// driver reads them when it prepares the device.
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

func claimRequests(claim *ResourceClaim) []string {
	names := make([]string, 0, len(claim.Spec.Devices.Requests))
	for _, request := range claim.Spec.Devices.Requests {
		names = append(names, request.Name)
	}
	return names
}

func claimEntries(claim *ResourceClaim) []ContainerClaim {
	var entries []ContainerClaim
	for _, request := range claimRequests(claim) {
		entries = append(entries, ContainerClaim{Name: podClaimName, Request: request})
	}
	return entries
}

func claimHasScreen(claim *ResourceClaim) bool {
	for _, request := range claim.Spec.Devices.Requests {
		if request.Name == screenRequest {
			return true
		}
	}
	return false
}

func claimHasSink(claim *ResourceClaim) bool {
	for _, request := range claim.Spec.Devices.Requests {
		if strings.HasPrefix(request.Name, audioRequestPrefix) {
			return true
		}
	}
	return false
}

// ensureClaim leaves an existing claim as it is, because a claim's spec
// cannot change. When a Player's devices change, replace deletes the old
// claim and creates the new one. A 409 on the create means another pass
// created the claim first.
func ensureClaim(c *apiclient.Client, claim *ResourceClaim) error {
	_, err := GetResourceClaim(c, claim.Metadata.Namespace, claim.Metadata.Name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, apiclient.ErrNotFound) {
		return err
	}
	if _, err := CreateResourceClaim(c, claim); err != nil && !errors.Is(err, apiclient.ErrConflict) {
		return err
	}
	return nil
}

// claimDiverged counts a missing claim as different, so the replace
// creates it.
func (o *operator) claimDiverged(desired *ResourceClaim) (bool, error) {
	current, err := GetResourceClaim(o.client, desired.Metadata.Namespace, desired.Metadata.Name)
	if errors.Is(err, apiclient.ErrNotFound) {
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

// sameClaimSpec compares the JSON encodings. The operator's types have
// only the fields that it sets, so decoding the stored claim drops the
// fields that the API server adds, and they do not count as a
// difference.
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
