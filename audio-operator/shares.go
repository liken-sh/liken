package main

// An endpoint that several claims hold at once.
//
// PipeWire mixes every stream that plays into a sink, so one sink can
// serve several consumers. The operator still publishes each endpoint
// for one claim, because a workload that allocates the television's
// speakers then knows that nothing else plays through them. A cluster
// owner who wants an endpoint shared states how many claims may hold
// it, in spec.sharedClaims on its Sink or Source.
//
// The scheduler enforces the count through consumable capacity
// (KEP-5075, the DRAConsumableCapacity gate). The device publishes
// allowMultipleAllocations and a capacity named claims, set to the
// count, with no request policy. A claim that asks for none of the
// capacity is charged all of it, so a claim written for an exclusive
// endpoint stays exclusive and keeps every other claim off the device.
// A claim that asks for one of the claims shares the device with the
// other claims that asked for one, up to the count.
//
// No limit of the hardware or of PipeWire sets the count. The count is
// the cluster owner's policy, and its absence keeps the endpoint to
// one claim.

import (
	"fmt"
	"maps"
	"os"
	"strconv"
)

// sharedClaimsCapacity names the capacity a shared device publishes,
// and the capacity a claim's request asks for to share it.
const sharedClaimsCapacity = "claims"

// shareDevices marks each device whose resource declares a share
// count. A count below two is no share, and the device stays
// exclusive.
func shareDevices(devices []SliceDevice, shares map[string]int) []SliceDevice {
	for i, device := range devices {
		count := shares[device.Name]
		if count < 2 {
			continue
		}
		shared := true
		devices[i].AllowMultipleAllocations = &shared
		devices[i].Capacity = map[string]DeviceCapacity{
			sharedClaimsCapacity: {Value: strconv.Itoa(count)},
		}
	}
	return devices
}

// sharedClaims answers the share count each of this machine's Sinks
// and Sources declares, by device name. A resource that declares none
// is left out.
func (e *endpointControl) sharedClaims() (map[string]int, error) {
	shares := map[string]int{}
	sinks, err := e.readSinks()
	if err != nil {
		return nil, fmt.Errorf("reading the Sinks: %w", err)
	}
	for _, sink := range sinks {
		if sink.Spec.SharedClaims != nil {
			shares[sink.Metadata.Name] = *sink.Spec.SharedClaims
		}
	}
	sources, err := e.readSources()
	if err != nil {
		return nil, fmt.Errorf("reading the Sources: %w", err)
	}
	for _, source := range sources {
		if source.Spec.SharedClaims != nil {
			shares[source.Metadata.Name] = *source.Spec.SharedClaims
		}
	}
	return shares, nil
}

// shares answers the share counts for the slice this pass publishes.
// A read that fails keeps the counts of the last read that succeeded.
// The resources say nothing new when the API server refuses a list,
// and a device that dropped its share for one pass would turn
// exclusive in the slice while shares of it still play.
func (r *reconciler) shares() map[string]int {
	if r.readShares == nil {
		return r.lastShares
	}
	read, err := r.readShares()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the share counts; the slice keeps the last ones read: %v\n", err)
		return r.lastShares
	}
	r.lastShares = maps.Clone(read)
	return read
}

// claimShares reports whether the claim's request for one allocation
// result asks for the claims capacity, which is what makes the claim
// a share of the device. A result of a request with alternatives
// names the alternative as request/subrequest.
func claimShares(claim *ResourceClaim, request string) bool {
	for _, each := range claim.Spec.Devices.Requests {
		if each.Exactly != nil && each.Name == request {
			return asksForAShare(each.Exactly.Capacity)
		}
		for _, alternative := range each.FirstAvailable {
			if each.Name+"/"+alternative.Name == request {
				return asksForAShare(alternative.Capacity)
			}
		}
	}
	return false
}

func asksForAShare(capacity *claimCapacity) bool {
	if capacity == nil {
		return false
	}
	_, asks := capacity.Requests[sharedClaimsCapacity]
	return asks
}

// claimRequests holds the part of a claim's spec that says whether
// each request asks for a share. The driver reads nothing else of the
// spec.
type claimRequests struct {
	Requests []claimRequest `json:"requests"`
}

type claimRequest struct {
	Name           string             `json:"name"`
	Exactly        *claimExactRequest `json:"exactly"`
	FirstAvailable []claimSubrequest  `json:"firstAvailable"`
}

type claimExactRequest struct {
	Capacity *claimCapacity `json:"capacity"`
}

type claimSubrequest struct {
	Name     string         `json:"name"`
	Capacity *claimCapacity `json:"capacity"`
}

type claimCapacity struct {
	Requests map[string]string `json:"requests"`
}
