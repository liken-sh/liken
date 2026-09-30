package main

// The adapter's answers to the two messages that ask a source to claim
// the input. HDMI-CEC 1.3a, CEC 13.2.2, gives both:
//
//   - A TV that comes out of standby, and a receiver that wakes with
//     it, broadcast Request Active Source, and the active source
//     answers with Active Source. A TV that hears no answer picks an
//     input of its own: its built-in home screen, or a source it sends
//     Set Stream Path. A wake's own Active Source can land while the TV
//     still boots, and the TV ignores it then, so the answer is what
//     puts the Player on the screen.
//   - A TV whose menu a person uses to pick an input broadcasts Set
//     Stream Path with that input's physical address, and the device
//     at that address answers with Active Source.
//
// The adapter answers only while a Player's session holds the room
// awake, and only for the session's Display. With no session awake the
// adapter is not the active source, and an answer would take the input
// from a person who watches another source. For the same reason it
// leaves a Request Active Source to another source when the route
// moved away from the Display after the adapter's own claim, such as to
// a streaming player a person chose: that source is the active source
// now. cec-compliance from
// v4l-utils checks the same rule from the other side: after it sends
// Active Source itself, its routing_control_req_active_source test
// fails a device that answers its Request Active Source
// (https://git.linuxtv.org/v4l-utils.git/tree/utils/cec-compliance/cec-test.cpp).
// The exception is a wake that has not sent its own Active Source yet:
// the wake is about to claim the input, and the route the adapter last
// heard can be from before the room woke. A Set Stream Path names the
// Display itself, so the TV has chosen it, and the adapter answers
// wherever the route led before.
//
// Each answer is Active Source alone, the response CEC 13.2.2 names.
// CEC 13.1.2 says a device sends Image View On only when its output
// needs the screen, and an answer serves the asker: a receiver that
// wakes for sound asks while the TV can be off, and Image View On would
// wake a TV a person turned off. A Set Stream Path comes from a TV that
// is on and already chose the Display, so it needs none either. The
// adapter sends Image View On with Active Source only when it claims
// the input for a person (claimInput).
//
// The adapter holds its active-source status only while the route
// leads to the Display. CEC 13.2.2 says a device that a switch
// deselects loses that status, so the route follows each routing
// message the adapter hears, not just Active Source: a Routing Change
// or a Routing Information from a switch, a Set Stream Path from the
// TV, and a Standby, after which no source is active. A session can
// stay awake while a person watches another source, so the room alone
// does not make the adapter the active source.

import (
	"fmt"
	"os"

	"github.com/liken-sh/equipment-operator/cec"
)

// routeState is where the bus's routing messages last sent the TV's
// picture. known is false until the adapter hears one, and an address
// that is not valid after one means no source is active, such as after
// a Standby.
type routeState struct {
	address cec.PhysicalAddress
	known   bool
}

// leadsTo answers whether the route shows a device at a physical
// address: the route names it, or names a switch above it. 0.0.0.0 is
// the TV's own tuner or apps, and leads to no source.
func (r routeState) leadsTo(physical cec.PhysicalAddress) bool {
	return r.address == physical || (r.address != 0 && r.address != cec.InvalidPhysicalAddress && r.address.Above(physical))
}

// followRoute moves the route on a routing message another device
// sent. An Active Source moves it in noteSource. A Standby ends the
// route when it reaches every device, the TV, or this adapter at own; a
// Standby to another device alone leaves the picture where it is.
func (n *cecNode) followRoute(message cec.Message, own cec.LogicalAddress) {
	opcode, _ := message.Opcode()
	operands := message.Operands()
	at := func(offset int) (cec.PhysicalAddress, bool) {
		if len(operands) < offset+2 {
			return cec.InvalidPhysicalAddress, false
		}
		return cec.PhysicalAddress(operands[offset])<<8 | cec.PhysicalAddress(operands[offset+1]), true
	}
	var address cec.PhysicalAddress
	var read bool
	switch {
	case message.IsPoll():
		return
	case opcode == cec.OpRoutingChange:
		address, read = at(2)
	case opcode == cec.OpRoutingInformation || opcode == cec.OpSetStreamPath:
		address, read = at(0)
	case opcode == cec.OpStandby:
		reaches := message.IsBroadcast() || message.To == cec.AddressTV || message.To == own
		address, read = cec.InvalidPhysicalAddress, reaches
	}
	if !read {
		return
	}
	n.mutex.Lock()
	n.route = routeState{address: address, known: true}
	n.mutex.Unlock()
}

// roomHold is the room a Player's session holds on this adapter's bus:
// the Television, the session's Player and Display, and the physical
// address the adapter announces for that Display.
type roomHold struct {
	television string
	player     string
	display    string
	physical   cec.PhysicalAddress
}

// holdRoom records the room the bus's Television's session holds, when
// the session is awake and this adapter speaks for its Display, and
// clears it otherwise. The pass runs it before the wake, so a request
// that arrives during a wake finds the room.
func (n *cecNode) holdRoom(bus *CECBus, television *Television) {
	var held *roomHold
	if session := sessionOf(television); session != nil && session.Awake {
		if _, physical, speaks := n.speaksFor(bus, session.Display); speaks {
			held = &roomHold{television: television.Metadata.Name, player: session.Player, display: session.Display, physical: physical}
		}
	}
	n.mutex.Lock()
	n.room = held
	n.mutex.Unlock()
}

// answerRouting answers a Request Active Source or a Set Stream Path
// for the Display that another device broadcast, while the room is
// held. from names the sender for the line. own is the adapter's
// logical address in Control.
func (n *cecNode) answerRouting(message cec.Message, from cec.Peer, own cec.LogicalAddress) {
	opcode, _ := message.Opcode()
	asks := opcode == cec.OpRequestActiveSource || opcode == cec.OpSetStreamPath
	if !asks || !message.IsBroadcast() || message.From == own {
		return
	}
	n.mutex.Lock()
	room, job, route := n.room, n.woken.job, n.route
	n.mutex.Unlock()
	if room == nil {
		return
	}
	words, _ := cec.Describe(message)
	asked := fmt.Sprintf("Television %s: %s broadcast %s", room.television, heardSender(from), words)
	if opcode == cec.OpSetStreamPath {
		if operands := message.Operands(); len(operands) < 2 || cec.PhysicalAddress(operands[0])<<8|cec.PhysicalAddress(operands[1]) != room.physical {
			return
		}
		n.answer(asked, own, room)
		return
	}
	claiming := job != nil && !job.claimed.Load()
	if !claiming && route.known && !route.leadsTo(room.physical) {
		reason := fmt.Sprintf("the bus last routed the picture to %s, and that source answers", route.address)
		if route.address == cec.InvalidPhysicalAddress {
			reason = "a Standby ended the adapter's claim of the input"
		}
		fmt.Fprintf(n.log, "%s; the adapter on %s sent nothing, because %s\n", asked, n.machine, reason)
		return
	}
	n.answer(asked, own, room)
}

// answer sends Active Source for the room's Display, and writes the
// line that starts with asked.
func (n *cecNode) answer(asked string, own cec.LogicalAddress, room *roomHold) {
	if failed, err := n.announceSource(own, room.physical); err != nil {
		if cec.IsGone(err) {
			n.fail(err)
			return
		}
		fmt.Fprintf(os.Stderr, "%s; the adapter on %s could not send %s: %v\n", asked, n.machine, failed, err)
		return
	}
	fmt.Fprintf(n.log, "%s; the adapter on %s answered with Active Source for %s, Display %s, because Player %s's session holds the room awake\n",
		asked, n.machine, room.physical, room.display, room.player)
}
