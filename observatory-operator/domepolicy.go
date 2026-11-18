package main

// The lock relay also keeps each running mount's DOME_POLICY in line
// with its observatory's domes. A mount under DOME_LOCKS keeps the last
// dome report it received, so when a person deletes the observatory's
// last dome, the mount stays locked to a parked dome that no longer
// exists. The relay's pass then writes DOME_IGNORED to each mount of
// that observatory, without waiting for the next Configure. A dome
// declared during a session counts only from the next session
// (tree.server), whose Configure writes DOME_LOCKS.
//
// The pass acts only on a telescope whose reservation is Ready, because
// Configure writes the policy during activation, and steady.go writes
// it again after a mount's driver restarts.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// policyWait is the time limit for one write of a mount's DOME_POLICY
// and its configuration save.
const policyWait = 30 * time.Second

// mountPolicies is the state of the pass between runs.
type mountPolicies struct {
	// tried holds the server's epoch and the policy of the last write to
	// each mount. So a driver that refuses a policy is not written again
	// on each pass, and a driver that restarts, which starts a new epoch,
	// is written again.
	tried map[string]string
	group sync.WaitGroup
	// writing holds the mounts that have a write in progress. The epoch
	// can change during a write, for example when the driver defines a
	// property, and without this set the next pass would start a second
	// write and a second save of the driver's configuration file.
	mu      sync.Mutex
	writing map[string]bool
	// seen is the version of the stores and the INDI epoch at the last
	// pass. An update of a property, such as a mount's position, changes
	// neither, so the pass returns at once for it.
	seen [2]uint64
}

// keepDomePolicies writes DOME_POLICY to each connected mount of a
// Ready reservation's telescope whose policy differs from what its
// observatory's domes require, and saves the driver's configuration
// (lockPolicy). Each write runs on its own goroutine, so a slow driver
// does not delay the lock relay.
func (o *operator) keepDomePolicies(ctx context.Context, p *mountPolicies) {
	now := [2]uint64{o.stores.version.Load(), epochs.Load()}
	if now == p.seen {
		return
	}
	p.seen = now
	// Take the snapshot after reading the versions, so a change that
	// arrives in between changes the versions again and starts another
	// pass.
	t := o.snapshot()
	for _, site := range sortedNames(t.observatories) {
		locked := len(domesOf(t, t.observatories[site])) > 0
		want := "DOME_IGNORED"
		if locked {
			want = "DOME_LOCKS"
		}
		for _, telescope := range sortedNames(t.telescopes) {
			holder, held := o.claims.holderOf(telescope)
			if !held || t.telescopes[telescope].Spec.Observatory != site {
				continue
			}
			if res := t.reservations[holder]; res == nil || res.Status.Phase != observatory.ReservationReady {
				continue
			}
			ref := serverRef{observatory.TelescopeKind, telescope}
			for _, h := range o.handlesOf(t, ref, t.devicesOf(ref, observatory.MountKind)) {
				policy, ok := h.client().Property(h.name, "DOME_POLICY")
				if !ok || !h.connected() || isOn(policy, want) {
					continue
				}
				id, key := h.server.name+"/"+h.name, fmt.Sprintf("%d %s", h.server.epoch.Load(), want)
				if p.tried[id] == key || !p.start(id) {
					continue
				}
				p.tried[id] = key
				p.group.Go(func() {
					defer p.finish(id)
					wait, cancel := context.WithTimeout(ctx, policyWait)
					defer cancel()
					var notes []string
					if _, err := lockPolicy(wait, h, "DOME_POLICY", "DOME_LOCKS", "DOME_IGNORED", locked, &notes); err != nil {
						o.logf("writing %s to %s: %v", want, h, err)
					}
					for _, note := range notes {
						o.logf("writing %s to %s: %s", want, h, note)
					}
				})
			}
		}
	}
}

// start marks a write to a mount as in progress, and returns false when
// one is already in progress.
func (p *mountPolicies) start(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.writing == nil {
		p.writing = map[string]bool{}
	}
	if p.writing[id] {
		return false
	}
	p.writing[id] = true
	return true
}

func (p *mountPolicies) finish(id string) {
	p.mu.Lock()
	delete(p.writing, id)
	p.mu.Unlock()
}
