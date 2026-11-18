package main

// A serverUse is the state of the reservations that use the server
// that runs a device, or notInstalled for a device that names no parent.
// devicePhase reads it to set the phase of a device that has no pod.

import (
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type serverUse int

const (
	// notInstalled means the device names no parent, and the operator
	// creates nothing for it.
	notInstalled serverUse = iota
	// idle means the device is installed, and no reservation of its
	// server is active.
	idle
	// activating means a reservation that needs the device's server is
	// running its activation steps, which create the device's pod.
	activating
	// maintained means a reservation that uses the server is Ready, and
	// its runner creates the device's pod again when the pod is gone.
	maintained
	// releasing means a reservation that uses the server is
	// deactivating, and no reservation that uses it is Ready or
	// activating, so the deactivation stops the device.
	releasing
)

// serverUseOf returns the use of the server that runs the device
// (tree.server). A device whose spec a person edits during a session
// to name no parent gets the use of the server that the session still
// runs it on. A device that runs on no server is notInstalled when it
// names no parent, and idle otherwise, such as when its train is
// missing.
func (o *operator) serverUseOf(t *tree, d *device) serverUse {
	ref, placed := t.server(d)
	_, installed := d.declaredParent()
	switch {
	case placed:
		return o.useOfServer(t, ref)
	case !installed:
		return notInstalled
	}
	return idle
}

// waits reports whether the device's spec places it on a server whose
// current session started without it. The device then starts with that
// server's next session.
func (t *tree) waits(d *device) bool {
	declared, ok := t.declared(d)
	_, placed := t.server(d)
	return ok && !placed && t.started(declared)
}

// parentFound returns a device's ParentFound condition. When a session
// keeps the device on a server that its spec no longer places it on,
// the reason is MovePending, and the message says where the device
// moves when the session ends.
func (t *tree) parentFound(d *device) observatory.Condition {
	if missing := t.missingParent(d); missing != "" {
		return parentCondition(missing)
	}
	p, installed := d.declaredParent()
	on, placed := t.server(d)
	declared, ok := t.declared(d)
	to := "inventory"
	if installed {
		to = p.Kind.Name + " " + p.Name
	}
	switch {
	case placed && (!ok || declared != on):
		return condition(observatory.ConditionParentFound, observatory.ConditionTrue, reasonMovePending, "Moves to "+to+" when "+endOf(t.sessions().holders[on]))
	case !installed:
		return noParent()
	}
	return found()
}

const reasonMovePending = "MovePending"

// endOf returns text such as "Reservation east-tonight ends".
func endOf(reservations []string) string {
	if len(reservations) == 1 {
		return "Reservation " + reservations[0] + " ends"
	}
	return "Reservations " + strings.Join(reservations, ", ") + " end"
}

// notInstalledMessage returns the Ready message of a device that names no
// parent. The message names the kind of resource that a device of this
// kind can be installed in.
func notInstalledMessage(kind observatory.Kind) string {
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

// noParent is the ParentFound condition of a device that names no
// parent. It is True, because no parent is missing.
func noParent() observatory.Condition {
	return condition(observatory.ConditionParentFound, observatory.ConditionTrue, "NoParent", "The spec sets no parent")
}
