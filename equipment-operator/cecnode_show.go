package main

// A home press asks for the Player's picture on the TV. A TV that shows
// one of its own apps is on a different input, and selecting the
// receiver's input does not bring the picture back. So the Deployment
// writes a new status.session.showAt for the press (television_show.go),
// and the adapter that the CECBus assigns to the session's Display sends
// the TV Image View On and then Active Source for that Display, as every
// other claim of the input does (claimInput).
//
// The adapter sends Image View On even when the TV reports that it is
// on, because a TV on an internal app switches input after Image View
// On and Active Source, the One Touch Play sequence, and not after
// Active Source alone. The adapter does not read the TV's power first,
// because a TV that is still booting does not answer the read. Waking
// the TV is correct here, because a session with Awake set belongs to a
// Player that is on.
//
// HDMI-CEC 1.3a, CEC 13.1.2, says Text View On also removes the menus
// that the TV shows, but CEC 13.1.3 says a TV of an earlier version may
// not remove them. So a home press sends Image View On, the same
// message as every other claim of the input.

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// showState records which showAt values the node workload has handled,
// so that it handles each once. The node's mutex guards it. listed is
// true after the first passShow, foundAtStart is the showAt that the
// first passShow found, and done is the last showAt that the node
// workload sent or skipped.
type showState struct {
	listed       bool
	foundAtStart commandKey
	done         commandKey
}

func showKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, "show " + television.Status.Session.ShowAt}
}

// passShow claims the input once for each showAt, when the session has
// Awake set and this adapter is the one for the session's Display. A
// home press on a Player that is off turns the Player on, and that wake
// claims the input. A showAt that is already in the status when the node
// workload starts is from an earlier press, so it sends nothing. A press
// during a wake also sends nothing, and is not sent later, because the
// wake claims the input and guards it.
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
	prefix := fmt.Sprintf("Television %s: Player %s asked to show Display %s at %s", television.Metadata.Name, session.Player, session.Display, session.ShowAt)
	if waking {
		fmt.Fprintf(n.log, "%s: a wake is in progress and already claims the input, so the adapter on %s sends no more commands for this request\n", prefix, n.machine)
		return
	}
	if failed, err := n.claimInput(own, physical); err != nil {
		if cec.IsGone(err) {
			n.fail(err)
			return
		}
		fmt.Fprintf(os.Stderr, "%s: the adapter on %s did not send %s: %v\n", prefix, n.machine, failed, err)
		return
	}
	fmt.Fprintf(n.log, "%s: the adapter on %s sent Image View On and Active Source for %s\n", prefix, n.machine, physical)
}
