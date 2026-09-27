package main

// The node workload's part of a Television: it reads the TV's power
// when a command or a power press needs it, and it finds which adapter
// of a bus sends the bus's commands. The power it reads goes into the
// adapter's entry with the rest of the devices, and the Deployment
// copies it into the Television. Between reads, the power follows what
// the TV and the other devices send on the bus, as cec.Directory
// states, and no timer asks the TV. cecnode_power.go applies
// spec.power.

import (
	"fmt"
	"os"
	"slices"

	"github.com/liken-sh/equipment-operator/cec"
)

// readPower reads the TV's power into the directory, and asks the loop
// to write the entry when the read changed what the directory holds.
// Each wake of the loop runs a pass, which reads the API server, so a
// read that changes nothing does not wake it. An error that means the
// adapter left ends the node workload; any other error is logged, and
// the answer is unknown. askPower is the one caller.
func (n *cecNode) readPower(own cec.LogicalAddress) (cec.PowerStatus, error) {
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

// powerReadMemory is what the node workload holds about the power reads
// a power press asked for. The node's mutex guards it. listed says the
// node workload has read the Televisions once, foundAtStart is the
// request that first read held, and done is the last request the node
// workload answered or skipped.
type powerReadMemory struct {
	listed       bool
	foundAtStart commandKey
	done         commandKey
}

func powerReadKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, "power read " + television.Status.Session.PowerReadAt}
}

// passPowerRead answers a power press's request for the TV's power. The
// remote's power button toggles the room, and the TV's power decides
// whether the room is on. No timer keeps that power current, and a TV
// that a person turned off with its own remote can send nothing the
// adapter hears. So the Deployment writes a new status.session.powerReadAt
// for each press and waits a few seconds for the answer. The adapter
// that sends the bus's commands asks the TV once and writes the answer
// in status.powerRead, with an empty power when the TV did not answer.
// A request that is already in the status when the node workload starts
// is older than the press's wait, so the node workload sends nothing
// for it.
func (n *cecNode) passPowerRead(bus *CECBus, television *Television) {
	session := sessionOf(television)
	live := session != nil && session.PowerReadAt != ""
	var key commandKey
	if live {
		key = powerReadKeyOf(television)
	}
	n.mutex.Lock()
	first := !n.powerRead.listed
	n.powerRead.listed = true
	if first && live {
		n.powerRead.foundAtStart = key
	}
	skip := !live || key == n.powerRead.done || key == n.powerRead.foundAtStart
	n.mutex.Unlock()
	answered := live && television.Status.PowerRead != nil && television.Status.PowerRead.At == session.PowerReadAt
	if skip || answered {
		return
	}
	own, commands := n.commands(bus)
	if !commands {
		return
	}
	n.mutex.Lock()
	n.powerRead.done = key
	n.mutex.Unlock()
	power, err := n.askPower(own)
	if err != nil {
		return
	}
	answer := ""
	if power != cec.PowerUnknown {
		answer = power.String()
	}
	if err := ApplyTelevisionPowerRead(n.client, television, n.machine, session.PowerReadAt, answer); err != nil {
		fmt.Fprintf(os.Stderr, "writing the power read of Television %s: %v\n", television.Metadata.Name, err)
	}
}
