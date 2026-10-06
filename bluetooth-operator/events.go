package main

// The Events this operator posts, for a person who runs
// `kubectl describe` on a PairingRequest, a Peripheral, or a Node.
//
// An Event records what just happened: a window opened or closed, a
// device paired, a bond or a radio went away. A condition and a status
// field hold what is true now, and the API server deletes an Event an
// hour after its last write, so each fact here is also in a status
// field or a log line.
//
// The Connected condition posts no Event. A Low Energy remote drops its
// link between presses and pages the radio again on the next one, so
// its condition changes many times an hour. The condition, the log,
// and the bluetooth_disconnects_total counter hold those
// changes. Only the change that needs a person, a bond that left
// bluetoothd, posts BondLost.
//
// Peripherals and Nodes are cluster-scoped, so their Events are in
// `default`. A PairingRequest's Events are in its own namespace.

import "github.com/liken-sh/liken/kubernetes/events"

// The reasons this operator posts. They are part of its published
// interface, and the install guide lists them.
const (
	// reasonPairingWindowOpened is Normal, on a PairingRequest: the
	// radio is discoverable and pairable until the window closes.
	reasonPairingWindowOpened = "PairingWindowOpened"

	// reasonPairingWindowExpired is Normal, on a PairingRequest: the
	// window closed and nobody approved a device the radio observed.
	reasonPairingWindowExpired = "PairingWindowExpired"

	// reasonPairingRefused is a Warning, on a PairingRequest:
	// bluetoothd refused to pair the approved device. The window tries
	// again on each pass, and the Event is posted when the refusal
	// first appears or changes.
	reasonPairingRefused = "PairingRefused"

	// reasonPaired is Normal, on the PairingRequest and on the
	// Peripheral it created: the device holds a bond with the radio.
	reasonPaired = "Paired"

	// reasonBondLost is a Warning, on a Peripheral: bluetoothd holds
	// no bond with the device any more, which means somebody removed
	// it by another route. The Peripheral stays until a person
	// deletes it.
	reasonBondLost = "BondLost"

	// reasonInputRelayFailed is a Warning, on a Peripheral: the
	// operator could not make the virtual input device that a claim
	// on the controller receives.
	reasonInputRelayFailed = "InputRelayFailed"

	// reasonRadioClaimed is Normal, on the Node: bluetoothd in this
	// pod reports the radio, and the operator serves it.
	reasonRadioClaimed = "RadioClaimed"

	// reasonRadioLost is a Warning, on the Node: bluetoothd reports no
	// radio after it reported one, which means the adapter was
	// unplugged or reset.
	reasonRadioLost = "RadioLost"
)

// requestReference names a PairingRequest in an Event.
func requestReference(request *PairingRequest) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: pairingAPI,
		Kind:       pairingRequestKind,
		Namespace:  request.Metadata.Namespace,
		Name:       request.Metadata.Name,
		UID:        request.Metadata.UID,
	}
}

// peripheralReference names a Peripheral in an Event.
func peripheralReference(peripheral *Peripheral) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: pairingAPI,
		Kind:       peripheralKind,
		Name:       peripheral.Metadata.Name,
		UID:        peripheral.Metadata.UID,
	}
}

// controllerReference names the Peripheral of one controller by its
// address alone. The relays hold no Peripheral, and the recorder keys
// an Event with no UID on its kind and name.
func controllerReference(mac string) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: pairingAPI,
		Kind:       peripheralKind,
		Name:       deviceName(mac),
	}
}

// nodeReference names the Node an operator runs on, from the owner
// reference its ResourceSlice carries.
func nodeReference(owner OwnerReference) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: owner.APIVersion,
		Kind:       owner.Kind,
		Name:       owner.Name,
		UID:        owner.UID,
	}
}
