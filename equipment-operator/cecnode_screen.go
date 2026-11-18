package main

// The TV can request that the Player's screen wake or sleep.
// media-operator controls that screen, so the node workload writes the
// request to the Television's status.screenAsk. media-operator passes
// each new request to the Player's screen client, which acts on it as on
// a key press. The session then wakes or sleeps, and the node workload
// follows the session as it does for any other wake or sleep.
//
// A Set Stream Path for the Display's address requests a wake. The TV
// sends it when a person selects the Player's input in the TV's source
// menu while the session sleeps. HDMI-CEC 1.3a, CEC 13.2.2, says the
// device at that address leaves standby and broadcasts Active Source
// once it has stable video. The Display has no picture until the screen
// wakes, so the adapter sends Active Source in the wake that follows
// (cecnode_source.go).
//
// A Standby while the session is awake requests a sleep. CEC 13.3.2 lets
// a device ignore Standby, but a screen left awake while the TV is in
// standby keeps the receiver on with nobody watching.

import (
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// streamPathRequest records a Set Stream Path from the TV for the Display
// of a sleeping session, and the time the adapter received it.
type streamPathRequest struct {
	physical cec.PhysicalAddress
	at       time.Time
}

// unwrittenScreenAsk is a request that writeScreenAsk has not written yet,
// and the Television it is for.
type unwrittenScreenAsk struct {
	television string
	ask        TelevisionScreenAsk
}

// queueScreenAsk runs on the CEC read loop, so it only stores the
// request and wakes the reconcile loop, whose next pass writes it. An API call here
// would delay every message received after it, and HDMI-CEC 1.3a, CEC
// 9.2, gives a follower one second to answer.
func (n *cecNode) queueScreenAsk(room *displaySession, screen, heard, words string) {
	ask := TelevisionScreenAsk{At: n.now().UTC().Format(wakeTimeLayout), Player: room.player, Screen: screen, Cause: heard}
	n.mutex.Lock()
	n.screenAsk = &unwrittenScreenAsk{television: room.television, ask: ask}
	n.mutex.Unlock()
	fmt.Fprintf(n.log, "Television %s: %s; %s\n", room.television, heard, words)
	sendWake(n.wake)
}

// recordStreamPath records a Set Stream Path for the Display of a sleeping
// session and requests a screen wake.
func (n *cecNode) recordStreamPath(asked string, room *displaySession) {
	n.mutex.Lock()
	n.streamPath = &streamPathRequest{physical: room.physical, at: n.now()}
	n.mutex.Unlock()
	n.queueScreenAsk(room, screenWake, asked, fmt.Sprintf(
		"Player %s's session is asleep, so the adapter on %s asked the Player's screen to wake, and sends Active Source for %s when the session wakes",
		room.player, n.machine, room.physical))
}

// consumeStreamPath reports whether the wake about to claim the input follows
// the TV's own selection of the Display, and clears the record. When it
// returns true, claimSource sends Active Source without Image View On.
// The selection counts only while the bus route still leads to the
// Display and for cecWakeFresh after it arrived. Later than that, a
// person may have turned the TV off with its own remote, and the adapter
// may have received no message about it, so the wake sends Image View On
// as well.
func (n *cecNode) consumeStreamPath(physical cec.PhysicalAddress) bool {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	request := n.streamPath
	n.streamPath = nil
	return request != nil && request.physical == physical && n.route.address == physical && n.now().Sub(request.at) <= cecWakeFresh
}

// streamPathPending reports whether the TV selected the Display and the
// screen has not woken yet. ownPower reports that time as In transition
// Standby to On.
func (n *cecNode) streamPathPending() bool {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	return n.streamPath != nil && n.route.address == n.streamPath.physical && n.now().Sub(n.streamPath.at) <= cecWakeFresh
}

// ownPower is the power status that the adapter sends in Report Power
// Status (HDMI-CEC 1.3a, CEC 13.14). The machine never sleeps, but a TV
// that queries its sources, for example to build its source list, treats
// a source that reports On as one with a picture to show. So the adapter
// reports On only while a Player's session is awake on its Display.
func (n *cecNode) ownPower() cec.PowerStatus {
	n.mutex.Lock()
	awake := n.awake != nil
	n.mutex.Unlock()
	switch {
	case awake:
		return cec.PowerOn
	case n.streamPathPending():
		return cec.PowerToOn
	}
	return cec.PowerStandby
}

// sleepOnStandby requests a screen sleep when a Standby arrives while
// the session is awake. It acts on a Standby from the TV that is
// broadcast or addressed to this adapter. From any other device it acts
// only on a broadcast Standby, because a broadcast Standby puts the
// whole system in standby (CEC 13.3.2).
func (n *cecNode) sleepOnStandby(message cec.Message, from cec.Peer, own cec.LogicalAddress) {
	opcode, _ := message.Opcode()
	if opcode != cec.OpStandby || message.IsPoll() || message.From == own {
		return
	}
	reaches := message.IsBroadcast() || (message.To == own && message.From == cec.AddressTV)
	n.mutex.Lock()
	room := n.awake
	n.mutex.Unlock()
	if !reaches || room == nil {
		return
	}
	words, _ := cec.Describe(message)
	sender := "broadcast"
	if !message.IsBroadcast() {
		sender = "sent the adapter"
	}
	n.queueScreenAsk(room, screenSleep, fmt.Sprintf("%s %s %s", heardSender(from), sender, words), fmt.Sprintf(
		"the room goes to standby with the TV, so the adapter on %s asked Player %s's screen to sleep",
		n.machine, room.player))
}

// writeScreenAsk writes the stored request to the bus's Television. It
// drops the request when its Television was deleted or is no longer the
// bus's, and when the apply returns a conflict, which happens when a new
// object of the same name replaced the Television, because the apply
// sets metadata.uid. Any other error keeps the request and retries.
func (n *cecNode) writeScreenAsk(television *Television) {
	n.mutex.Lock()
	record := n.screenAsk
	n.mutex.Unlock()
	if record == nil {
		return
	}
	err := apiclient.ErrNotFound
	if television != nil && television.Metadata.Name == record.television {
		err = ApplyTelevisionScreenAsk(n.client, television, n.machine, record.ask)
	}
	if err != nil && err != apiclient.ErrNotFound && err != apiclient.ErrConflict {
		fmt.Fprintf(os.Stderr, "writing the screen request of Television %s: %v\n", record.television, err)
		n.retryLater()
		return
	}
	n.mutex.Lock()
	if n.screenAsk == record {
		n.screenAsk = nil
	}
	n.mutex.Unlock()
}
