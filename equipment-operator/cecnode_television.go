package main

// The node workload's part of a Television. It reads the TV's power for
// a powerAsk, and it determines which adapter of a bus sends the bus's
// commands. The power it reads is written to the adapter's entry in the
// CECBus, and the Deployment copies it to the Television. Between reads,
// cec.Directory updates the power from the messages that the TV and the
// other devices send, so no timer queries the TV. cecnode_power.go
// applies spec.power.

import (
	"fmt"
	"os"
	"slices"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// readPower reads the TV's power into the directory. Marking the entry
// dirty starts a pass, so readPower marks it only when the directory
// changed, and a read that changes nothing starts no pass. An error that means the adapter is gone ends the node workload. Any
// other error is logged, and the power is PowerUnknown.
func (n *cecNode) readPower(own cec.LogicalAddress) (cec.PowerStatus, error) {
	before := n.directory.Peers()
	power, err := cec.ReadPower(n.device, n.directory, own, cec.AddressTV)
	if err != nil && cec.IsGone(err) {
		n.fail(err)
		return cec.PowerUnknown, err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the power of the TV through the adapter on %s: %v\n", n.machine, err)
	}
	if !slices.Equal(before, n.directory.Peers()) {
		n.markDirty()
	}
	return power, nil
}

// commands returns whether this adapter sends the bus's commands, and
// the logical address it sends them from. The sender is the first
// adapter in spec.adapters that is in Control mode and has a logical
// address, so the TV receives each command once. This adapter's own
// state is read from memory, which is newer than its entry. Another
// adapter counts only while its entry is current.
func (n *cecNode) commands(bus *CECBus) (cec.LogicalAddress, bool) {
	n.mutex.Lock()
	control := n.applied.mode == CECControl
	logical := n.entry.LogicalAddress
	n.mutex.Unlock()
	if !control || logical == nil {
		return 0, false
	}
	entries := reportedEntries(bus, n.liveness())
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

// powerReadState records which power read requests the node workload
// has handled. The node's mutex guards it.
type powerReadState struct {
	// listed is true after the first pass that read the Televisions.
	listed bool
	// foundAtStart is the request that the first pass found.
	foundAtStart commandKey
	// done is the last request that this adapter read the power for.
	done commandKey
}

func powerReadKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, "power read " + television.Status.Session.PowerReadAt}
}

// passPowerRead handles a powerAsk's request for the TV's power. The
// operator decides from the TV's power whether the room is on, and
// status.power can be out of date: a TV that a
// person turned off with its own remote can send nothing that the
// adapter receives. So for each powerAsk the Deployment writes a new
// status.session.powerReadAt and waits up to cecPowerReadWait. The
// adapter that sends the bus's commands reads the TV's power once and
// writes it to status.powerRead, with an empty power when the TV did
// not reply. A request that is already in the status when the node
// workload starts is older than that wait, so the node workload skips
// it.
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
	power, err := n.readTVPower(own)
	if err != nil {
		return
	}
	answer := ""
	if power != cec.PowerUnknown {
		answer = power.String()
	}
	if err := ApplyTelevisionPowerRead(n.client, television, n.machine, session.PowerReadAt, answer); err != nil {
		fmt.Fprintf(os.Stderr, "writing the power reading of Television %s: %v\n", television.Metadata.Name, err)
	}
}

// postTelevision posts the Event for a condition that the node workload
// wrote on a Television, when the condition is new or its status or
// reason changed. Call it only after the API server accepted the write.
func (n *cecNode) postTelevision(television *Television, written Condition) {
	postTransitions(n.recorder, reference("Television", television.Metadata), television.Status.Conditions, []Condition{written})
}
