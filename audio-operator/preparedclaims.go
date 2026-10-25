package main

// The record of which claims hold each endpoint, which status.claims
// reports.

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"sync"
)

// preparedClaims is which claims hold each device now: one claim on
// an exclusive endpoint, and up to the share count on a shared one.
//
// The record is in memory and nowhere else. A prepare call is where
// the claim's namespace and name arrive, and the CDI file the prepare
// leaves behind carries the claim's UID alone, which names no claim a
// reader can ask the API server for. So the plugin keeps the pair,
// the resource reports it, and a pod that started before this pod
// shows no claim until the kubelet prepares its claim again.
//
// The kubelet's calls and the reconcile pass both reach it, which is
// why it holds a lock.
type preparedClaims struct {
	mutex sync.RWMutex
	// held answers status.claims: for each allocated device name, the
	// claims that hold it, by UID. devices is the other direction,
	// because an unprepare call carries the claim's UID and no device
	// at all.
	held    map[string]map[string]EndpointClaim
	devices map[string][]string
}

// prepared records one claim's devices. A nil record takes the write
// and drops it, so a plugin built without one needs no branch.
func (p *preparedClaims) prepared(uid string, claim EndpointClaim, devices []string) {
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

// released drops what one claim held. The other claims that share a
// device keep it.
func (p *preparedClaims) released(uid string) {
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

// holders answers the claims that hold one device, sorted by
// namespace and name, and nothing between holders.
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

// heldByAnother reports whether a claim other than the one with this
// UID holds the device now.
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
