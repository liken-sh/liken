package main

// The demand side of the relay: which events each real node delivers.
//
// A virtual device exists for every node whether or not a claim names
// the controller, so node numbers stay stable. What a claim changes is
// only what the kernel queues on the real node behind it. Demand is
// the union over the prepared claims on a controller, so two consumers
// of one controller each receive what they asked for, and a node that
// no claim asks for delivers nothing.
//
// A claim states two things. `inputs` decides which event types the
// kernel queues on the real node, and `axes` decides how far a
// position must move before the kernel reports it at all. Both are
// written on the real node's own file descriptor. The mask changes
// only this operator's view of the device; the axes change the device
// itself, for every reader, which is why the device's own values are
// stored and put back.

import (
	"fmt"
	"os"
)

// prepare records what one claim asks a controller to deliver. The
// kubelet calls it once for each claim before the consumer's
// container starts, and again whenever it repeats a prepare it has no
// record of, so a claim already recorded is overwritten with the same
// answer.
func (r *relays) prepare(mac, claimUID string, want delivery) {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.controller(mac)
	held.demand[claimUID] = want
	r.apply(mac, held)
	fmt.Printf("relay: claim %s receives %s of controller %s\n", claimUID, want.classes, publishedMAC(mac))
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
		r.apply(mac, held)
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
		want := delivery{classes: everyInputClass}
		if len(prepared.inputs) > 0 {
			parsed, err := parseInputClasses(prepared.inputs)
			if err != nil {
				// A record this operator cannot read restores as every
				// class, which is what the consumer holding the node
				// received before the restart.
				fmt.Fprintf(os.Stderr, "relay: reading the inputs of prepared claim %s: %v\n", prepared.claimUID, err)
			} else {
				want.classes = parsed
			}
		}
		if prepared.axes != "" {
			parsed, err := parseRecordedAxes(prepared.axes)
			if err != nil {
				// A record this operator cannot read leaves the axes as
				// the device reports them, which is what the consumer
				// received before the parameter existed.
				fmt.Fprintf(os.Stderr, "relay: reading the axes of prepared claim %s: %v\n", prepared.claimUID, err)
			} else {
				want.axes = parsed
			}
		}
		r.prepare(mac, prepared.claimUID, want)
	})
}

// demanded is the union of what every prepared claim on one
// controller asks for. It is none for a controller no claim holds,
// and the pumps of such a controller deliver nothing.
//
// An axis takes the largest fuzz and the largest flat any prepared
// claim states, so each claim receives at least the smoothing it
// asked for. An axis no remaining claim states goes back to the
// device's own values.
func (c *controllerRelay) demanded() delivery {
	want := delivery{}
	for _, claim := range c.demand {
		want.classes |= claim.classes
		want.axes = want.axes.merge(claim.axes)
	}
	return want
}

// apply makes every open node of one controller agree with what its
// prepared claims demand: the events the kernel queues, and the fuzz
// and flat of each absolute axis. The caller holds the lock.
func (r *relays) apply(mac string, held *controllerRelay) {
	want := held.demanded()
	for _, relay := range held.nodes {
		relay.narrow(mac, want.classes)
		relay.tune(mac, want.axes)
	}
}

// tune writes each axis of one node so it carries the fuzz and the
// flat the prepared claims state, and the device's own values for a
// field no claim states. A write happens only where the kernel does
// not already report the value, so a node no claim tunes takes no
// write at all.
//
// ABS_MT_SLOT is skipped. The kernel refuses a write to it, because
// the number of contacts a device reserved cannot change while the
// device exists.
func (n *nodeRelay) tune(mac string, overrides axisOverrides) {
	if n.source == nil {
		return
	}
	for _, original := range n.caps.Axes {
		if original.Code == absMTSlot {
			continue
		}
		current, err := n.source.axisRange(original.Code)
		if err != nil {
			fmt.Fprintf(os.Stderr, "relay: reading %s of controller %s: %v\n", n.sourcePath, publishedMAC(mac), err)
			continue
		}
		tuned := tunedAxis(original, current, overrides[original.Code])
		if tuned == current {
			continue
		}
		if err := n.source.setAxisRange(original.Code, tuned); err != nil {
			fmt.Fprintf(os.Stderr, "relay: tuning %s of controller %s: %v\n", n.sourcePath, publishedMAC(mac), err)
			continue
		}
		fmt.Printf("relay: controller %s reports %s with fuzz %d and flat %d\n",
			publishedMAC(mac), absCodeName(original.Code), tuned.Fuzz, tuned.Flat)
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
