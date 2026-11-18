package main

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"sync"
)

// preparedClaims records which claims hold each device, for
// status.claims.
//
// The record is only in memory, because the kubelet's prepare call is
// the only source of a claim's namespace and name. The CDI spec that
// prepare writes names only the claim's UID, and the API server cannot
// look up a claim by UID. So after the operator pod restarts, status
// leaves out the claims of running pods until the kubelet prepares them
// again.
//
// The kubelet's calls and the reconcile pass both use the record, so a
// mutex guards it.
type preparedClaims struct {
	mutex sync.RWMutex
	// held maps a device name to the claims that hold it, keyed by claim
	// UID. devices maps a claim UID to its device names, because an
	// unprepare call names only the claim's UID.
	held    map[string]map[string]EndpointClaim
	devices map[string][]string
}

// record records that the claim with this UID holds these devices.
// A nil receiver does nothing, so a plugin built without a record needs
// no nil check. The other methods treat a nil receiver the same way.
func (p *preparedClaims) record(uid string, claim EndpointClaim, devices []string) {
	if p == nil || len(devices) == 0 {
		return
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.held == nil {
		p.held, p.devices = map[string]map[string]EndpointClaim{}, map[string][]string{}
	}
	for _, device := range devices {
		if p.held[device] == nil {
			p.held[device] = map[string]EndpointClaim{}
		}
		p.held[device][uid] = claim
	}
	p.devices[uid] = devices
}

// release removes the claim with this UID from each device it holds.
// Other claims on a shared device keep it.
func (p *preparedClaims) release(uid string) {
	if p == nil {
		return
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, device := range p.devices[uid] {
		delete(p.held[device], uid)
		if len(p.held[device]) == 0 {
			delete(p.held, device)
		}
	}
	delete(p.devices, uid)
}

// holders returns the claims that hold the device, sorted by namespace
// and name, or nil when no claim holds it.
func (p *preparedClaims) holders(device string) []EndpointClaim {
	if p == nil {
		return nil
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if len(p.held[device]) == 0 {
		return nil
	}
	claims := slices.Collect(maps.Values(p.held[device]))
	slices.SortFunc(claims, func(a, b EndpointClaim) int {
		return cmp.Or(strings.Compare(a.Namespace, b.Namespace), strings.Compare(a.Name, b.Name))
	})
	return claims
}

func (p *preparedClaims) heldByAnother(device, uid string) bool {
	if p == nil {
		return false
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	for holder := range p.held[device] {
		if holder != uid {
			return true
		}
	}
	return false
}
