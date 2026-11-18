package main

// This file computes a Player's status.sinks: the Sink that the
// scheduler allocated for each entry of spec.sinks. The allocation is
// recorded only in the Play's claim. The operator copies it to the
// Player's status because a client would need get on resourceclaims in
// every namespace to read the claim, and the claim is deleted with its
// Play.

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// audioDriver is the DRA driver name of audio-operator. audio-operator
// gives each endpoint's device and its Sink object the same name, so
// the device name of an allocation result is the name of the Sink.
const audioDriver = "audio.liken.sh"

// reconcileSinks returns the list that a pass writes to status.sinks:
// the Sinks allocated to the running Play's claim. When no Play runs,
// or its claim is missing, unreadable, or has no audio results yet, it
// returns the list already in status, so the Sinks stay on the Player
// after a Play ends.
func (o *operator) reconcileSinks(player *Player, play string) []PlayerSinkStatus {
	remembered := player.Status.Sinks
	if play == "" {
		return remembered
	}
	claim, err := o.view.ResourceClaim(player.Metadata.Namespace, claimName(play))
	if err != nil {
		if !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "reading the ResourceClaim of the Play on Player %s/%s: %v\n",
				player.Metadata.Namespace, player.Metadata.Name, err)
		}
		return remembered
	}
	found := sinksOf(claim, len(player.Spec.Sinks))
	if len(found) == 0 {
		return remembered
	}
	return found
}

// sinksOf returns the Sink allocated to each of the first sinks
// requests (audio0, audio1, and so on) of a claim, in request order.
// The scheduler writes the results in its own order, and status.sinks
// must follow spec.sinks, which is also the order of the audio tracks
// in the stream. A request with no result from audioDriver has no
// entry, and a claim that is not allocated returns nil.
func sinksOf(claim *ResourceClaim, sinks int) []PlayerSinkStatus {
	if claim == nil || claim.Status == nil || claim.Status.Allocation == nil {
		return nil
	}
	var found []PlayerSinkStatus
	for index := range sinks {
		request := audioRequestPrefix + strconv.Itoa(index)
		for _, result := range claim.Status.Allocation.Devices.Results {
			if result.Driver != audioDriver || result.Request != request {
				continue
			}
			found = append(found, PlayerSinkStatus{Request: request, Name: result.Device})
			break
		}
	}
	return found
}
