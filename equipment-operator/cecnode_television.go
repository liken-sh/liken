package main

// The node workload's part of a Television: it reads the TV's power
// on a timer, and it finds which adapter of a bus sends the bus's
// commands. The power it reads goes into the adapter's entry with the
// rest of the devices, and the Deployment copies it into the
// Television. cecnode_power.go applies spec.power.

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// How often an adapter in Control asks the TV for its power. One
// question and its answer take tens of milliseconds of the wire, so a
// read every ten seconds costs little, and a TV turned on or off with
// its own remote shows in the status within seconds instead of at the
// next scan a minute later.
var cecPowerInterval = 10 * time.Second

// readPower asks the TV for its power every cecPowerInterval until the
// mode ends. The directory takes the answer, and the entry carries it
// to the Deployment.
func (n *cecNode) readPower(ctx context.Context, own cec.LogicalAddress) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(cecPowerInterval):
		}
		if _, err := n.askPower(own); err != nil {
			return
		}
	}
}

// askPower reads the TV's power into the directory, and asks the loop
// to write the entry when the read changed what the directory holds.
// Each wake of the loop runs a pass, which reads the API server, so a
// read that changes nothing does not wake it. An error that means the
// adapter left ends the node workload; any other error is logged, and
// the answer is unknown.
func (n *cecNode) askPower(own cec.LogicalAddress) (cec.PowerStatus, error) {
	before := n.directory.Peers()
	power, err := cec.ReadPower(n.device, n.directory, own, cec.AddressTV)
	if err != nil && cec.IsGone(err) {
		n.fail(err)
		return cec.PowerUnknown, err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "asking the TV for its power from the adapter on %s: %v\n", n.machine, err)
	}
	if !slices.Equal(before, n.directory.Peers()) {
		n.markDirty()
	}
	return power, nil
}

// commands answers whether this adapter sends the bus's commands, and
// the logical address it sends them from. The first adapter in
// spec.adapters that holds a logical address sends them, so the TV
// gets one command and not one from each adapter. The node workload
// reads another adapter's state from that adapter's current entry, and
// its own state from memory, which is newer than its entry.
func (n *cecNode) commands(bus *CECBus) (cec.LogicalAddress, bool) {
	n.mutex.Lock()
	control := n.applied.mode == CECControl
	logical := n.entry.LogicalAddress
	n.mutex.Unlock()
	if !control || logical == nil {
		return 0, false
	}
	entries := reportedEntries(bus, n.now())
	for _, adapter := range bus.Spec.Adapters {
		if adapter.Machine == n.machine {
			return cec.LogicalAddress(*logical), true
		}
		if other, current := entries.current[adapter.Machine]; current && other.Mode == CECControl && other.LogicalAddress != nil {
			return 0, false
		}
	}
	return 0, false
}
