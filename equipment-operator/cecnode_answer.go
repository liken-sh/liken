package main

// While a Player's session on a Television is awake, the adapter that
// speaks for the session's Display answers Request Active Source and a
// Set Stream Path for that Display with Active Source (HDMI-CEC 1.3a,
// CEC 13.2.2).
//
// A TV that leaves standby, and a receiver that wakes with it,
// broadcast Request Active Source. When no device answers, the TV
// switches to its home screen or to a source of its own choice. The TV
// can ignore the wake's own Active Source while it boots, so this
// answer is what puts the Player on the screen. A TV broadcasts Set
// Stream Path when a person picks an input in its menu. When that input
// is the Display of a sleeping session, the adapter asks the Player's
// screen to wake (cecnode_screen.go) and answers when the session wakes.
//
// The session can stay awake while a person watches another source. So
// the adapter does not answer a Request Active Source after the route
// moved away from the Display, because the source that the route leads
// to is the active source, and an answer would take the input from it.
// cec-compliance from v4l-utils tests the same rule: its
// routing_control_req_active_source test sends Active Source and then
// fails a device that answers its Request Active Source
// (https://git.linuxtv.org/v4l-utils.git/tree/utils/cec-compliance/cec-test.cpp).
// During a wake the adapter answers anyway until the wake has sent its
// own Active Source, and after a Routing Information, because the wake
// is about to claim the input and the route can be from before the room
// woke. A Set Stream Path for the Display is answered whatever the
// route was, because the TV chose the Display.
//
// The answer is Active Source alone. CEC 13.1.2 says a device sends
// Image View On only when its output needs the screen. A receiver that
// wakes for sound sends Request Active Source while the TV can be off,
// and Image View On would turn on a TV that a person turned off. A TV
// that sends Set Stream Path is already on. The adapter sends Image View
// On only when it claims the input for a person (claimInput).

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// routeState is where the last routing message on the bus sent the
// TV's picture. known is false until the adapter receives one. After
// that, cec.InvalidPhysicalAddress means that no source is active, as
// after a Standby. by is the opcode of that message, and words
// describes it for the wake's log line.
//
// chosen is true when a person moved the route. It ends a wake's guard
// instead of letting the guard claim the input back (guardMove in
// cecnode_source.go). These messages set it:
//   - a Routing Change, which a switch broadcasts when a person
//     switches it by hand
//   - a Set Stream Path from the TV, which it broadcasts when a person
//     picks a source in its menu
//   - a Standby, because claiming the input again would turn on a TV
//     that a person turned off
//   - the TV's Active Source for 0.0.0.0, its own tuner or apps, which
//     noteSource records
//
// An Active Source from a source device does not set chosen, because a
// streaming player sends one by itself when the room wakes. A Routing
// Information does not either. CEC 13.2.2 says a switch sends it when
// it answers a Routing Change or leaves standby, and it reports the
// switch's current route, which can be from before the room woke.
type routeState struct {
	address cec.PhysicalAddress
	known   bool
	chosen  bool
	by      cec.Opcode
	words   string
}

// leadsTo reports whether the route is physical or the address of a
// switch above it. 0.0.0.0 is the TV's own tuner or apps, and leads to
// no source.
func (r routeState) leadsTo(physical cec.PhysicalAddress) bool {
	return r.address == physical || (r.address != 0 && r.address != cec.InvalidPhysicalAddress && r.address.Above(physical))
}

// followRoute moves the route on a Routing Change, a Routing
// Information, a Set Stream Path, or a Standby. CEC 13.2.2 says that a
// device a switch deselects is no longer the active source, so the
// route follows these messages and not only Active Source, which
// noteSource handles. A Standby clears the route only when it is
// broadcast or sent to the TV or to this adapter.
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
	sendWake(n.sources)
}

// displaySession is a Television session that this adapter speaks for: the
// Television, the session's Player and Display, and the physical
// address that the adapter announces for the Display.
type displaySession struct {
	television string
	player     string
	display    string
	physical   cec.PhysicalAddress
}

