package main

// The asks the node workload hears on the bus for the Player's screen.
// A Player's screen wakes and sleeps in media-operator, so the node
// workload writes each ask in the Television's status.screenAsk.
// media-operator watches the Television objects and relays each new ask
// to the Player's screen client, which wakes or sleeps as a press would
// make it. The session then follows the screen, and the node workload
// acts on the session as it does for any other wake or sleep.
//
// Two messages ask. The TV sends Set Stream Path for the Display when
// a person picks the Player's input in the TV's source menu, and the
// Player's session sleeps: HDMI-CEC 1.3a, CEC 13.2.2, says the device
// at that address comes out of standby and, when it has stable video,
// broadcasts Active Source. The adapter has no picture until the
// screen wakes, so it sends nothing yet; the wake that the woken
// session starts sends Active Source (cecnode_source.go). And the TV
// sends Standby when a person turns it off, while the session holds the
// room awake: the room goes dark with the TV, so the Player's screen
// sleeps too. CEC 13.3.2 allows a device to ignore Standby, and liken
// follows it because a screen that stays awake behind a TV in standby
// holds the receiver and the room awake for no one.

import (
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// streamPick is the TV's Set Stream Path for the Display that asked a
// sleeping session's screen to wake, and when the adapter heard it.
type streamPick struct {
	physical cec.PhysicalAddress
	at       time.Time
}

// screenAskRecord is one ask the API server has not accepted yet, and
// the Television it belongs to.
type screenAskRecord struct {
	television string
	ask        TelevisionScreenAsk
}

// askScreen records an ask of the room's Player's screen, writes its
// line, and asks the loop for a pass, which writes it. It runs on the
// read loop, so the write waits for the pass: an API call here would
// hold every message behind it, and a follower answers within one
// second (CEC 9.2).
func (n *cecNode) askScreen(room *roomHold, screen, heard, words string) {
	ask := TelevisionScreenAsk{At: n.now().UTC().Format(wakeTimeLayout), Player: room.player, Screen: screen, Cause: heard}
	n.mutex.Lock()
	n.screenAsk = &screenAskRecord{television: room.television, ask: ask}
	n.mutex.Unlock()
	fmt.Fprintf(n.log, "Television %s: %s; %s\n", room.television, heard, words)
	poke(n.wake)
}

// pickDisplay answers the TV's Set Stream Path for the Display of a
// session that sleeps: the adapter records the pick and asks the
// Player's screen to wake. It sends no Active Source now, because the
// Display has no picture until the screen wakes.
func (n *cecNode) pickDisplay(asked string, room *roomHold) {
	n.mutex.Lock()
	n.picked = &streamPick{physical: room.physical, at: n.now()}
	n.mutex.Unlock()
	n.askScreen(room, screenWake, asked, fmt.Sprintf(
		"Player %s's session is asleep, so the adapter on %s asked the Player's screen to wake, and sends Active Source for %s when the session wakes",
		room.player, n.machine, room.physical))
}

// takePick answers whether the wake about to claim the input answers
// the TV's own pick of the Display, and forgets the pick. The pick
// counts only while the route still leads to the Display and for
// cecWakeFresh after the adapter heard it: a TV that a person turned
// off since then with its own remote can send nothing the adapter
// hears, and a wake long after must send Image View On to reach it.
func (n *cecNode) takePick(physical cec.PhysicalAddress) bool {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	picked := n.picked
	n.picked = nil
	return picked != nil && picked.physical == physical && n.route.address == physical && n.now().Sub(picked.at) <= cecWakeFresh
}

// pickPending answers whether the TV picked the Display and the screen
// has not woken yet, which the adapter reports as its power moving from
// Standby to On.
func (n *cecNode) pickPending() bool {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	return n.picked != nil && n.route.address == n.picked.physical && n.now().Sub(n.picked.at) <= cecWakeFresh
}

// ownPower is the power the adapter reports for itself in Report Power
// Status (HDMI-CEC 1.3a, CEC 13.14). The machine never sleeps, but a
// playback device with no picture is in standby to the TV, and a TV
// that asks the power of its sources, such as for its source list,
// reads On as a source it can show. So the adapter reports On while a
// Player's session holds the room awake on its Display, In transition
// Standby to On from the TV's pick of a sleeping session's Display
// until the screen wakes, and Standby otherwise.
func (n *cecNode) ownPower() cec.PowerStatus {
	n.mutex.Lock()
	awake := n.room != nil
	n.mutex.Unlock()
	switch {
	case awake:
		return cec.PowerOn
	case n.pickPending():
		return cec.PowerToOn
	}
	return cec.PowerStandby
}

// sleepOnStandby asks the Player's screen to sleep for a Standby that
// reaches the room while its session holds the room awake. A Standby
// from the TV counts when it is broadcast or directed to this adapter.
// A Standby from any other device counts only when it is broadcast,
// because a broadcast Standby puts the whole system in standby (CEC
// 13.3.2), and a directed one from a device that is not the TV asks
// nothing a person asked of this room.
func (n *cecNode) sleepOnStandby(message cec.Message, from cec.Peer, own cec.LogicalAddress) {
	opcode, _ := message.Opcode()
	if opcode != cec.OpStandby || message.IsPoll() || message.From == own {
		return
	}
	reaches := message.IsBroadcast() || (message.To == own && message.From == cec.AddressTV)
	n.mutex.Lock()
	room := n.room
	n.mutex.Unlock()
	if !reaches || room == nil {
		return
	}
	words, _ := cec.Describe(message)
	sender := "broadcast"
	if !message.IsBroadcast() {
		sender = "sent the adapter"
	}
	n.askScreen(room, screenSleep, fmt.Sprintf("%s %s %s", heardSender(from), sender, words), fmt.Sprintf(
		"the room goes to standby with the TV, so the adapter on %s asked Player %s's screen to sleep",
		n.machine, room.player))
}

// writeScreenAsk writes an ask the API server has not accepted yet on
// the bus's Television. An ask for a Television that is gone, or no
// longer the bus's, is dropped: the Player it named left the room.
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
		fmt.Fprintf(os.Stderr, "writing the screen ask of Television %s: %v\n", record.television, err)
		n.retryLater()
		return
	}
	n.mutex.Lock()
	if n.screenAsk == record {
		n.screenAsk = nil
	}
	n.mutex.Unlock()
}
