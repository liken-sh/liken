package main

// The demand side of the relay: which events each real node delivers.
//
// A virtual device exists for every node whether or not a claim names
// the controller, so node numbers stay stable. What a claim changes is
// only what the kernel queues on the real node behind it. Demand is
// the union over the prepared claims on a controller, so two consumers
// of one controller each receive what they asked for, and a node that
// no claim asks for delivers nothing.

import (
	"fmt"
	"os"
)

// prepare records what one claim asks a controller to deliver. The
// kubelet calls it once for each claim before the consumer's
// container starts, and again whenever it repeats a prepare it has no
// record of, so a claim already recorded is overwritten with the same
// answer.
func (r *relays) prepare(mac, claimUID string, want inputClasses) {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.controller(mac)
	held.demand[claimUID] = want
	r.narrow(mac, held)
	fmt.Printf("relay: claim %s receives %s of controller %s\n", claimUID, want, publishedMAC(mac))
}

// unprepare withdraws one claim's demand. The prepare call names a
// controller and this one does not, so every controller is searched:
// one claim can hold two of them.
func (r *relays) unprepare(claimUID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for mac, held := range r.held {
		if _, found := held.demand[claimUID]; !found {
			continue
		}
		delete(held.demand, claimUID)
		r.narrow(mac, held)
	}
}

// restorePrepared rebuilds the demand from the claims that were
// prepared before this operator started.
//
// The record is the CDI spec files, not the API. The same files
// already record which nodes each consumer holds, they are on this
// node's own disk, and reading the claims back from the API would
// widen this operator's grant to every workload's claims for a fact
// it has already written down.
func (r *relays) restorePrepared() {
	eachDeliveredDevice(func(prepared deliveredDevice) {
		mac, ok := prepared.controller()
		if !ok {
			return
		}
		want := everyInputClass
		if len(prepared.inputs) > 0 {
			parsed, err := parseInputClasses(prepared.inputs)
			if err != nil {
				// A record this operator cannot read restores as every
				// class, which is what the consumer holding the node
				// received before the restart.
				fmt.Fprintf(os.Stderr, "relay: reading the inputs of prepared claim %s: %v\n", prepared.claimUID, err)
			} else {
				want = parsed
			}
		}
		r.prepare(mac, prepared.claimUID, want)
	})
}

// demanded is the union of what every prepared claim on one
// controller asks for. It is none for a controller no claim holds,
// and the pumps of such a controller deliver nothing.
func (c *controllerRelay) demanded() inputClasses {
	var want inputClasses
	for _, claim := range c.demand {
		want |= claim
	}
	return want
}

// narrow makes every open node of one controller queue exactly what
// its prepared claims demand. The caller holds the lock.
func (r *relays) narrow(mac string, held *controllerRelay) {
	want := held.demanded()
	for _, relay := range held.nodes {
		relay.narrow(mac, want)
	}
}

// narrow limits one node to a demand. A node with no real node behind
// it right now takes the demand on the next open, because a fresh fd
// carries no mask.
//
// A widened demand sets a mask that passes everything. The kernel
// keeps a mask on an fd until another one replaces it, and has no
// call that removes one.
func (n *nodeRelay) narrow(mac string, want inputClasses) {
	if n.source == nil {
		return
	}
	masks := inputMasks(n.caps, want)
	apply, narrowed := masks, masks != nil
	if !narrowed {
		if !n.narrowed {
			return
		}
		apply = passEverything()
	}
	if err := n.source.narrow(apply); err != nil {
		fmt.Fprintf(os.Stderr, "relay: limiting %s of controller %s: %v\n", n.sourcePath, publishedMAC(mac), err)
		return
	}
	n.narrowed = narrowed
}