// holdRoom records the bus's Television session when this adapter
// speaks for the session's Display: in n.awake while the session is
// awake, and in n.asleep while it sleeps. It clears both otherwise. The
// reconcile pass runs it before the wake, so a request that arrives
// during a wake finds the room. When the session goes to sleep with no
// new standbyAt, the adapter sends Inactive Source (darkened, in
// cecnode_inactive.go).
func (n *cecNode) holdRoom(bus *CECBus, television *Television) {
	var held, asleep *displaySession
	var own cec.LogicalAddress
	if session := sessionOf(television); session != nil {
		var physical cec.PhysicalAddress
		var speaks bool
		if own, physical, speaks = n.speaksFor(bus, session.Display); speaks {
			room := &displaySession{television: television.Metadata.Name, player: session.Player, display: session.Display, physical: physical}
			if session.Awake {
				held = room
			} else {
				asleep = room
			}
		}
	}
	n.mutex.Lock()
	was := n.awake
	n.awake, n.asleep = held, asleep
	n.mutex.Unlock()
	if darkened(was, asleep, television) {
		why := fmt.Sprintf("Television %s: Player %s's screen went dark", television.Metadata.Name, asleep.player)
		if err := n.withdraw(own, asleep.physical, why); err != nil {
			n.fail(err)
		}
	}
}

// answerRouting answers a broadcast Request Active Source or Set Stream
// Path from another device. from is the sender, for the log line, and
// own is the logical address that the adapter claimed in Control mode.
func (n *cecNode) answerRouting(message cec.Message, from cec.Peer, own cec.LogicalAddress) {
	opcode, _ := message.Opcode()
	requests := opcode == cec.OpRequestActiveSource || opcode == cec.OpSetStreamPath
	if !requests || !message.IsBroadcast() || message.From == own {
		return
	}
	n.mutex.Lock()
	room, asleep, job, route := n.awake, n.asleep, n.woken.job, n.route
	n.mutex.Unlock()
	words, _ := cec.Describe(message)
	received := fmt.Sprintf("%s broadcast %s", heardSender(from), words)
	if opcode == cec.OpSetStreamPath {
		operands := message.Operands()
		if len(operands) < 2 {
			return
		}
		requested := cec.PhysicalAddress(operands[0])<<8 | cec.PhysicalAddress(operands[1])
		switch {
		case room != nil && requested == room.physical:
			n.answer(fmt.Sprintf("Television %s: %s", room.television, received), own, room)
		case room == nil && asleep != nil && requested == asleep.physical:
			n.recordStreamPath(received, asleep)
		}
		return
	}
	if room == nil {
		return
	}
	prefix := fmt.Sprintf("Television %s: %s", room.television, received)
	claiming := job != nil && (!job.claimed.Load() || route.by == cec.OpRoutingInformation)
	if !claiming && route.known && !route.leadsTo(room.physical) {
		reason := fmt.Sprintf("the bus last routed the picture to %s, and that source answers", route.address)
		if route.address == cec.InvalidPhysicalAddress {
			reason = "a Standby ended the adapter's claim of the input"
		}
		fmt.Fprintf(n.log, "%s; the adapter on %s sent nothing, because %s\n", prefix, n.machine, reason)
		return
	}
	n.answer(prefix, own, room)
}

// answer broadcasts Active Source for the room's Display and logs the
// result after prefix.
func (n *cecNode) answer(prefix string, own cec.LogicalAddress, room *displaySession) {
	if failed, err := n.announceSource(own, room.physical); err != nil {
		if cec.IsGone(err) {
			n.fail(err)
			return
		}
		fmt.Fprintf(os.Stderr, "%s; the adapter on %s did not send %s: %v\n", prefix, n.machine, failed, err)
		return
	}
	fmt.Fprintf(n.log, "%s; the adapter on %s answered with Active Source for %s, Display %s, because Player %s's session is awake\n",
		prefix, n.machine, room.physical, room.display, room.player)
}
