package main

// Each running mount's DOME_POLICY follows the domes of its
// observatory. A mount under DOME_LOCKS keeps the last dome report it
// received, so a mount whose dome was deleted stays locked to a parked
// dome that no longer exists, until its driver restarts or the next
// Configure. So the lock relay's pass also writes the policy: when an
// observatory's last dome goes, each running mount of it gets
// DOME_IGNORED at once, and when a dome comes, DOME_LOCKS. The write
// saves the driver's configuration, as Configure's does (lockPolicy).

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// policyWait bounds one write of a mount's DOME_POLICY and its save. It
// is a clock.
const policyWait = 30 * time.Second

// mountPolicies holds what the pass wrote to each mount, so a driver
// that answers with another policy is not written again on each
// change. The key holds the server's epoch, so a driver that restarts
// is written again.
type mountPolicies struct {
	tried map[string]string
	group sync.WaitGroup
	// seen holds the version of the stores and the INDI epoch that the
	// last pass read. A reading changes neither, so a pass that finds
	// both the same has nothing new to write, and a mount that reports
	// its position several times a second costs nothing here.
	seen [2]uint64
}

// keepDomePolicies writes DOME_POLICY to each connected mount of a held
// telescope whose policy differs from what its observatory's domes ask
// for. Each write runs on its own goroutine, so a slow driver does not
// hold the lock relay.
func (o *operator) keepDomePolicies(ctx context.Context, p *mountPolicies) {
	now := [2]uint64{o.stores.version.Load(), epochs.Load()}
	if now == p.seen {
		return
	}
	p.seen = now
	// The snapshot comes after the versions, so a change after them
	// starts another pass.
	t := o.snapshot()
	for _, site := range sortedNames(t.observatories) {
		locked := len(domesOf(t, t.observatories[site])) > 0
		want := "DOME_IGNORED"
		if locked {
			want = "DOME_LOCKS"
		}
		for _, telescope := range sortedNames(t.telescopes) {
			if _, held := o.claims.holderOf(telescope); !held || t.telescopes[telescope].Spec.Observatory != site {
				continue
			}
			ref := serverRef{observatory.TelescopeKind, telescope}
			for _, h := range o.handlesOf(t, ref, t.devicesOf(ref, observatory.MountKind)) {
				policy, ok := h.client().Property(h.name, "DOME_POLICY")
				if !ok || !h.connected() || isOn(policy, want) {
					continue
				}
				id, key := h.server.name+"/"+h.name, fmt.Sprintf("%d %s", h.server.epoch.Load(), want)
				if p.tried[id] == key {
					continue
				}
				p.tried[id] = key
				p.group.Go(func() {
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
