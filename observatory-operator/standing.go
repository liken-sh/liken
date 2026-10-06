package main

// A device's standing is what its place in the tree and the
// reservations ask of its pod. It decides the phase of a device that
// has no pod: a device on the shelf is Inventory, an installed device
// with no active reservation is Idle, and a device whose reservation
// activates or is Ready is Starting until its pod exists.

import "github.com/liken-sh/liken/observatory-operator/observatory"

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
)

// standingOf answers a device's standing. A device whose train is
// missing has no server, and is idle.
func (o *operator) standingOf(t *tree, d *device) standing {
	if _, installed := d.parent(); !installed {
		return onShelf
	}
	ref, placed := t.server(d)
	if !placed {
		return idle
	}
	return o.serverStanding(t, ref)
}

// shelfMessage names where a device of a kind can be installed, for
// the Ready condition of a device on the shelf.
func shelfMessage(kind observatory.Kind) string {
	switch kind {
	case observatory.MountKind, observatory.GPSKind, observatory.PolarAlignerKind:
		return "Not installed in a telescope"
	case observatory.DomeKind, observatory.WeatherStationKind:
		return "Not installed in an observatory"
	case observatory.SkyQualityMeterKind, observatory.SwitchKind, observatory.ReceiverKind:
		return "Not installed in a telescope or an observatory"
	}
	return "Not installed in an optical train"
}

// shelved is the ParentFound condition of a device on the shelf. It
// has no parent to look for, so nothing is missing.
func shelved() observatory.Condition {
	return condition(observatory.ConditionParentFound, observatory.ConditionTrue, "NoParent", "Names no parent")
}
