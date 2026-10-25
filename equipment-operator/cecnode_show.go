package main

// The node workload's part of a home press. A TV that shows its own
// apps is on another input, and the receiver's input alone does not
// bring the Player back. So a home press writes a new
// status.session.showAt (television_show.go), and the adapter that
// speaks for the session's Display sends the TV Inactive Source, Image
// View On, and then Active Source for that Display.
//
// Image View On goes first even when the TV reports On, as it does
// before every Active Source the adapter sends (claimInput). A TV on an
// internal app switches on Image View On followed by Active Source, the
// sequence One Touch Play sends, and not on a bare Active Source. The
// press reads no power first. A session that holds the room awake
// belongs to a Player that is on, because a home press on a Player that
// is off turns it on and the wake claims the input. So Image View On
// may wake the TV: a person who presses home on a Player that is on
// wants its picture. A read would only add a way to fail, because a TV
// that is still booting does not answer it.
//
// Inactive Source for the Display goes before the pair. An Android TV
// records the last Active Source it heard and switches only when a new
// one names another address. A TV that hears the wake's Active Source
// while it boots can record the Display and still open its own home
// screen, and after that it ignores every Active Source for the
// Display. Inactive Source clears the record, so the Active Source that
// follows is a change. The TV announces no change of its own input, so
// the adapter cannot tell this state from a TV that shows the Display,
// and it sends Inactive Source on every home press. A TV that already
// shows the Display can leave it for a moment before the pair brings it
// back.
//
// A home press sends Image View On and not Text View On. HDMI-CEC 1.3a,
// CEC 13.1.2, says Text View On also removes the menus the TV shows,
// but CEC 13.1.3 says a TV built before 1.3a may not remove them, so
// the gain depends on the TV. Image View On is the message every other
// claim sends, so the TV sees one sequence whatever asked for the
// input.

import (
	"fmt"
	"os"

	"github.com/liken-sh/equipment-operator/cec"
)

// showMemory is what the node workload holds about the home presses it
// answered. The node's mutex guards it. listed says the node workload
// has read the Televisions once, foundAtStart is the ask that first
// read held, and done is the last ask the node workload answered or
// skipped.
type showMemory struct {
	listed       bool
	foundAtStart commandKey
	done         commandKey
}

func showKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, "show " + television.Status.Session.ShowAt}
}

// passShow answers a home press's ask once, when the session holds the
// room awake and this adapter speaks for the session's Display. An ask
// that is already in the status when the node workload starts is older
// than the press, so the node workload sends nothing for it. A wake in
// progress claims the input and guards its claim, so the ask sends
// nothing more during it.
func (n *cecNode) passShow(bus *CECBus, television *Television) {
	session := sessionOf(television)
	live := session != nil && session.ShowAt != ""
	var key commandKey
	if live {
		key = showKeyOf(television)
	}
	n.mutex.Lock()
	first := !n.shown.listed
	n.shown.listed = true
	if first && live {
		n.shown.foundAtStart = key
	}
	skip := !live || key == n.shown.done || key == n.shown.foundAtStart
	n.mutex.Unlock()
	if skip || !session.Awake {
		return
	}
	own, physical, speaks := n.speaksFor(bus, session.Display)
	if !speaks {
		return
	}
	n.mutex.Lock()
	n.shown.done = key
	waking := n.woken.job != nil
	n.mutex.Unlock()
	asks := fmt.Sprintf("Television %s: Player %s asked to show Display %s at %s", television.Metadata.Name, session.Player, session.Display, session.ShowAt)
	if waking {
		fmt.Fprintf(n.log, "%s; the wake in progress claims the input, so the adapter on %s sends nothing more for the ask\n", asks, n.machine)
		return
	}
	if failed, err := n.reclaimInput(own, physical); err != nil {
		if cec.IsGone(err) {
			n.fail(err)
			return
		}
		fmt.Fprintf(os.Stderr, "%s; the adapter on %s could not send %s: %v\n", asks, n.machine, failed, err)
		return
	}
	fmt.Fprintf(n.log, "%s; the adapter on %s sent Inactive Source, Image View On, and Active Source for %s\n", asks, n.machine, physical)
}

// reclaimInput sends the TV Inactive Source for the Display and then
// claims the input with Image View On and Active Source. It answers the
// message it could not send.
func (n *cecNode) reclaimInput(own cec.LogicalAddress, physical cec.PhysicalAddress) (string, error) {
	if _, err := n.device.Transmit(cec.InactiveSource(own, physical), 0, 0); err != nil {
		return "Inactive Source for " + physical.String(), err
	}
	return n.claimInput(own, physical)
}
