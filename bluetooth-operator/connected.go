package main

// The Connected condition, and the reasons it carries.
//
// Connected is the link state, in the standard condition shape. The
// reason says why a device is not connected, from the reason BlueZ gave
// when the link ended (linkend.go):
//
//   - LinkUp: the link is up.
//   - Asleep: the link timed out, and the device sleeps between
//     sessions. A Low Energy remote stops answering after an idle time
//     and pages the radio again on the next press, so it needs no
//     attention. This reason arms the press that wakes the room
//     (wake.go).
//   - LinkLost: the link timed out, and the device does not sleep
//     between sessions, so it went out of range or its battery ran out.
//   - ClosedByDevice: the device ended the link.
//   - ClosedByRadio: this radio ended the link, at an unpair or at the
//     input recovery.
//   - AuthenticationFailed: the link ended because the keys did not
//     match. It is the one reason that needs a person.
//   - NotConnected: the link is down, and this operator received no
//     reason. The link ended while the operator was not running, or
//     BlueZ reported Unknown or Suspend. A restart of bluetoothd reports
//     Unknown for each link.
//   - NotBonded: bluetoothd holds no object for the device, which means
//     the bond was removed by another route.
//
// The reason changes only when the link changes. A pass that finds the
// device still disconnected, and holds no reason from a signal, keeps
// the reason the Peripheral holds. So a restart of this operator keeps
// Asleep on a sleeping remote, and the press that wakes the room still
// follows the remote's next connect.

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

// connectedMessages is the message of each reason. The Event of each
// transition carries the same words (peripheral.go).
var connectedMessages = map[string]string{
	reasonLinkUp:               "the device holds a link with the radio",
	reasonAsleep:               "the link timed out, and the device sleeps between sessions, so its next press connects it again",
	reasonLinkLost:             "the link timed out: the device went out of range, or its battery ran out",
	reasonClosedByDevice:       "the device ended the link: it was switched off, or it disconnected",
	reasonClosedByRadio:        "this radio ended the link, at an unpair or at the input recovery",
	reasonAuthenticationFailed: "the link ended because the keys did not match; delete the Peripheral and pair the device again",
	reasonNotConnected:         "the link is down, and the operator received no reason from bluetoothd",
	reasonNotBonded:            "bluetoothd holds no object for the device, so the bond was removed by another route",
}

// connectedCondition reports the link state of one bonded device. ended
// is the reason BlueZ gave for the device's last link end, and empty
// when no signal arrived since the last pass wrote one.
//
// lastTransitionTime carries over from the condition the object already
// holds whenever the status is unchanged, so it marks when the link
// last changed and not when the operator last wrote the object.
func connectedCondition(held []Condition, device deviceState, present bool, ended string, now time.Time) Condition {
	condition := Condition{Type: conditionConnected, Status: conditionFalse}
	switch {
	case !present:
		condition.Reason = reasonNotBonded
	case device.Connected:
		condition.Status, condition.Reason = conditionTrue, reasonLinkUp
	case ended != "":
		condition.Reason = linkEndReason(ended, device)
	default:
		condition.Reason = keptReason(held)
	}
	condition.Message = connectedMessages[condition.Reason]
	// The API server keeps a date-time to the second, so the time is
	// cut to the second, and a status composed again compares equal to
	// the one the object holds.
	condition.LastTransitionTime = now.UTC().Truncate(time.Second)
	list := slices.Clone(held)
	conditions.Set(&list, condition)
	condition, _ = conditions.Find(list, conditionConnected)
	return condition
}

// linkEndReason turns the reason BlueZ gave into the condition's
// reason. A timeout is a sleep only for a device that sleeps between
// sessions, because a device that holds its link while it is on loses
// it only when it leaves range or its battery runs out. Suspend and
// Unknown say nothing about the device, so they read as no reason.
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

// keptReason is the reason of a device that is still disconnected with
// no new signal: the reason the Peripheral holds. A Peripheral that held
// LinkUp lost its link with no signal, and one that held NotBonded has
// a device object again with no link, so both give NotConnected.
func keptReason(held []Condition) string {
	previous, found := conditions.Find(held, conditionConnected)
	if !found || previous.Status != conditionFalse || previous.Reason == reasonNotBonded {
		return reasonNotConnected
	}
	if _, known := connectedMessages[previous.Reason]; !known {
		return reasonNotConnected
	}
	return previous.Reason
}

// connectedWarning is the status of the Connected condition that posts
// a Warning, for one reason. Only AuthenticationFailed needs a person,
// so every other transition posts a Normal Event.
func connectedWarning(reason string) conditions.Status {
	if reason == reasonAuthenticationFailed {
		return conditionFalse
	}
	return ""
}

// hidOverGATT is the assigned number of the HID over GATT service.
const hidOverGATT = 0x1812

// sleepsBetweenSessions reports whether a device that is not connected
// is expected to be off the air.
//
// A Low Energy device drops its link after a short idle and pages the
// radio again on the next press, so disconnected is its resting state.
// Two facts name one: a random address, which only Low Energy uses,
// and the HID over GATT service, which a device on a public address
// can still carry.
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
