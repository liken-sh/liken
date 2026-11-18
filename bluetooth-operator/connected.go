package main

// The Connected condition of a Peripheral is True while the device has
// a link with the radio. When it is False, the reason comes from the
// reason BlueZ gave when the link ended (linkend.go):
//
//   - Asleep: the link timed out on a device that disconnects when idle
//     and reconnects on its next press, such as a T6 remote. Such a
//     remote loses the report of the press that woke it, so the relay
//     writes a press in its place when it reconnects (wake.go).
//   - LinkLost: the link timed out on a device that stays connected
//     while it is on, so it left range or its battery ran out.
//   - ClosedByDevice: the device ended the link. An X6 air-mouse remote
//     does this when idle, and it reports the press that woke it, so
//     the relay writes no press for it.
//   - ClosedByRadio: this operator ended the link, to unpair the device
//     or to reconnect a controller whose HID device never attached
//     (inputrecovery.go).
//   - AuthenticationFailed: the keys did not match. It is the only
//     reason that needs a person.
//   - NotConnected: the operator has no reason for the link end. The
//     link ended while the operator was not running, or BlueZ gave
//     Unknown or Suspend. bluetoothd gives Unknown for each link when
//     it restarts.
//   - NotBonded: bluetoothd has no object for the device, so another
//     tool removed the bond.
//
// While the device stays disconnected and no new link-end signal
// arrives, the condition keeps its reason. So a remote that sleeps
// through an operator restart is still Asleep, and its next connect
// still gets the written press.

import (
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

const (
	conditionConnected = "Connected"

	conditionTrue  = conditions.True
	conditionFalse = conditions.False

	reasonLinkUp               = "LinkUp"
	reasonAsleep               = "Asleep"
	reasonLinkLost             = "LinkLost"
	reasonClosedByDevice       = "ClosedByDevice"
	reasonClosedByRadio        = "ClosedByRadio"
	reasonAuthenticationFailed = "AuthenticationFailed"
	reasonNotConnected         = "NotConnected"
	reasonNotBonded            = "NotBonded"
)

// connectedMessages is the condition message for each reason, which
// the Event of each transition also uses.
var connectedMessages = map[string]string{
	reasonLinkUp:               "The device has a link with the radio",
	reasonAsleep:               "The link timed out, and the device sleeps between sessions, so its next press connects it again",
	reasonLinkLost:             "The link timed out: the device went out of range, or its battery ran out",
	reasonClosedByDevice:       "The device ended the link: it was switched off, or it disconnected",
	reasonClosedByRadio:        "This radio ended the link, at an unpair or when the operator reconnected a controller that sent no input",
	reasonAuthenticationFailed: "The link ended because the keys did not match. Delete the Peripheral and pair the device again",
	reasonNotConnected:         "The link is down, and the operator received no reason from bluetoothd",
	reasonNotBonded:            "bluetoothd has no object for the device, because another tool removed the bond",
}

// connectedCondition returns the Connected condition of one bonded
// device. ended is the BlueZ reason of the last link end, or empty when
// no link-end signal arrived since the last pass. lastTransitionTime
// changes only when the status changes.
func connectedCondition(existing []Condition, device deviceState, present bool, ended string, now time.Time) Condition {
	condition := Condition{Type: conditionConnected, Status: conditionFalse}
	switch {
	case !present:
		condition.Reason = reasonNotBonded
	case device.Connected:
		condition.Status, condition.Reason = conditionTrue, reasonLinkUp
	case ended != "":
		condition.Reason = linkEndReason(ended, device)
	default:
		condition.Reason = previousReason(existing)
	}
	condition.Message = connectedMessages[condition.Reason]
	// The API server stores times to the second, so truncate to make a
	// rebuilt condition equal the stored one.
	condition.LastTransitionTime = now.UTC().Truncate(time.Second)
	list := slices.Clone(existing)
	conditions.Set(&list, condition)
	condition, _ = conditions.Find(list, conditionConnected)
	return condition
}

// linkEndReason returns the condition reason for a BlueZ link-end
// reason. BlueZ's Suspend and Unknown tell nothing about the device, so
// they return NotConnected.
func linkEndReason(ended string, device deviceState) string {
	switch ended {
	case blueZReasonTimeout:
		if device.Paired && sleepsBetweenSessions(device) {
			return reasonAsleep
		}
		return reasonLinkLost
	case blueZReasonRemote:
		return reasonClosedByDevice
	case blueZReasonLocal:
		return reasonClosedByRadio
	case blueZReasonAuthentication:
		return reasonAuthenticationFailed
	}
	return reasonNotConnected
}

// previousReason returns the reason that the Peripheral's condition
// already has, for a device that is still disconnected. A condition
// that was True lost its link with no signal, and one that was
// NotBonded now has a device object and no link, so both return
// NotConnected.
func previousReason(existing []Condition) string {
	previous, found := conditions.Find(existing, conditionConnected)
	if !found || previous.Status != conditionFalse || previous.Reason == reasonNotBonded {
		return reasonNotConnected
	}
	if _, known := connectedMessages[previous.Reason]; !known {
		return reasonNotConnected
	}
	return previous.Reason
}

// connectedWarning returns the status that makes the recorder post a
// transition to this reason as a Warning Event, or an empty status for
// a Normal Event.
func connectedWarning(reason string) conditions.Status {
	if reason == reasonAuthenticationFailed {
		return conditionFalse
	}
	return ""
}

// hidOverGATT is the Bluetooth assigned number of the HID over GATT
// service.
const hidOverGATT = 0x1812

// sleepsBetweenSessions reports whether a device is a Bluetooth Low
// Energy device, which drops its link when idle and reconnects on its
// next press. A random address is used only by Low Energy, and a Low
// Energy device with a public address still has the HID over GATT
// service.
func sleepsBetweenSessions(device deviceState) bool {
	if strings.EqualFold(device.AddressType, "random") {
		return true
	}
	for _, uuid := range device.UUIDs {
		if short, ok := shortUUID(uuid); ok && short == hidOverGATT {
			return true
		}
	}
	return false
}
