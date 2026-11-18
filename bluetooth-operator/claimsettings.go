package main

// The relay creates a virtual device for every evdev node of a
// controller whether or not a claim exists, so preparing a claim does
// not add or remove devices. The settings of the prepared claims set
// what each real node passes to its virtual device. A node with no
// claim passes nothing, and when several claims hold one controller,
// it passes the union of what they request.
//
// A claim sets two things on the operator's file descriptor for the
// real node. `inputs` sets an event mask, which affects only that
// descriptor. `axes` sets the fuzz and flat of absolute axes, which is
// how far a position must move before the kernel reports it. Fuzz and
// flat change the device for every reader, so tune writes the device's
// own values back when no claim sets them.

import (
	"fmt"
	"os"
)

// prepare records the settings of one claim on a controller and
// applies the union to its nodes. The kubelet can repeat a prepare, so
// a claim that is already recorded is overwritten with the same
// settings.
func (r *relays) prepare(mac, claimUID string, want inputSettings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	controller := r.controller(mac)
	controller.claims[claimUID] = want
	r.apply(mac, controller)
	fmt.Printf("relay: claim %s receives %s of controller %s\n", claimUID, want.classes, publishedMAC(mac))
}

// unprepare removes the settings of one claim. The kubelet gives only
// the claim UID, and one claim can hold several controllers, so this
// searches every controller.
func (r *relays) unprepare(claimUID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for mac, controller := range r.controllers {
		if _, found := controller.claims[claimUID]; !found {
			continue
		}
		delete(controller.claims, claimUID)
		r.apply(mac, controller)
	}
}

// restorePrepared rebuilds the settings of the claims that were
// prepared before this operator started. It reads the CDI spec files
// on the node, which record the inputs and axes of each claim, because
// reading the claims from the API server would need RBAC to read the
// claims of every workload.
func (r *relays) restorePrepared() {
	eachPreparedDevice(func(prepared preparedDevice) {
		mac, ok := prepared.controller()
		if !ok {
			return
		}
		want := inputSettings{classes: everyInputClass}
		if len(prepared.inputs) > 0 {
			parsed, err := parseInputClasses(prepared.inputs)
			if err != nil {
				// Keep every class, which is what the consumer received
				// before the restart.
				fmt.Fprintf(os.Stderr, "relay: reading the inputs of prepared claim %s: %v\n", prepared.claimUID, err)
			} else {
				want.classes = parsed
			}
		}
		if prepared.axes != "" {
			parsed, err := parseRecordedAxes(prepared.axes)
			if err != nil {
				// Leave the axes as the device reports them.
				fmt.Fprintf(os.Stderr, "relay: reading the axes of prepared claim %s: %v\n", prepared.claimUID, err)
			} else {
				want.axes = parsed
			}
		}
		r.prepare(mac, prepared.claimUID, want)
	})
}

// combinedSettings returns the union of the settings of every prepared
// claim on the controller. For each axis it takes the largest fuzz and the
// largest flat that any claim sets, so every claim gets at least the
// smoothing it asked for.
func (c *controllerRelay) combinedSettings() inputSettings {
	want := inputSettings{}
	for _, claim := range c.claims {
		want.classes |= claim.classes
		want.axes = want.axes.merge(claim.axes)
	}
	return want
}

// apply sets the combined settings of a controller on each of its
// nodes. The
// caller holds the lock.
func (r *relays) apply(mac string, controller *controllerRelay) {
	want := controller.combinedSettings()
	for _, relay := range controller.nodes {
		relay.applyMask(mac, want.classes)
		relay.tune(mac, want.axes)
	}
}

// tune sets the fuzz and flat of each absolute axis to the values the
// claims set, and to the device's own values where no claim sets one.
// It writes an axis only when the kernel reports a different value, so
// a device that keeps its own values gets no writes.
//
// It skips ABS_MT_SLOT, because the kernel refuses to change the
// number of touch slots while the device exists.
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

// applyMask sets the event mask on the real node to the wanted classes.
// While the controller is disconnected there is no real node, and read
// applies the mask when the controller reconnects.
func (n *nodeRelay) applyMask(mac string, want inputClasses) {
	if n.source == nil {
		return
	}
	masks := inputMasks(n.caps, want)
	apply, masked := masks, masks != nil
	if !masked {
		if !n.masked {
			return
		}
		apply = passEverything()
	}
	if err := n.source.setMasks(apply); err != nil {
		fmt.Fprintf(os.Stderr, "relay: limiting %s of controller %s: %v\n", n.sourcePath, publishedMAC(mac), err)
		return
	}
	n.masked = masked
}
