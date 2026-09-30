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
// from a person who watches another source. A Set Stream Path for the
// Display of a session that sleeps is a person's pick of the Player in
// the TV's menu, and the adapter asks the Player's screen to wake
// (cecnode_screen.go) and answers once the session wakes. For the same reason it
// leaves a Request Active Source to another source when the route
// moved away from the Display after the adapter's own claim, such as to
// a streaming player a person chose: that source is the active source
// now. cec-compliance from
// v4l-utils checks the same rule from the other side: after it sends
// Active Source itself, its routing_control_req_active_source test
// fails a device that answers its Request Active Source
// (https://git.linuxtv.org/v4l-utils.git/tree/utils/cec-compliance/cec-test.cpp).
// The exceptions are a wake that has not sent its own Active Source
// yet, because the wake is about to claim the input and the route the
// adapter last heard can be from before the room woke, and a Routing
// Information during a wake, for the same reason. A Set Stream Path names the
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
// a Standby. by is the opcode of the message that moved it.
//
// chosen says a person moved the route through the TV or a switch: a
// Routing Change, which a switch broadcasts when a person switches it
// by hand; a Set Stream Path from the TV, which it broadcasts when a
// person picks a source in its menu; the TV's own Active Source for
// 0.0.0.0, which it broadcasts for its tuner or apps (HDMI-CEC 1.3a,
// CEC 13.2.2); and a Standby, after which a claim would wake a TV that
// a person turned off. A source device's Active Source is no such
// choice, because a streaming player sends one by itself when the room
// wakes. A Routing Information is none either: CEC 13.2.2 says a
// switch sends it when it answers a Routing Change or comes out of
// standby, and it reports the route the switch holds, which can be
// from before the room woke. words names the message that moved the
// route, for the wake's line.
type routeState struct {
	address cec.PhysicalAddress
	known   bool
	chosen  bool
	by      cec.Opcode
	words   string
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
	words, _ := cec.Describe(message)
	chosen := opcode == cec.OpRoutingChange || opcode == cec.OpStandby || (opcode == cec.OpSetStreamPath && message.From == cec.AddressTV)
	n.mutex.Lock()
	n.route = routeState{address: address, known: true, chosen: chosen, by: opcode, words: words}
	n.mutex.Unlock()
	poke(n.sources)
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
// this adapter speaks for its Display: as the room held awake when the
// session is awake, and as the sleeping room when it is not. It clears
// both otherwise. The pass runs it before the wake, so a request that
// arrives during a wake finds the room.
//
// A room that the pass sees go dark, with no standby, gets Inactive
// Source (cecnode_inactive.go).
func (n *cecNode) holdRoom(bus *CECBus, television *Television) {
	var held, asleep *roomHold
	var own cec.LogicalAddress
	if session := sessionOf(television); session != nil {
		var physical cec.PhysicalAddress
		var speaks bool
		if own, physical, speaks = n.speaksFor(bus, session.Display); speaks {
			room := &roomHold{television: television.Metadata.Name, player: session.Player, display: session.Display, physical: physical}
			if session.Awake {
				held = room
			} else {
				asleep = room
			}
		}
	}
	n.mutex.Lock()
	was := n.room
	n.room, n.asleep = held, asleep
	n.mutex.Unlock()
	if darkened(was, asleep, television) {
		why := fmt.Sprintf("Television %s: Player %s's screen went dark", television.Metadata.Name, asleep.player)
		if err := n.withdraw(own, asleep.physical, why); err != nil {
			n.fail(err)
		}
	}
}

// answerRouting answers a Request Active Source or a Set Stream Path
// for the Display that another device broadcast, while the room is
// held. A Set Stream Path for the Display of a sleeping session asks
// the screen to wake instead. from names the sender for the line. own is the adapter's
// logical address in Control.
func (n *cecNode) answerRouting(message cec.Message, from cec.Peer, own cec.LogicalAddress) {
	opcode, _ := message.Opcode()
	asks := opcode == cec.OpRequestActiveSource || opcode == cec.OpSetStreamPath
	if !asks || !message.IsBroadcast() || message.From == own {
		return
	}
	n.mutex.Lock()
	room, asleep, job, route := n.room, n.asleep, n.woken.job, n.route
	n.mutex.Unlock()
	words, _ := cec.Describe(message)
	heard := fmt.Sprintf("%s broadcast %s", heardSender(from), words)
	if opcode == cec.OpSetStreamPath {
		operands := message.Operands()
		if len(operands) < 2 {
			return
		}
		named := cec.PhysicalAddress(operands[0])<<8 | cec.PhysicalAddress(operands[1])
		switch {
		case room != nil && named == room.physical:
			n.answer(fmt.Sprintf("Television %s: %s", room.television, heard), own, room)
		case room == nil && asleep != nil && named == asleep.physical:
			n.pickDisplay(heard, asleep)
		}
		return
	}
	if room == nil {
		return
	}
	asked := fmt.Sprintf("Television %s: %s", room.television, heard)
	// A Routing Information during a wake can be the stale route of a
	// receiver that wakes with the room, so it alone does not end the
	// answer while the wake runs.
	claiming := job != nil && (!job.claimed.Load() || route.by == cec.OpRoutingInformation)
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
