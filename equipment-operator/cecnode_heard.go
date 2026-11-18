package main

// Log lines for the messages that the node workload receives on the
// CEC bus. When another device wakes the TV, changes its input, or moves
// its sound to the receiver, a person sees only the result. The line
// names the device that sent the message.
//
// In Listen the adapter is a monitor and receives every message on the
// bus. In Control the adapter is a follower, and the kernel delivers to
// a follower only broadcasts and messages addressed to it. Image View
// On, Text View On, Inactive Source, and System Audio Mode Request are
// addressed to the TV or the audio system, so in Control they write no
// line. The kernel also drops a broadcast Report Power Status when the
// adapter claims a CEC version before 2.0, and this adapter claims 1.4.

import (
	"fmt"
	"strings"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// logHeard writes a log line for a message that a person would want to
// see. before and after are the sender's directory entry before and
// after the message was applied. own is the adapter's logical address
// in Control, and AddressUnregistered in Listen, where it has none.
//
// Report Power Status writes a line only when it changes the sender's
// power in the directory, because devices report their power whenever
// they are asked, and many ask on a timer. User Control Pressed writes
// a line only when it is addressed to this adapter, because a press
// sent to another device is for that device.
//
// The adapter's own messages do not arrive here, because the kernel
// does not deliver a follower's messages back to it, and a monitor
// sends none. cecnode_power.go writes the lines for the adapter's
// commands.
func (n *cecNode) logHeard(bus string, message cec.Message, before, after cec.Peer, own cec.LogicalAddress) {
	words, visible := cec.Describe(message)
	if !visible {
		return
	}
	switch opcode, _ := message.Opcode(); {
	case opcode == cec.OpReportPowerStatus && after.Power == before.Power:
		return
	case opcode == cec.OpUserControlPressed && (own == cec.AddressUnregistered || message.To != own):
		return
	}
	line := fmt.Sprintf("CECBus %s: %s broadcast %s", bus, heardSender(after), words)
	if !message.IsBroadcast() {
		line = fmt.Sprintf("CECBus %s: %s sent %s to %s", bus, heardSender(after), words, describeReceiver(n.directory.Lookup(message.To), own))
	}
	fmt.Fprintln(n.log, line)
}

// heardSender describes the sender of a message: its name or type, its
// logical address, its physical address when known, and which of the
// name and the physical address the directory does not have yet.
func heardSender(peer cec.Peer) string {
	facts := []string{fmt.Sprintf("logical %d", peer.Logical)}
	var unknown []string
	if peer.OSDName == "" {
		unknown = append(unknown, "name")
	}
	if peer.Physical == cec.InvalidPhysicalAddress {
		unknown = append(unknown, "physical address")
	} else {
		facts = append(facts, peer.Physical.String())
	}
	if len(unknown) > 0 {
		facts = append(facts, strings.Join(unknown, " and ")+" not known yet")
	}
	return fmt.Sprintf("%s (%s)", deviceName(peer), strings.Join(facts, ", "))
}

// describeReceiver describes the device that a directed message is
// addressed to. It gives only the logical address, because the sender's
// description already makes the line long.
func describeReceiver(peer cec.Peer, own cec.LogicalAddress) string {
	if own != cec.AddressUnregistered && peer.Logical == own {
		return fmt.Sprintf("this adapter (logical %d)", own)
	}
	return fmt.Sprintf("%s (logical %d)", deviceName(peer), peer.Logical)
}

// deviceName returns the device's OSD name when it has reported one,
// and otherwise a description of its type.
func deviceName(peer cec.Peer) string {
	if peer.OSDName != "" {
		return fmt.Sprintf("%q", peer.OSDName)
	}
	return typeNames[peer.Type]
}

// typeNames describes each device type, for a device whose OSD name is
// not known. A bus has at most one TV and one audio system, so those
// two read as "the TV" and "the audio system".
var typeNames = map[cec.DeviceType]string{
	cec.TypeTV:           "the TV",
	cec.TypeAudioSystem:  "the audio system",
	cec.TypeRecording:    "a recording device",
	cec.TypeTuner:        "a tuner",
	cec.TypePlayback:     "a playback device",
	cec.TypeSwitch:       "a switch",
	cec.TypeProcessor:    "a video processor",
	cec.TypeBackup:       "a backup device",
	cec.TypeSpecific:     "a specific-use device",
	cec.TypeUnregistered: "an unregistered device",
}
