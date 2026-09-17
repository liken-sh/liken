package main

// This file derives the sink memory on a Player: the Sink each
// spec.sinks selection resolved to. The spec holds selections, and the
// scheduler picks the device, so only an allocated playback claim says
// which Sink a Player plays through. The operator publishes the answer
// on the Player because a client cannot derive it: reading the claim
// would need get on resourceclaims in every namespace, between runs
// there is no claim, and nothing re-derives an allocation the scheduler
// owns.

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// audioDriver is the audio operator's DRA driver name, the driver an
// allocation result names for a sink. The device name that driver
// allocates is the Sink's own name: the audio operator publishes each
// endpoint as a device under its inventory name and creates the Sink
// under that same name, so the result's device is the object a client
// asks the audio API for.
const audioDriver = "audio.liken.sh"

// reconcileSinks answers the sink list a pass writes on the Player: the
// Sinks the running Play's claim resolved, or the list the Player
// already carries when nothing better is known. The list is memory, so
// no Play, a claim that is gone, a claim the pass cannot read, and a
// claim the scheduler has not allocated yet all keep the remembered
// list. Replacing it with nothing would make the Player forget its
// Sinks every time a Play retires.
func (o *operator) reconcileSinks(player *Player, play string) []PlayerSinkStatus {
	remembered := player.Status.Sinks
	if play == "" {
		return remembered
	}
	claim, err := GetResourceClaim(o.client, player.Metadata.Namespace, claimName(play))
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			fmt.Fprintf(os.Stderr, "reading the playback claim of player %s/%s: %v\n",
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

// sinksOf reads the Sinks out of an allocated claim, one per spec.sinks
// position. It walks the spec's positions and looks each request up in
// the allocation, rather than walking the allocation, because the
// allocation's order is the scheduler's and the list must be in spec
// order, the order a person wrote and the order the composed stream
// puts its tracks in. Each answer carries the request name, audio0 and
// so on, and the device name, which is the Sink's name. A request with
// no result from the audio driver adds no entry. An unallocated claim
// answers nil.
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
