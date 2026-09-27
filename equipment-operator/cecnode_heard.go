package main

// The lines the node workload writes for what it hears on the bus. A
// device that wakes the TV, changes its input, or moves the sound to
// the receiver does it with a message every device can hear, and the
// line names the device that sent it. Without the line, a person whose
// TV changes input sees only the result and not which device did it.
//
// In Listen the monitor receives every message on the bus. In Control
// the adapter is a follower, and the kernel passes a follower only the
// broadcasts and the messages to its own address. Image View On, Text
// View On, Inactive Source, and System Audio Mode Request go to the TV
// or the audio system, so an adapter in Control writes no line for
// them. The kernel also drops a broadcast Report Power Status for a
// claim that states a CEC version before 2.0, and the claim states 1.4.

import (
	"fmt"
	"strings"

	"github.com/liken-sh/equipment-operator/cec"
)

// logHeard writes one line for a message a person notices, whoever
// sent it. before and after are the sender as the directory held it
// before and after the message. own is the adapter's logical address
// in Control, and AddressUnregistered in Listen, where the adapter
// holds none.
//
// Two messages repeat without news. A device reports its power each
// time a device asks, and many devices ask on a timer, so a Report
// Power Status writes a line only when it changes the power the
// directory held. The directory also sets the TV's power from the
// messages that change it, such as a broadcast Standby, so a report
// that confirms such a change writes no line either. User Control Pressed writes a line only when
// the TV passes a press of its remote to this adapter; a press sent to
// another device is that device's business.
//
// The adapter's own messages never arrive here: the kernel does not
// pass a follower the messages it sent, and a monitor in Listen sends
// nothing. The lines for its commands are in cecnode_power.go.
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
		line = fmt.Sprintf("CECBus %s: %s sent %s to %s", bus, heardSender(after), words, heardReceiver(n.directory.Lookup(message.To), own))
	}
	fmt.Fprintln(n.log, line)
}

// heardSender names a device with every fact the directory holds for it,
// and says which facts it does not hold yet.
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
	return fmt.Sprintf("%s (%s)", heardName(peer), strings.Join(facts, ", "))
}

// heardReceiver names the device a directed message went to, with its
// logical address alone, because the sender's facts already make the
// line long.
func heardReceiver(peer cec.Peer, own cec.LogicalAddress) string {
	if own != cec.AddressUnregistered && peer.Logical == own {
		return fmt.Sprintf("this adapter (logical %d)", own)
	}
	return fmt.Sprintf("%s (logical %d)", heardName(peer), peer.Logical)
}

// heardName is the device's OSD name when it stated one, and its type
// until then.
func heardName(peer cec.Peer) string {
	if peer.OSDName != "" {
		return fmt.Sprintf("%q", peer.OSDName)
	}
	return typeNames[peer.Type]
}

// typeNames are the words for a device whose name is not known yet.
// The TV and the audio system exist at most once on a bus.
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
