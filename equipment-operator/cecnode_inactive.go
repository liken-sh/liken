package main

// Inactive Source. HDMI-CEC 1.3a, CEC 13.2.2, says a source that has no
// video to present may send the TV Inactive Source with its own
// physical address, and the TV then shows another source or its own
// tuner. The adapter sends it when the Player's screen goes dark while
// the route still leads to the Display, and when the node workload
// stops while no session holds the room awake, because a TV that
// still routes the picture to a dark Display shows black until a
// person picks another input. A room that goes to
// standby gets Standby from the power press instead (cecnode_standby.go),
// and a route that a person moved to another source is that source's,
// so neither sends Inactive Source.

import (
	"fmt"
	"os"

	"github.com/liken-sh/equipment-operator/cec"
)

// withdraw sends the TV Inactive Source for the Display's physical
// address when the route still leads there, and clears the route, so
// the adapter answers no Request Active Source for a dark Display. why
// starts the line. It answers an error only when the adapter left.
func (n *cecNode) withdraw(own cec.LogicalAddress, physical cec.PhysicalAddress, why string) error {
	n.mutex.Lock()
	route := n.route
	n.mutex.Unlock()
	if !route.known || !route.leadsTo(physical) {
		return nil
	}
	if _, err := n.device.Transmit(cec.InactiveSource(own, physical), 0, 0); err != nil {
		if cec.IsGone(err) {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s; the adapter on %s could not send Inactive Source for %s: %v\n", why, n.machine, physical, err)
		return nil
	}
	n.mutex.Lock()
	n.route = routeState{address: cec.InvalidPhysicalAddress, known: true, words: "Inactive Source for " + physical.String()}
	n.mutex.Unlock()
	fmt.Fprintf(n.log, "%s; the adapter on %s sent the TV Inactive Source for %s, because the route still led to the Display\n", why, n.machine, physical)
	return nil
}

// darkened answers whether the pass sees the room's screen go dark: the
// session that held the room awake sleeps now on the same Display, and
// no power press put the room in standby. A power press writes a new
// standbyAt with the sleep, and the standby sends the TV Standby.
func darkened(was, asleep *roomHold, television *Television) bool {
	if was == nil || asleep == nil || was.player != asleep.player || was.display != asleep.display {
		return false
	}
	session := television.Status.Session
	return session.StandbyAt == "" || session.StandbyAt == television.Status.StandbyAt
}

// withdrawOnStop sends Inactive Source when the node workload stops in
// Control while the route leads to its Display and no Player's session
// holds the room awake. A session that holds the room awake keeps its
// picture: the Player plays on through a restart of this pod, so
// Inactive Source would tell the TV something false, and the TV could
// switch away from a film.
func (n *cecNode) withdrawOnStop() {
	n.mutex.Lock()
	control := n.applied.mode == CECControl && n.entry.LogicalAddress != nil && n.room == nil
	var own cec.LogicalAddress
	if control {
		own = cec.LogicalAddress(*n.entry.LogicalAddress)
	}
	announced := n.entry.PhysicalAddress
	n.mutex.Unlock()
	physical, err := cec.ParsePhysicalAddress(announced)
	if !control || err != nil {
		return
	}
	_ = n.withdraw(own, physical, fmt.Sprintf("the node workload on %s stops", n.machine))
}
