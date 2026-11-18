package main

// HDMI-CEC 1.3a, CEC 13.2.2: a source with no video to show may send the
// TV Inactive Source with its own physical address, and the TV then
// switches to another source or to its tuner. Without it, a TV that is
// still switched to a Display with no picture shows black until a person
// picks another input.
//
// The adapter sends Inactive Source only while the route, the physical
// address that the TV last switched to, leads to the Display. It sends
// it when the Player's screen goes dark, and when the node workload
// stops while no session is awake. A powerAsk that turns the room off
// sends Standby instead (cecnode_standby.go).

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// withdraw sends Inactive Source for physical when the route leads to
// it, and then clears the route, so the adapter does not answer a
// Request Active Source for a Display with no picture. why starts the
// log line. withdraw returns an error only when the adapter is gone,
// and logs any other send failure.
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
		fmt.Fprintf(os.Stderr, "%s; the adapter on %s did not send Inactive Source for %s: %v\n", why, n.machine, physical, err)
		return nil
	}
	n.mutex.Lock()
	n.route = routeState{address: cec.InvalidPhysicalAddress, known: true, words: "Inactive Source for " + physical.String()}
	n.mutex.Unlock()
	fmt.Fprintf(n.log, "%s; the adapter on %s sent Inactive Source for %s to the TV, because the route still led to the Display\n", why, n.machine, physical)
	return nil
}

// darkened reports whether the session that was awake is now asleep on the
// same Player and Display with no new standby. A powerAsk that turns
// the room off writes a Status.Session.StandbyAt that differs from
// Status.StandbyAt, and the standby path sends Standby for it.
func darkened(was, asleep *displaySession, television *Television) bool {
	if was == nil || asleep == nil || was.player != asleep.player || was.display != asleep.display {
		return false
	}
	session := television.Status.Session
	return session.StandbyAt == "" || session.StandbyAt == television.Status.StandbyAt
}

// withdrawOnStop sends Inactive Source when the node workload stops in
// Control mode and no session keeps the TV awake. While a session is
// awake, the Player keeps playing through a restart of this pod, and
// Inactive Source could make the TV switch away from the video.
func (n *cecNode) withdrawOnStop() {
	n.mutex.Lock()
	control := n.applied.mode == CECControl && n.entry.LogicalAddress != nil && n.awake == nil
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
