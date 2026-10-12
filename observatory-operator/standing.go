package main

// A device's standing is what its place in the tree and the
// reservations ask of its pod. It decides the phase of a device that
// has no pod: a device on the shelf is Inventory, an installed device
// with no active reservation is Idle, and a device whose reservation
// activates or is Ready is Starting until its pod exists.

import (
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type standing int

const (
	// onShelf: the device names no parent, and the operator creates
	// nothing for it.
	onShelf standing = iota
	// idle: the device is installed, and no reservation of its server
	// is active.
	idle
	// activating: a reservation that needs the device's server runs
	// its activation steps, which create the device's pod.
	activating
	// kept: a Ready reservation's runner keeps the pods of the
	// device's server, and creates a pod that is gone.
	kept
	// releasing: a reservation that needs the device's server
	// deactivates, and no other one keeps or activates it, so the
	// operator stops the device's driver, its pod, or its server.
	releasing
)

// standingOf answers a device's standing. The server that runs a
// device decides it (tree.server), so a device that a spec edit put on
// the shelf during a session stands with that session's server. A
// device whose train is missing has no server, and is idle.
func (o *operator) standingOf(t *tree, d *device) standing {
	ref, placed := t.server(d)
	_, installed := d.parent()
	switch {
	case placed:
		return o.serverStanding(t, ref)
	case !installed:
		return onShelf
	}
	return idle
}

// waits reports whether a device waits for the next session of the
// server that its parents place it on, because a session holds that
// server and did not start the device (tree.server).
func (t *tree) waits(d *device) bool {
	declared, ok := t.declared(d)
	_, placed := t.server(d)
	return ok && !placed && t.started(declared)
}

// parentFound answers a device's ParentFound condition. A device that
// a session holds where its parents no longer place it says where it
// goes when the session ends, and the condition's reason changes, so
// the move posts one Event when it is first deferred.
func (t *tree) parentFound(d *device) observatory.Condition {
	if missing := t.missingParent(d); missing != "" {
		return parentCondition(missing)
	}
	p, installed := d.parent()
	on, placed := t.server(d)
	declared, ok := t.declared(d)
	to := "the shelf"
	if installed {
		to = p.Kind.Name + " " + p.Name
	}
	switch {
	case placed && (!ok || declared != on):
		return condition(observatory.ConditionParentFound, observatory.ConditionTrue, reasonMovePending, "Moves to "+to+" when "+endOf(t.sessions().holders[on]))
	case !installed:
		return shelved()
	}
	return found()
}

// reasonMovePending is the reason of the ParentFound condition of a
// device that a session holds where its parents no longer place it.
const reasonMovePending = "MovePending"

// endOf names the end of the sessions of reservations, such as
// "Reservation east-tonight ends".
func endOf(reservations []string) string {
	if len(reservations) == 1 {
		return "Reservation " + reservations[0] + " ends"
	}
	return "Reservations " + strings.Join(reservations, ", ") + " end"
}

// shelfMessage names where a device of a kind can be installed, for
// the Ready condition of a device on the shelf.
func shelfMessage(kind observatory.Kind) string {
	switch kind {
	case observatory.MountKind, observatory.GPSKind, observatory.PolarAlignerKind:
		return "Not installed in a telescope"
	case observatory.DomeKind, observatory.WeatherStationKind:
		return "Not installed in an observatory"
	case observatory.SwitchKind, observatory.ReceiverKind:
		return "Not installed in a telescope or an observatory"
	}
	return "Not installed in an optical train"
}

// shelved is the ParentFound condition of a device on the shelf. It
// has no parent to look for, so nothing is missing.
func shelved() observatory.Condition {
	return condition(observatory.ConditionParentFound, observatory.ConditionTrue, "NoParent", "Names no parent")
}
