package main

// Sharing one Sink or Source among several ResourceClaims.
//
// PipeWire mixes every stream played into a sink, so several claims
// can play through one endpoint. An endpoint is exclusive unless its
// Sink or Source sets spec.sharedClaims, because a workload that claims
// a television's speakers expects that no other workload plays through
// them.
//
// The scheduler enforces spec.sharedClaims with DRA consumable capacity
// (KEP-5075, the DRAConsumableCapacity feature gate). A shared device in
// the ResourceSlice sets allowMultipleAllocations and a capacity named
// claims whose value is spec.sharedClaims, with no requestPolicy. When a
// claim's request does not set capacity.requests.claims, the scheduler
// allocates it the whole capacity, so a claim written for an exclusive
// endpoint still gets the device alone. Claims that each set
// capacity.requests.claims to "1" share the device, up to
// spec.sharedClaims of them.

import (
	"fmt"
	"maps"
	"os"
	"strconv"
)

const sharedClaimsCapacity = "claims"

// shareDevices publishes a device as shared when shares gives it a
// count of 2 or more.
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

// sharedClaims returns spec.sharedClaims of this machine's Sinks and
// Sources, keyed by resource name, which is also the device name in the
// ResourceSlice.
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

// shares returns the share counts for the ResourceSlice of this pass.
// When the read fails, it returns the last counts read, because a
// refused list does not mean that a count changed, and without the
// counts the pass would publish shared devices as exclusive.
func (r *reconciler) shares() map[string]int {
	if r.readShares == nil {
		return r.lastShares
	}
	read, err := r.readShares()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the share counts are unreadable, so the slice keeps the last counts read: %v\n", err)
		return r.lastShares
	}
	r.lastShares = maps.Clone(read)
	return read
}

// claimShares reports whether the request that an allocation result
// names sets capacity.requests.claims, which makes the claim a shared
// one. The allocation result of a firstAvailable request names the
// subrequest as "<request>/<subrequest>".
func claimShares(claim *ResourceClaim, request string) bool {
	for _, each := range claim.Spec.Devices.Requests {
		if each.Exactly != nil && each.Name == request {
			return requestsAShare(each.Exactly.Capacity)
		}
		for _, alternative := range each.FirstAvailable {
			if each.Name+"/"+alternative.Name == request {
				return requestsAShare(alternative.Capacity)
			}
		}
	}
	return false
}

func requestsAShare(capacity *claimCapacity) bool {
	if capacity == nil {
		return false
	}
	_, requested := capacity.Requests[sharedClaimsCapacity]
	return requested
}

// claimRequests decodes the requests in a ResourceClaim's
// spec.devices, with only the fields that claimShares reads.
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
